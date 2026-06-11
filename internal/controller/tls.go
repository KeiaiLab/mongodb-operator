/*
Copyright 2026 keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonscertmanager "github.com/keiailab/keiailab-commons/pkg/certmanager"
	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
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

// CertificateGVK 는 cert-manager Certificate CR 의 GroupVersionKind —
// keiailab-commons pkg/certmanager 단일 진실원 alias (v0.11.0 승격).
var CertificateGVK = commonscertmanager.CertificateGVK

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

	// SAN: cluster.Name + mongos service + cfg/shard headless services.
	// 4단 FQDN 확장은 commons ServiceSANs 위임 (SAN 목록 조립은 도메인 책임 잔류).
	dnsNames := []string{mdbsh.Name}
	dnsNames = append(dnsNames, commonscertmanager.ServiceSANs(mdbsh.Name+"-mongos", mdbsh.Namespace, false)...)
	dnsNames = append(dnsNames, commonscertmanager.ServiceSANs(mdbsh.Name+"-cfg-headless", mdbsh.Namespace, false)...)
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		dnsNames = append(dnsNames,
			commonscertmanager.ServiceSANs(fmt.Sprintf("%s-shard-%d-headless", mdbsh.Name, i), mdbsh.Namespace, false)...)
	}

	// IssuerKind 빈 값 → "Issuer" fallback 은 commons BuildCertificate 내장
	// (기존 거동과 동일 — 출력 byte-identical 보존, 운영 cert 재발급 트리거 0).
	return commonscertmanager.BuildCertificate(commonscertmanager.CertParams{
		Name:      mdbsh.Name + "-tls",
		Namespace: mdbsh.Namespace,
		Labels: map[string]string{
			"app.kubernetes.io/name":       "mongodbsharded",
			"app.kubernetes.io/instance":   mdbsh.Name,
			"app.kubernetes.io/managed-by": "keiailab-mongodb-operator",
			"mongodb.keiailab.com/role":    "server-tls",
		},
		SecretName:      TLSCertSecretName(mdbsh),
		CommonName:      mdbsh.Name,
		DNSNames:        dnsNames,
		IssuerName:      issuer.Name,
		IssuerKind:      issuer.Kind,
		Duration:        mdbsh.Spec.TLS.CertManager.Duration,
		RenewBefore:     mdbsh.Spec.TLS.CertManager.RenewBefore,
		ECDSAPrivateKey: true,
	})
}

// TLSCertSecretNameForRS returns the TLS cert Secret name for a MongoDB ReplicaSet.
func TLSCertSecretNameForRS(mdb *mongodbv1alpha1.MongoDB) string {
	if mdb.Spec.TLS != nil && mdb.Spec.TLS.CustomCert != nil &&
		mdb.Spec.TLS.CustomCert.SecretName != "" {
		return mdb.Spec.TLS.CustomCert.SecretName
	}
	return mdb.Name + "-tls"
}

func buildRSCertificate(mdb *mongodbv1alpha1.MongoDB) *unstructured.Unstructured {
	if mdb.Spec.TLS == nil || !mdb.Spec.TLS.Enabled ||
		mdb.Spec.TLS.CertManager == nil || mdb.Spec.TLS.CertManager.IssuerRef.Name == "" {
		return nil
	}
	issuer := mdb.Spec.TLS.CertManager.IssuerRef

	// SAN: cluster.Name + headless/client service. wildcard=true 로 RS
	// per-pod DNS (`*.<svc>.<ns>.svc.cluster.local`) 까지 커버 (기존 addSvc 동일).
	// 순서 보존: [name, headless SANs..., client SANs...] (cert 재발급 트리거 0).
	headlessSANs := commonscertmanager.ServiceSANs(mdb.Name+"-headless", mdb.Namespace, true)
	clientSANs := commonscertmanager.ServiceSANs(mdb.Name, mdb.Namespace, true)
	dnsNames := make([]string, 0, 1+len(headlessSANs)+len(clientSANs))
	dnsNames = append(dnsNames, mdb.Name)
	dnsNames = append(dnsNames, headlessSANs...)
	dnsNames = append(dnsNames, clientSANs...)

	return commonscertmanager.BuildCertificate(commonscertmanager.CertParams{
		Name:      mdb.Name + "-tls",
		Namespace: mdb.Namespace,
		Labels: map[string]string{
			"app.kubernetes.io/name":       "mongodb",
			"app.kubernetes.io/instance":   mdb.Name,
			"app.kubernetes.io/managed-by": "keiailab-mongodb-operator",
			"mongodb.keiailab.com/role":    "server-tls",
		},
		SecretName:      TLSCertSecretNameForRS(mdb),
		CommonName:      mdb.Name,
		DNSNames:        dnsNames,
		IssuerName:      issuer.Name,
		IssuerKind:      issuer.Kind,
		Duration:        mdb.Spec.TLS.CertManager.Duration,
		RenewBefore:     mdb.Spec.TLS.CertManager.RenewBefore,
		ECDSAPrivateKey: true,
	})
}

func (r *MongoDBReconciler) reconcileTLS(
	ctx context.Context,
	mdb *mongodbv1alpha1.MongoDB,
) error {
	cert := buildRSCertificate(mdb)
	if cert == nil {
		return nil
	}
	logger := log.FromContext(ctx)

	if err := controllerutil.SetControllerReference(mdb, cert, r.Scheme); err != nil {
		return fmt.Errorf("set controller reference: %w", err)
	}

	desired := cert.DeepCopy()
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, cert, func() error {
		if spec, found, _ := unstructured.NestedMap(desired.Object, "spec"); found {
			_ = unstructured.SetNestedField(cert.Object, spec, "spec")
		}
		cert.SetLabels(desired.GetLabels())
		return controllerutil.SetControllerReference(mdb, cert, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("upsert tls Certificate: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		logger.Info("TLS Certificate reconciled", "operation", op, "name", cert.GetName())
	}
	return nil
}

// MongoTLSMountPath 는 mongod pod 가 server cert/key 를 읽는 경로.
// Phase 3 의 mongod args (--tlsCertificateKeyFile) 가 본 경로 + PEM merge file 참조.
const MongoTLSMountPath = "/etc/ssl/mongo"

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
