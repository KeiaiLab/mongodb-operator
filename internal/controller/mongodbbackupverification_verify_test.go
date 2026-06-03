/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// 본 파일은 envtest/실제 mongo 없이 fake client + mock docCounter 로 backup
// verification 의 "Verifying" 단계 실 검증 로직을 회귀 가드한다. 이전 STUB
// (allPassed 항상 true + DocCount=MinExpectedDocs echo)에서 실제 CountDocuments
// 기반 판정으로 전환됐음을 증명한다.
package controller

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// fakeDocCounter 는 db.coll → 반환 문서 수(또는 에러)를 미리 지정하는 mock.
type fakeDocCounter struct {
	counts map[string]int64
	errs   map[string]error
	closed bool
}

func (f *fakeDocCounter) Count(_ context.Context, db, coll, _ string) (int64, error) {
	key := db + "." + coll
	if err, ok := f.errs[key]; ok {
		return 0, err
	}
	return f.counts[key], nil
}

func (f *fakeDocCounter) Close(_ context.Context) { f.closed = true }

// newVerifyingReconciler 는 Verifying phase 의 verification CR + backup-uri
// secret 을 seed 한 reconciler + fake client 를 만든다.
func newVerifyingReconciler(t *testing.T, queries []mongodbv1alpha1.VerificationQuery, counter docCounter) (*MongoDBBackupVerificationReconciler, *mongodbv1alpha1.MongoDBBackupVerification) {
	t.Helper()
	s := newTestScheme(t)

	verify := &mongodbv1alpha1.MongoDBBackupVerification{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-1", Namespace: "default"},
		Spec: mongodbv1alpha1.MongoDBBackupVerificationSpec{
			BackupRef:     corev1.LocalObjectReference{Name: "backup-1"},
			SampleQueries: queries,
		},
		Status: mongodbv1alpha1.MongoDBBackupVerificationStatus{Phase: "Verifying"},
	}
	// restore 대상 connectionString secret (readRestoreConnString 가 읽음).
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "backup-1-backup-uri", Namespace: "default"},
		Data:       map[string][]byte{"connectionString": []byte("mongodb://restore-target:27017")},
	}

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(verify, secret).
		WithStatusSubresource(verify).
		Build()

	r := &MongoDBBackupVerificationReconciler{
		Client: cl,
		Scheme: s,
		NewDocCounter: func(_ context.Context, _ string) (docCounter, error) {
			return counter, nil
		},
	}
	return r, verify
}

func reconcileOnce(t *testing.T, r *MongoDBBackupVerificationReconciler, v *mongodbv1alpha1.MongoDBBackupVerification) *mongodbv1alpha1.MongoDBBackupVerification {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: v.Name, Namespace: v.Namespace},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got := &mongodbv1alpha1.MongoDBBackupVerification{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, got); err != nil {
		t.Fatalf("Get after reconcile: %v", err)
	}
	return got
}

// 모든 쿼리가 MinExpectedDocs 이상 → Passed.
func TestVerifying_AllQueriesMeetThreshold_Passed(t *testing.T) {
	queries := []mongodbv1alpha1.VerificationQuery{
		{DB: "shop", Collection: "users", MinExpectedDocs: 10},
		{DB: "shop", Collection: "orders", MinExpectedDocs: 5},
	}
	counter := &fakeDocCounter{counts: map[string]int64{"shop.users": 42, "shop.orders": 5}}
	r, v := newVerifyingReconciler(t, queries, counter)

	got := reconcileOnce(t, r, v)

	if got.Status.Phase != "Passed" {
		t.Errorf("phase = %q, want Passed", got.Status.Phase)
	}
	if len(got.Status.QueryResults) != 2 {
		t.Fatalf("results = %d, want 2", len(got.Status.QueryResults))
	}
	// 실제 count 가 기록돼야 함(STUB 의 MinExpectedDocs echo 가 아님).
	if got.Status.QueryResults[0].DocCount != 42 {
		t.Errorf("users DocCount = %d, want 42 (실 count)", got.Status.QueryResults[0].DocCount)
	}
	for _, res := range got.Status.QueryResults {
		if !res.Passed {
			t.Errorf("%s.%s should pass", res.DB, res.Collection)
		}
	}
	if !counter.closed {
		t.Error("counter.Close 가 호출되지 않음")
	}
}

// 한 쿼리가 임계 미달 → Failed (STUB 였다면 항상 Passed 였음 — 회귀 가드 핵심).
func TestVerifying_OneQueryBelowThreshold_Failed(t *testing.T) {
	queries := []mongodbv1alpha1.VerificationQuery{
		{DB: "shop", Collection: "users", MinExpectedDocs: 10},
		{DB: "shop", Collection: "orders", MinExpectedDocs: 100},
	}
	counter := &fakeDocCounter{counts: map[string]int64{"shop.users": 42, "shop.orders": 3}}
	r, v := newVerifyingReconciler(t, queries, counter)

	got := reconcileOnce(t, r, v)

	if got.Status.Phase != backupPhaseFailed {
		t.Errorf("phase = %q, want %q", got.Status.Phase, backupPhaseFailed)
	}
	var ordersResult *mongodbv1alpha1.VerificationQueryResult
	for i := range got.Status.QueryResults {
		if got.Status.QueryResults[i].Collection == "orders" {
			ordersResult = &got.Status.QueryResults[i]
		}
	}
	if ordersResult == nil || ordersResult.Passed {
		t.Errorf("orders 쿼리는 실패해야 함(count 3 < 100): %+v", ordersResult)
	}
}

// 쿼리 실행 자체가 에러 → 해당 쿼리 실패 + 전체 Failed.
func TestVerifying_QueryError_Failed(t *testing.T) {
	queries := []mongodbv1alpha1.VerificationQuery{
		{DB: "shop", Collection: "users", MinExpectedDocs: 1},
	}
	counter := &fakeDocCounter{errs: map[string]error{"shop.users": fmt.Errorf("connection reset")}}
	r, v := newVerifyingReconciler(t, queries, counter)

	got := reconcileOnce(t, r, v)

	if got.Status.Phase != backupPhaseFailed {
		t.Errorf("phase = %q, want %q", got.Status.Phase, backupPhaseFailed)
	}
}

// SampleQueries 미지정 → 기본 sanity 검증(admin.system.version).
func TestVerifying_NoQueries_DefaultSanityCheck(t *testing.T) {
	counter := &fakeDocCounter{counts: map[string]int64{"admin.system.version": 1}}
	r, v := newVerifyingReconciler(t, nil, counter)

	got := reconcileOnce(t, r, v)

	if got.Status.Phase != "Passed" {
		t.Errorf("phase = %q, want Passed", got.Status.Phase)
	}
	if len(got.Status.QueryResults) != 1 || got.Status.QueryResults[0].Collection != "system.version" {
		t.Errorf("기본 sanity 검증(admin.system.version)이 실행돼야 함: %+v", got.Status.QueryResults)
	}
}
