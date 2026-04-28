/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// 본 파일은 Track A의 K8s Lease 분산락 동작을 fake client로 회귀 가드한다.
// 진짜 K8s 또는 envtest 없이도 acquire/release/busy/takeover 시나리오를 검증.
package controller

import (
	"context"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func newTestMongoDBForLease(name, ns string) *mongodbv1alpha1.MongoDB {
	return &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID(name + "-uid")},
		Spec:       mongodbv1alpha1.MongoDBSpec{Members: 3},
	}
}

// TestAcquireBootstrapLease_FreshCreatesAndReleases는 lease가 없는 깨끗한
// 상태에서 acquire가 신규 lease를 생성하고 release가 그것을 삭제하는지 검증.
func TestAcquireBootstrapLease_FreshCreatesAndReleases(t *testing.T) {
	s := newTestScheme(t)
	mdb := newTestMongoDBForLease("rs", "ns")
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdb).Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}
	ctx := context.Background()

	lease, ok, err := r.acquireBootstrapLease(ctx, mdb)
	if err != nil {
		t.Fatalf("acquire fresh: %v", err)
	}
	if !ok || lease == nil {
		t.Fatalf("기대 ok=true + lease non-nil, got ok=%v lease=%v", ok, lease)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != bootstrapLeaseHolder() {
		t.Fatalf("holder identity 기대 %q, got %v", bootstrapLeaseHolder(), lease.Spec.HolderIdentity)
	}

	r.releaseBootstrapLease(ctx, lease)

	got := &coordinationv1.Lease{}
	err = cl.Get(ctx, types.NamespacedName{Name: bootstrapLeaseName(mdb), Namespace: mdb.Namespace}, got)
	if err == nil {
		t.Fatalf("release 후에도 lease 존재: %+v", got)
	}
}

// TestAcquireBootstrapLease_BusyWhenOtherHolderValid는 다른 holder가 valid한
// lease를 보유 중일 때 acquire가 (nil, false, nil)로 양보하는지 검증.
func TestAcquireBootstrapLease_BusyWhenOtherHolderValid(t *testing.T) {
	s := newTestScheme(t)
	mdb := newTestMongoDBForLease("rs", "ns")

	// 다른 holder의 valid lease를 미리 생성.
	other := "other-pod-1234"
	durationSec := int32(30)
	now := metav1.NewMicroTime(time.Now())
	existing := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: bootstrapLeaseName(mdb), Namespace: mdb.Namespace},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &other,
			LeaseDurationSeconds: &durationSec,
			AcquireTime:          &now,
			RenewTime:            &now,
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdb, existing).Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	lease, ok, err := r.acquireBootstrapLease(context.Background(), mdb)
	if err != nil {
		t.Fatalf("expected nil error on busy, got %v", err)
	}
	if ok || lease != nil {
		t.Fatalf("기대 busy(ok=false, lease=nil), got ok=%v lease=%v", ok, lease)
	}
}

// TestAcquireBootstrapLease_TakeoverExpired는 기존 lease가 만료됐을 때 acquire
// 가 holder를 자기 자신으로 갱신해 takeover하는지 검증.
func TestAcquireBootstrapLease_TakeoverExpired(t *testing.T) {
	s := newTestScheme(t)
	mdb := newTestMongoDBForLease("rs", "ns")

	// 60초 전 RenewTime + 30초 LeaseDurationSeconds = 30초 전 만료.
	other := "ghost-holder"
	durationSec := int32(30)
	expired := metav1.NewMicroTime(time.Now().Add(-60 * time.Second))
	existing := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: bootstrapLeaseName(mdb), Namespace: mdb.Namespace},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &other,
			LeaseDurationSeconds: &durationSec,
			AcquireTime:          &expired,
			RenewTime:            &expired,
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdb, existing).Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	lease, ok, err := r.acquireBootstrapLease(context.Background(), mdb)
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if !ok || lease == nil {
		t.Fatalf("기대 takeover 성공(ok=true), got ok=%v lease=%v", ok, lease)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != bootstrapLeaseHolder() {
		t.Fatalf("expired lease takeover 후 holder identity 갱신 안 됨: got %v", lease.Spec.HolderIdentity)
	}
}

// TestReleaseBootstrapLease_HandlesNilAndNotFound은 release가 nil lease 또는
// 이미 사라진 lease에 대해 panic/error 없이 동작하는지 검증.
func TestReleaseBootstrapLease_HandlesNilAndNotFound(t *testing.T) {
	s := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	// nil lease — no-op.
	r.releaseBootstrapLease(context.Background(), nil)

	// 이미 사라진 lease — 내부에서 IsNotFound 무시되어야 함.
	ghost := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "ghost", Namespace: "ns"},
	}
	r.releaseBootstrapLease(context.Background(), ghost)
}
