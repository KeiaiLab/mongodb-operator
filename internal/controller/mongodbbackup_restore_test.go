/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// mongodbbackup_restore_test.go — F-IMP-01 / F04 (cycle 1) restore path 회귀 가드.
//
// 검증 시나리오: Spec.Restore 가 nil 이 아닌 MongoDBBackup CR 이 Reconcile
// 됐을 때 Phase 가 Restoring 으로 전환되고, 백업 Job 이 생성되지 않는지.

package controller

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// fakeOplogStore 는 OplogSegmentStore 의 테스트 대역 (network 0).
type fakeOplogStore struct {
	oplogEnd    *metav1.Time
	oplogEndErr error
}

func (f *fakeOplogStore) ListSegments(_ context.Context, _ *mongodbv1alpha1.S3StorageSpec, _, _ string) ([]OplogSegment, error) {
	return nil, nil
}
func (f *fakeOplogStore) DeleteSegments(_ context.Context, _ *mongodbv1alpha1.S3StorageSpec, _ string, _ []string) error {
	return nil
}
func (f *fakeOplogStore) ReadBaseOplogEnd(_ context.Context, _ *mongodbv1alpha1.S3StorageSpec, _, _ string) (*metav1.Time, error) {
	return f.oplogEnd, f.oplogEndErr
}

// TestBackupPath_JobComplete_PopulatesOplogStart — S3 백업 Job 이 완료되면
// updateBackupStatus 가 base.meta.json 의 oplogEnd 를 status.OplogStart 로
// 끌어와야 한다. 회귀 가드: 배선 전에는 OplogStart 가 영구 nil 이라 PITR
// window 가 비어 restore 가 base 시점만 허용됐다.
func TestBackupPath_JobComplete_PopulatesOplogStart(t *testing.T) {
	s := newTestScheme(t)
	backup := &mongodbv1alpha1.MongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBBackupSpec{
			ClusterRef: mongodbv1alpha1.ClusterReference{Kind: "MongoDB", Name: "c1"},
			Storage: mongodbv1alpha1.BackupStorageSpec{
				Type: "s3",
				S3:   &mongodbv1alpha1.S3StorageSpec{Bucket: "bk", Prefix: "pitr/", CredentialsRef: corev1.LocalObjectReference{Name: "cred"}},
			},
		},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Now(),
		}}},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(backup, job).
		WithStatusSubresource(&mongodbv1alpha1.MongoDBBackup{}).Build()
	want := metav1.NewTime(metav1.Unix(1700000123, 0).Time)
	r := &MongoDBBackupReconciler{Client: cl, Scheme: s, Store: &fakeOplogStore{oplogEnd: &want}}

	if err := r.updateBackupStatus(context.Background(), backup, "b1"); err != nil {
		t.Fatalf("updateBackupStatus: %v", err)
	}
	got := &mongodbv1alpha1.MongoDBBackup{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "b1", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != backupPhaseCompleted {
		t.Fatalf("Phase 기대 Completed, got %q", got.Status.Phase)
	}
	if got.Status.OplogStart == nil {
		t.Fatal("OplogStart 미기록 — base.meta.json 배선 회귀")
	}
	if got.Status.OplogStart.Unix() != 1700000123 {
		t.Errorf("OplogStart Unix= %d, want 1700000123", got.Status.OplogStart.Unix())
	}
}

// TestBackupPath_NoStore_SkipsOplogStart — Store 미주입이면 OplogStart 를
// 못 채우지만 백업 완료 전이 자체는 정상이어야 한다 (nil 안전 degrade).
func TestBackupPath_NoStore_SkipsOplogStart(t *testing.T) {
	s := newTestScheme(t)
	backup := &mongodbv1alpha1.MongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "b2", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBBackupSpec{
			ClusterRef: mongodbv1alpha1.ClusterReference{Kind: "MongoDB", Name: "c1"},
			Storage: mongodbv1alpha1.BackupStorageSpec{
				Type: "s3",
				S3:   &mongodbv1alpha1.S3StorageSpec{Bucket: "bk", Prefix: "pitr/", CredentialsRef: corev1.LocalObjectReference{Name: "cred"}},
			},
		},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "b2", Namespace: "ns"},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Now(),
		}}},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(backup, job).
		WithStatusSubresource(&mongodbv1alpha1.MongoDBBackup{}).Build()
	r := &MongoDBBackupReconciler{Client: cl, Scheme: s} // Store nil

	if err := r.updateBackupStatus(context.Background(), backup, "b2"); err != nil {
		t.Fatalf("updateBackupStatus: %v", err)
	}
	got := &mongodbv1alpha1.MongoDBBackup{}
	_ = cl.Get(context.Background(), types.NamespacedName{Name: "b2", Namespace: "ns"}, got)
	if got.Status.Phase != backupPhaseCompleted {
		t.Fatalf("Store nil 이어도 Completed 전이는 돼야: got %q", got.Status.Phase)
	}
	if got.Status.OplogStart != nil {
		t.Errorf("Store nil 이면 OplogStart 는 nil 이어야: %v", got.Status.OplogStart)
	}
}

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

// newRestoreEnv 는 restore path 가 Job 생성까지 도달하는 데 필요한 주변 객체
// (대상 MongoDB CR + admin credential secret) 를 갖춘 reconciler 를 만든다.
// 이게 없으면 getClusterConnectionString 이 실패해 Job 이 생성되지 않는다.
func newRestoreEnv(t *testing.T, backup *mongodbv1alpha1.MongoDBBackup) (*MongoDBBackupReconciler, client.Client) {
	t.Helper()
	s := newTestScheme(t)
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "target-cluster", Namespace: backup.Namespace},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Auth: mongodbv1alpha1.AuthSpec{
				AdminCredentialsSecretRef: corev1.LocalObjectReference{Name: "admin-creds"},
			},
		},
	}
	creds := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "admin-creds", Namespace: backup.Namespace},
		Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("pw")},
	}
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(backup, mdb, creds).
		WithStatusSubresource(&mongodbv1alpha1.MongoDBBackup{}).
		Build()
	return &MongoDBBackupReconciler{Client: cl, Scheme: s}, cl
}

// completeRestoreJob 은 restore Job 에 종단 condition 을 주입한다 (실제
// kube-controller-manager 가 하는 일의 테스트 대역).
func completeRestoreJob(t *testing.T, cl client.Client, name, ns string, condType batchv1.JobConditionType, msg string) {
	t.Helper()
	job := &batchv1.Job{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: ns}, job); err != nil {
		t.Fatalf("restore job 이 생성돼 있어야 함: %v", err)
	}
	job.Status.Conditions = []batchv1.JobCondition{{
		Type:               condType,
		Status:             corev1.ConditionTrue,
		Message:            msg,
		LastTransitionTime: metav1.Now(),
	}}
	if err := cl.Status().Update(context.Background(), job); err != nil {
		t.Fatalf("job status update: %v", err)
	}
}

// driveToRestoring 은 finalizer add → restore branch 진입 (Job 생성) 까지 굴린다.
func driveToRestoring(t *testing.T, r *MongoDBBackupReconciler, req ctrl.Request) {
	t.Helper()
	for i := range 2 {
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatalf("%d차 reconcile error: %v", i+1, err)
		}
	}
}

// TestRestorePath_JobComplete_TransitionsToCompleted — restore Job 이 성공하면
// CR 이 Completed 로 전이해야 한다. 회귀 가드: 전이 배선 전에는 Restoring 에서
// 영영 멈춰 복원 성공을 관측할 수 없었다.
func TestRestorePath_JobComplete_TransitionsToCompleted(t *testing.T) {
	t.Parallel()
	backup := newRestoreBackup("restore-done", "ns", "src-backup")
	r, cl := newRestoreEnv(t, backup)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "restore-done", Namespace: "ns"}}
	ctx := context.Background()

	driveToRestoring(t, r, req)
	completeRestoreJob(t, cl, "restore-done-restore", "ns", batchv1.JobComplete, "")

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("완료 감지 reconcile error: %v", err)
	}

	got := &mongodbv1alpha1.MongoDBBackup{}
	if err := cl.Get(ctx, req.NamespacedName, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != backupPhaseCompleted {
		t.Fatalf("Phase 기대 %q, got %q", backupPhaseCompleted, got.Status.Phase)
	}
	if got.Status.CompletionTime == nil {
		t.Error("CompletionTime 기대 non-nil")
	}
}

// TestRestorePath_JobFailed_TransitionsToFailed — restore Job 이 실패하면
// CR 이 Failed + Error 메시지로 전이해야 한다.
func TestRestorePath_JobFailed_TransitionsToFailed(t *testing.T) {
	t.Parallel()
	backup := newRestoreBackup("restore-fail", "ns", "src-backup")
	r, cl := newRestoreEnv(t, backup)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "restore-fail", Namespace: "ns"}}
	ctx := context.Background()

	driveToRestoring(t, r, req)
	completeRestoreJob(t, cl, "restore-fail-restore", "ns", batchv1.JobFailed, "oplog segment chain 부재")

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("실패 감지 reconcile error: %v", err)
	}

	got := &mongodbv1alpha1.MongoDBBackup{}
	if err := cl.Get(ctx, req.NamespacedName, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != backupPhaseFailed {
		t.Fatalf("Phase 기대 %q, got %q", backupPhaseFailed, got.Status.Phase)
	}
	if got.Status.Error != "oplog segment chain 부재" {
		t.Errorf("Error 에 Job condition message 기대, got %q", got.Status.Error)
	}
}

// TestRestorePath_JobRunning_StaysRestoring — Job 이 종단 condition 없이
// 진행 중이면 Restoring 을 유지하고 재큐해야 한다 (조기 완료 선언 금지).
func TestRestorePath_JobRunning_StaysRestoring(t *testing.T) {
	t.Parallel()
	backup := newRestoreBackup("restore-running", "ns", "src-backup")
	r, cl := newRestoreEnv(t, backup)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "restore-running", Namespace: "ns"}}
	ctx := context.Background()

	driveToRestoring(t, r, req)

	res, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Error("진행 중 restore 는 재큐돼야 함")
	}

	got := &mongodbv1alpha1.MongoDBBackup{}
	if err := cl.Get(ctx, req.NamespacedName, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != backupPhaseRestoring {
		t.Fatalf("Phase 기대 %q, got %q", backupPhaseRestoring, got.Status.Phase)
	}
}

// TestRestorePath_CompletedIsIdempotent — 종단 도달 후 재reconcile 은 phase 를
// 뒤집지 않아야 한다 (Job GC 로 TTL 만료 후에도 Completed 유지).
func TestRestorePath_CompletedIsIdempotent(t *testing.T) {
	t.Parallel()
	backup := newRestoreBackup("restore-idem", "ns", "src-backup")
	r, cl := newRestoreEnv(t, backup)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "restore-idem", Namespace: "ns"}}
	ctx := context.Background()

	driveToRestoring(t, r, req)
	completeRestoreJob(t, cl, "restore-idem-restore", "ns", batchv1.JobComplete, "")
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("완료 감지 reconcile error: %v", err)
	}

	// Job 이 TTL 로 GC 된 뒤 재reconcile 되는 상황.
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "restore-idem-restore", Namespace: "ns"}}
	if err := cl.Delete(ctx, job); err != nil {
		t.Fatalf("job delete: %v", err)
	}
	res, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("종단 후 reconcile 은 error 없이 no-op 이어야 함: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Error("종단 도달 후에는 재큐하지 않아야 함")
	}

	got := &mongodbv1alpha1.MongoDBBackup{}
	if err := cl.Get(ctx, req.NamespacedName, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != backupPhaseCompleted {
		t.Fatalf("Phase 는 %q 유지돼야 함, got %q", backupPhaseCompleted, got.Status.Phase)
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
