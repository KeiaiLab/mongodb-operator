/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

// 본 파일은 reconcile 핵심 경로 중 **fake client 로 결정론 재현이 가능한** 헬퍼를
// 회귀 가드한다. 이 함수들은 전부 커버리지 0% 였는데, 실패 시 증상이 조용하다는
// 공통점이 있다 — admin 시크릿을 못 읽으면 인증 매니저가 빈 비밀번호로 접속을
// 시도하고, 백업 status 전이가 어긋나면 실패한 백업이 성공으로 보인다.
// envtest 불필요 (fake client 만 사용).
package controller

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func newMongoDBWithAdminSecret(name, ns, secretName string) *mongodbv1alpha1.MongoDB {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID(name + "-uid")},
		Spec:       mongodbv1alpha1.MongoDBSpec{Members: 3},
	}
	mdb.Spec.Auth.AdminCredentialsSecretRef = corev1.LocalObjectReference{Name: secretName}
	return mdb
}

// TestGetAdminPassword_시크릿이_없으면_에러를_반환한다 — 시크릿 부재를 조용히
// 빈 문자열로 넘기면 이후 인증이 실패해도 원인이 드러나지 않는다.
func TestGetAdminPassword_시크릿이_없으면_에러를_반환한다(t *testing.T) {
	s := newTestScheme(t)
	mdb := newMongoDBWithAdminSecret("rs", "ns", "missing-secret")
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdb).Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	pw, err := r.getAdminPassword(context.Background(), mdb)
	if err == nil {
		t.Fatalf("시크릿이 없는데 에러가 없다 (pw=%q)", pw)
	}
	if pw != "" {
		t.Fatalf("에러 시 비밀번호는 비어 있어야 한다, got %q", pw)
	}
	if !strings.Contains(err.Error(), "admin credentials secret") {
		t.Fatalf("에러 메시지가 원인을 지목하지 않는다: %v", err)
	}
}

// TestGetAdminPassword_password_키가_없으면_에러를_반환한다 — 시크릿은 있는데
// 키 이름이 틀린 경우가 실제로 가장 흔한 오배선이다.
func TestGetAdminPassword_password_키가_없으면_에러를_반환한다(t *testing.T) {
	s := newTestScheme(t)
	mdb := newMongoDBWithAdminSecret("rs", "ns", "admin")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "admin", Namespace: "ns"},
		Data:       map[string][]byte{"username": []byte("admin")}, // password 누락
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdb, secret).Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	if _, err := r.getAdminPassword(context.Background(), mdb); err == nil {
		t.Fatal("password 키가 없는데 에러가 없다")
	} else if !strings.Contains(err.Error(), "password key not found") {
		t.Fatalf("에러 메시지가 원인을 지목하지 않는다: %v", err)
	}
}

func TestGetAdminPassword_정상_시크릿에서_값을_읽는다(t *testing.T) {
	s := newTestScheme(t)
	mdb := newMongoDBWithAdminSecret("rs", "ns", "admin")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "admin", Namespace: "ns"},
		Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("s3cr3t")},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdb, secret).Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	pw, err := r.getAdminPassword(context.Background(), mdb)
	if err != nil {
		t.Fatalf("정상 경로에서 에러: %v", err)
	}
	if pw != "s3cr3t" {
		t.Fatalf("비밀번호 불일치: got %q, want %q", pw, "s3cr3t")
	}
}

func newBackup(name, ns string) *mongodbv1alpha1.MongoDBBackup {
	return &mongodbv1alpha1.MongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID(name + "-uid")},
	}
}

// TestCreateOrUpdateSecret_없으면_생성하고_있으면_갱신한다 — 이 헬퍼는 Get 이
// NotFound 일 때만 Create 로 분기한다. 분기를 잘못 타면 매 reconcile 마다
// AlreadyExists 로 실패하거나(생성만), 최초 생성이 안 된다(갱신만).
func TestCreateOrUpdateSecret_없으면_생성하고_있으면_갱신한다(t *testing.T) {
	s := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &MongoDBBackupReconciler{Client: cl, Scheme: s}
	ctx := context.Background()

	want := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "backup-creds", Namespace: "ns"},
		StringData: map[string]string{"key": "v1"},
	}
	if err := r.createOrUpdateSecret(ctx, want); err != nil {
		t.Fatalf("최초 생성 실패: %v", err)
	}

	got := &corev1.Secret{}
	if err := cl.Get(ctx, types.NamespacedName{Name: "backup-creds", Namespace: "ns"}, got); err != nil {
		t.Fatalf("생성 후 조회 실패: %v", err)
	}

	// 두 번째 호출은 Create 가 아니라 Update 경로여야 한다 (AlreadyExists 로 죽지 않음).
	updated := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "backup-creds", Namespace: "ns"},
		StringData: map[string]string{"key": "v2"},
	}
	if err := r.createOrUpdateSecret(ctx, updated); err != nil {
		t.Fatalf("갱신 실패: %v", err)
	}

	got = &corev1.Secret{}
	if err := cl.Get(ctx, types.NamespacedName{Name: "backup-creds", Namespace: "ns"}, got); err != nil {
		t.Fatalf("갱신 후 조회 실패: %v", err)
	}
	if got.StringData["key"] != "v2" {
		t.Fatalf("갱신된 값이 반영되지 않았다: got %q, want %q", got.StringData["key"], "v2")
	}
}

// TestUpdateBackupStatus_Job_완료를_Completed_로_전이한다 — 이 전이가 깨지면
// 완료된 백업이 영원히 Running 으로 남아 다음 스케줄을 막는다.
func TestUpdateBackupStatus_Job_완료를_Completed_로_전이한다(t *testing.T) {
	s := newTestScheme(t)
	backup := newBackup("b1", "ns")
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "b1-job", Namespace: "ns"},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type:               batchv1.JobComplete,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		}}},
	}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(backup, job).WithStatusSubresource(backup).Build()
	r := &MongoDBBackupReconciler{Client: cl, Scheme: s}

	if err := r.updateBackupStatus(context.Background(), backup, "b1-job"); err != nil {
		t.Fatalf("status 갱신 실패: %v", err)
	}
	if backup.Status.Phase != backupPhaseCompleted {
		t.Fatalf("phase 불일치: got %q, want %q", backup.Status.Phase, backupPhaseCompleted)
	}
	if backup.Status.CompletionTime == nil {
		t.Fatal("완료 시각이 기록되지 않았다")
	}
}

// TestUpdateBackupStatus_Job_실패를_Failed_로_전이하고_사유를_남긴다 — 실패한
// 백업이 성공으로 보이는 것은 이 프로젝트에서 가장 비싼 오류다.
func TestUpdateBackupStatus_Job_실패를_Failed_로_전이하고_사유를_남긴다(t *testing.T) {
	s := newTestScheme(t)
	backup := newBackup("b2", "ns")
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "b2-job", Namespace: "ns"},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type:               batchv1.JobFailed,
			Status:             corev1.ConditionTrue,
			Message:            "BackoffLimitExceeded",
			LastTransitionTime: metav1.Now(),
		}}},
	}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(backup, job).WithStatusSubresource(backup).Build()
	r := &MongoDBBackupReconciler{Client: cl, Scheme: s}

	if err := r.updateBackupStatus(context.Background(), backup, "b2-job"); err != nil {
		t.Fatalf("status 갱신 실패: %v", err)
	}
	if backup.Status.Phase != backupPhaseFailed {
		t.Fatalf("phase 불일치: got %q, want %q", backup.Status.Phase, backupPhaseFailed)
	}
	if backup.Status.Error != "BackoffLimitExceeded" {
		t.Fatalf("실패 사유가 보존되지 않았다: got %q", backup.Status.Error)
	}
}

// TestUpdateBackupStatus_Job_이_없으면_에러를_반환한다 — 조용히 넘기면 status 가
// 낡은 값으로 고정된다.
func TestUpdateBackupStatus_Job_이_없으면_에러를_반환한다(t *testing.T) {
	s := newTestScheme(t)
	backup := newBackup("b3", "ns")
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(backup).WithStatusSubresource(backup).Build()
	r := &MongoDBBackupReconciler{Client: cl, Scheme: s}

	if err := r.updateBackupStatus(context.Background(), backup, "nonexistent-job"); err == nil {
		t.Fatal("Job 이 없는데 에러가 없다")
	}
}
