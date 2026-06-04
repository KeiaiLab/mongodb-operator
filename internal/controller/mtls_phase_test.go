/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mtls_phase_test.go — Phase 5.4-c2 reconcileMTLSPhase 배선 회귀 가드 (fake client).
// 안전 단계 결정 로직은 internal/security TestResolveMTLSStep 가 별도 커버.

package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func mtlsMdb(mode string, members int32) *mongodbv1alpha1.MongoDB {
	return &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members: members,
			TLS: &mongodbv1alpha1.TLSSpec{
				Enabled: true,
				MTLS:    &mongodbv1alpha1.MTLSSpec{Enabled: true, Mode: mode},
			},
		},
	}
}

func readySts() *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: 3},
	}
}

func TestReconcileMTLSPhase_DisabledNoOp(t *testing.T) {
	s := newTestScheme(t)
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec:       mongodbv1alpha1.MongoDBSpec{Members: 3}, // TLS.MTLS 없음
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdb).WithStatusSubresource(mdb).Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	if err := r.reconcileMTLSPhase(context.Background(), mdb); err != nil {
		t.Fatalf("err=%v", err)
	}
	if mdb.Status.MTLSPhase != "" {
		t.Errorf("MTLS 비활성 시 MTLSPhase 빈값 기대, got %q", mdb.Status.MTLSPhase)
	}
}

func TestReconcileMTLSPhase_NewClusterStartsSafe(t *testing.T) {
	s := newTestScheme(t)
	mdb := mtlsMdb("x509", 3) // 신규: sts 없음 → rolloutReady=false → keyFile 부터
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdb).WithStatusSubresource(mdb).Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	if err := r.reconcileMTLSPhase(context.Background(), mdb); err != nil {
		t.Fatalf("err=%v", err)
	}
	if mdb.Status.MTLSPhase != "keyFile" {
		t.Errorf("신규 클러스터는 keyFile 부터 안전 시작 기대, got %q", mdb.Status.MTLSPhase)
	}
	if mdb.Spec.TLS.MTLS.Mode != "keyFile" {
		t.Errorf("effective mode=keyFile 기대 (StatefulSet 빌드용), got %q", mdb.Spec.TLS.MTLS.Mode)
	}
}

func TestReconcileMTLSPhase_RolloutReadyAdvancesOneStep(t *testing.T) {
	s := newTestScheme(t)
	mdb := mtlsMdb("x509", 3)
	mdb.Status.MTLSPhase = "sendKeyFile" // 현재 단계
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdb, readySts()).WithStatusSubresource(mdb).Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	if err := r.reconcileMTLSPhase(context.Background(), mdb); err != nil {
		t.Fatalf("err=%v", err)
	}
	if mdb.Status.MTLSPhase != "sendX509" {
		t.Errorf("rollout ready 시 한 단계 전진(sendKeyFile→sendX509) 기대, got %q", mdb.Status.MTLSPhase)
	}
}

func TestReconcileMTLSPhase_RolloutNotReadyHolds(t *testing.T) {
	s := newTestScheme(t)
	mdb := mtlsMdb("x509", 3)
	mdb.Status.MTLSPhase = "sendKeyFile"
	notReady := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: 1}, // 3 중 1 → not ready
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdb, notReady).WithStatusSubresource(mdb).Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	if err := r.reconcileMTLSPhase(context.Background(), mdb); err != nil {
		t.Fatalf("err=%v", err)
	}
	if mdb.Status.MTLSPhase != "sendKeyFile" {
		t.Errorf("rollout 미완 시 현 단계 유지(점프 금지) 기대, got %q", mdb.Status.MTLSPhase)
	}
}
