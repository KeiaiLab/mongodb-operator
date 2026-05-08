/*
Copyright 2026 keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"fmt"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Pillar P7 — TLS 통합 3-phase roadmap (postgres-operator 패턴 정합).
//
//   Phase 1 (1.4.x): spec.tls field facade — 이미 정의 (api/v1alpha1/common_types.go).
//                    condition TLSReady emit 만 (cycle 19 까지 facade only).
//   Phase 2 (1.4.14): cert-manager Certificate CR 자동 emit (본 파일). IssuerRef 명시 +
//                     Enabled=true 시 reconciler 가 <cluster>-tls Secret 자동 발급 위임.
//   Phase 3 (별 cycle): STS volume mount + mongod.conf net.tls.mode=requireSSL +
//                       net.tls.certificateKeyFile + net.tls.CAFile.
//
// Certificate CR 은 cert-manager.io/v1 group. operator 는 unstructured.Unstructured 로
// emit — cert-manager Go SDK 의존 회피 (cert-manager 미설치 cluster 도 정상 동작).

// CertificateGVK 는 cert-manager Certificate CR 의 GroupVersionKind.
var CertificateGVK = schema.GroupVersionKind{
	Group:   "cert-manager.io",
	Version: "v1",
	Kind:    "Certificate",
}

// TLSCertSecretName 은 cluster 의 server cert Secret 이름을 결정한다.
// "<cluster>-tls" default — 사용자 customCert.secretName 이 명시되면 그것 사용.
func TLSCertSecretName(mdbsh *mongodbv1alpha1.MongoDBSharded) string {
	if mdbsh.Spec.TLS != nil && mdbsh.Spec.TLS.CustomCert != nil &&
		mdbsh.Spec.TLS.CustomCert.SecretName != "" {
		return mdbsh.Spec.TLS.CustomCert.SecretName
	}
	return mdbsh.Name + "-tls"
}

// buildCertificate 는 cert-manager Certificate CR 을 반환한다 (Phase 2).
//
// SAN: cluster.Name + mongos service + 모든 shard headless service + cfg headless.
// MongoDB client 의 hostname verify 시 TLS handshake 통과.
//
// duration / renewBefore / privateKey rotation 은 사용자 spec.tls.certManager 우선,
// 미명시 시 cert-manager default (90d / 15d / Always) 사용.
func buildCertificate(mdbsh *mongodbv1alpha1.MongoDBSharded) *unstructured.Unstructured {
	if mdbsh.Spec.TLS == nil || !mdbsh.Spec.TLS.Enabled ||
		mdbsh.Spec.TLS.CertManager == nil || mdbsh.Spec.TLS.CertManager.IssuerRef.Name == "" {
		return nil
	}
	issuer := mdbsh.Spec.TLS.CertManager.IssuerRef
	kind := issuer.Kind
	if kind == "" {
		kind = "Issuer"
	}

	// SAN: cluster.Name + mongos service + cfg/shard headless services.
	dnsNames := []any{mdbsh.Name}
	addSvc := func(svc string) {
		dnsNames = append(dnsNames,
			svc,
			fmt.Sprintf("%s.%s", svc, mdbsh.Namespace),
			fmt.Sprintf("%s.%s.svc", svc, mdbsh.Namespace),
			fmt.Sprintf("%s.%s.svc.cluster.local", svc, mdbsh.Namespace),
		)
	}
	addSvc(mdbsh.Name + "-mongos")
	addSvc(mdbsh.Name + "-cfg-headless")
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		addSvc(fmt.Sprintf("%s-shard-%d-headless", mdbsh.Name, i))
	}

	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(CertificateGVK)
	cert.SetName(mdbsh.Name + "-tls")
	cert.SetNamespace(mdbsh.Namespace)
	cert.SetLabels(map[string]string{
		"app.kubernetes.io/name":       "mongodbsharded",
		"app.kubernetes.io/instance":   mdbsh.Name,
		"app.kubernetes.io/managed-by": "keiailab-mongodb-operator",
		"mongodb.keiailab.com/role":    "server-tls",
	})

	spec := map[string]any{
		"secretName": TLSCertSecretName(mdbsh),
		"commonName": mdbsh.Name,
		"dnsNames":   dnsNames,
		"issuerRef": map[string]any{
			"name":  issuer.Name,
			"kind":  kind,
			"group": "cert-manager.io",
		},
		"usages": []any{"server auth", "client auth"},
		"privateKey": map[string]any{
			"algorithm":      "ECDSA",
			"size":           int64(256),
			"rotationPolicy": "Always",
		},
	}
	if d := mdbsh.Spec.TLS.CertManager.Duration; d != "" {
		spec["duration"] = d
	}
	if rb := mdbsh.Spec.TLS.CertManager.RenewBefore; rb != "" {
		spec["renewBefore"] = rb
	}
	if err := unstructured.SetNestedField(cert.Object, spec, "spec"); err != nil {
		// programming error — spec 은 단순 map. recover 불필요.
		return nil
	}
	return cert
}

// reconcileTLS 는 Pillar P7 의 cert-manager Certificate CR upsert 를 처리한다.
// TLS 미활성 또는 IssuerRef 미설정 시 no-op (CustomCert 패턴은 사용자가 직접 Secret bootstrap).
func (r *MongoDBShardedReconciler) reconcileTLS(
	ctx context.Context,
	mdbsh *mongodbv1alpha1.MongoDBSharded,
) error {
	cert := buildCertificate(mdbsh)
	if cert == nil {
		return nil
	}
	logger := log.FromContext(ctx)

	if err := controllerutil.SetControllerReference(mdbsh, cert, r.Scheme); err != nil {
		return fmt.Errorf("set controller reference: %w", err)
	}

	desired := cert.DeepCopy()
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, cert, func() error {
		// CreateOrUpdate 가 fetch 후 mutator 안에서 cert 객체에 기존 metadata 채움.
		// desired 의 spec 으로 강제 동기화.
		if spec, found, _ := unstructured.NestedMap(desired.Object, "spec"); found {
			_ = unstructured.SetNestedField(cert.Object, spec, "spec")
		}
		cert.SetLabels(desired.GetLabels())
		return controllerutil.SetControllerReference(mdbsh, cert, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("upsert tls Certificate: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		logger.Info("TLS Certificate reconciled", "operation", op, "name", cert.GetName())
	}
	return nil
}
