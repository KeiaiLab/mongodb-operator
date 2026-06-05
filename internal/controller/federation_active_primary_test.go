/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// federation_active_primary_test.go — Phase 5.2 reconcile 가 Status.ActivePrimary 를
// DecideFederationPrimary 결과로 갱신하는지 fake-client 검증.
// kubeconfigRef 가 빈 region 은 in-cluster (propagateRegionMongoDB synced=true) 라
// remote I/O 없이 reconcile 전체 경로 테스트 가능.

package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestFederationReconcile_SetsActivePrimary(t *testing.T) {
	s := newTestScheme(t)
	fed := &mongodbv1alpha1.MongoDBFederation{
		ObjectMeta: metav1.ObjectMeta{Name: "fed", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBFederationSpec{
			Regions: []mongodbv1alpha1.FederationRegion{
				{Name: "west", Priority: "1.0"}, // kubeconfigRef 비움 → in-cluster
				{Name: "east", Priority: "5.0"},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(fed).WithStatusSubresource(fed).Build()
	r := &MongoDBFederationReconciler{Client: cl, Scheme: s}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "fed", Namespace: "ns"},
	}); err != nil {
		t.Fatalf("reconcile err: %v", err)
	}

	got := &mongodbv1alpha1.MongoDBFederation{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "fed", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.ActivePrimary != "east" {
		t.Errorf("ActivePrimary = %q, want east (priority 5.0 최고, 양 region in-cluster Synced)", got.Status.ActivePrimary)
	}
}

func TestFederationReconcile_InvalidSpecRejected(t *testing.T) {
	s := newTestScheme(t)
	fed := &mongodbv1alpha1.MongoDBFederation{
		ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBFederationSpec{
			Regions: []mongodbv1alpha1.FederationRegion{{Name: "solo", Priority: "1.0"}}, // 1개 → 무효
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(fed).WithStatusSubresource(fed).Build()
	r := &MongoDBFederationReconciler{Client: cl, Scheme: s}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "bad", Namespace: "ns"},
	}); err != nil {
		t.Fatalf("reconcile err: %v", err)
	}
	got := &mongodbv1alpha1.MongoDBFederation{}
	_ = cl.Get(context.Background(), types.NamespacedName{Name: "bad", Namespace: "ns"}, got)
	if got.Status.Phase != federationPhaseFailed {
		t.Errorf("무효 spec(region 1개) → Failed phase 기대, got %q", got.Status.Phase)
	}
}
