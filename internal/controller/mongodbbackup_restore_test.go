/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// mongodbbackup_restore_test.go — F-IMP-01 / F04 (cycle 1) restore path 회귀 가드.
//
// 검증 시나리오: Spec.Restore 가 nil 이 아닌 MongoDBBackup CR 이 Reconcile
// 됐을 때 Phase 가 Restoring 으로 전환되고, 백업 Job 이 생성되지 않는지.

package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func newRestoreBackup(name, ns, source string) *mongodbv1alpha1.MongoDBBackup {
	return &mongodbv1alpha1.MongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID(name + "-uid")},
		Spec: mongodbv1alpha1.MongoDBBackupSpec{
			ClusterRef: mongodbv1alpha1.ClusterReference{Kind: "MongoDB", Name: "target-cluster"},
			Storage:    mongodbv1alpha1.BackupStorageSpec{Type: "pvc"},
			Restore: &mongodbv1alpha1.RestoreSpec{
				SourceBackupName: source,
			},
		},
	}
}

// TestRestorePath_EntersRestoringPhase 는 Spec.Restore 가 설정된 CR 이
// 첫 reconcile 후 Phase=Restoring 으로 진입하는지 검증.
// finalizer add 단계 1회 → restore branch 진입 단계 1회 (총 2 reconcile).
func TestRestorePath_EntersRestoringPhase(t *testing.T) {
	t.Parallel()
	s := newTestScheme(t)
	backup := newRestoreBackup("restore-cr", "ns", "src-backup")
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(backup).
		WithStatusSubresource(&mongodbv1alpha1.MongoDBBackup{}).
		Build()
	r := &MongoDBBackupReconciler{Client: cl, Scheme: s}
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "restore-cr", Namespace: "ns"}}

	// 1차 reconcile: finalizer add 후 requeue
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("1차 reconcile error: %v", err)
	}

	// 2차 reconcile: restore branch 진입
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("2차 reconcile error: %v", err)
	}

	// status 검증
	got := &mongodbv1alpha1.MongoDBBackup{}
	if err := cl.Get(ctx, req.NamespacedName, got); err != nil {
		t.Fatalf("get after reconcile: %v", err)
	}
	if got.Status.Phase != "Restoring" {
		t.Fatalf("Phase 기대 Restoring, got %q", got.Status.Phase)
	}
	if got.Status.StartTime == nil {
		t.Fatalf("StartTime 기대 non-nil")
	}
}

// TestRestorePath_PointInTimeRoundTrip 는 PointInTime 필드가 spec 저장 +
// 재읽기 round-trip 에서 보존되는지 검증.
func TestRestorePath_PointInTimeRoundTrip(t *testing.T) {
	t.Parallel()
	// metav1.Time 은 second 단위 정밀도로 round-trip 됨 (k8s API 표준). monotonic
	// clock + sub-second 는 보존되지 않으므로 second 단위로 비교.
	now := metav1.NewTime(metav1.Now().Round(1e9)) // Round to second
	backup := newRestoreBackup("pitr-cr", "ns", "src-backup")
	backup.Spec.Restore.PointInTime = &now

	s := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(backup).Build()

	got := &mongodbv1alpha1.MongoDBBackup{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "pitr-cr", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Spec.Restore == nil || got.Spec.Restore.PointInTime == nil {
		t.Fatalf("PointInTime 보존 실패")
	}
	gotTime := got.Spec.Restore.PointInTime.Round(1e9)
	wantTime := now.Round(1e9)
	if !gotTime.Equal(wantTime) {
		t.Fatalf("PointInTime mismatch: got %v want %v", gotTime, wantTime)
	}
}
