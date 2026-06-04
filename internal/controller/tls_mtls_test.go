/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// tls_mtls_test.go — Phase 5.4 mTLS: cert-manager Certificate 에 X.509 cluster
// membership subject (O/OU) 가 MTLS 활성 시 추가되는지 회귀 가드. envtest 불요.

package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	securitypkg "github.com/keiailab/mongodb-operator/internal/security"
)

func mdbWithMTLS(enabled bool) *mongodbv1alpha1.MongoDB {
	tls := &mongodbv1alpha1.TLSSpec{
		Enabled:     true,
		CertManager: &mongodbv1alpha1.CertManagerSpec{IssuerRef: mongodbv1alpha1.CertIssuerRef{Name: "ca-issuer"}},
	}
	if enabled {
		tls.MTLS = &mongodbv1alpha1.MTLSSpec{Enabled: true}
	}
	return &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "test-mongodb", Namespace: "default"},
		Spec:       mongodbv1alpha1.MongoDBSpec{TLS: tls},
	}
}

func TestBuildRSCertificate_MTLSEnabled_AddsMembershipSubject(t *testing.T) {
	cert := buildRSCertificate(mdbWithMTLS(true))
	if cert == nil {
		t.Fatal("buildRSCertificate returned nil")
	}
	orgs, found, err := unstructured.NestedStringSlice(cert.Object, "spec", "subject", "organizations")
	if err != nil || !found {
		t.Fatalf("spec.subject.organizations 부재 (found=%v err=%v) — MTLS 활성 시 membership subject 기대", found, err)
	}
	if len(orgs) != 1 || orgs[0] != securitypkg.MembershipOrg {
		t.Errorf("organizations = %v, want [%s]", orgs, securitypkg.MembershipOrg)
	}
	ous, _, _ := unstructured.NestedStringSlice(cert.Object, "spec", "subject", "organizationalUnits")
	if len(ous) != 1 || ous[0] != "test-mongodb" {
		t.Errorf("organizationalUnits = %v, want [test-mongodb]", ous)
	}
}

func TestBuildRSCertificate_MTLSDisabled_NoSubject(t *testing.T) {
	cert := buildRSCertificate(mdbWithMTLS(false))
	if cert == nil {
		t.Fatal("buildRSCertificate returned nil")
	}
	if _, found, _ := unstructured.NestedMap(cert.Object, "spec", "subject"); found {
		t.Error("MTLS 미설정 시 spec.subject 미기대 (server cert 기본 subject 유지)")
	}
}
