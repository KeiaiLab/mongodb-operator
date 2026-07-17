/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonsfinalizer "github.com/keiailab/keiailab-commons/pkg/finalizer"
	commonsreconcile "github.com/keiailab/keiailab-commons/pkg/reconcile"
	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"
	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

const (
	// mongodbBackupFinalizer — api/v1alpha1.FinalizerMongoDBBackup local alias.
	mongodbBackupFinalizer = mongodbv1alpha1.FinalizerMongoDBBackup

	backupPhaseCompleted = "Completed"
	backupPhaseFailed    = "Failed"
	backupPhasePassed    = "Passed"
	backupPhasePending   = "Pending"
	backupPhaseRunning   = "Running"
	backupPhaseRestoring = "Restoring"
)

// MongoDBBackupReconciler reconciles a MongoDBBackup object
type MongoDBBackupReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Store 는 base.meta.json 을 읽어 status.OplogStart(window/restore 앵커)를
	// 채우기 위한 S3 접근 seam. nil 이면 OplogStart 를 못 채운다 (PITR window 가
	// 비어 restore 가 base 시점만 허용) — OplogUploaderReconciler 와 동일 구현체를
	// cmd/main.go 에서 주입한다. nil 안전(기능 degrade 지 crash 아님).
	Store OplogSegmentStore
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbbackups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbbackups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbbackups/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

func (r *MongoDBBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling MongoDBBackup", "namespace", req.Namespace, "name", req.Name)

	// Fetch MongoDBBackup instance
	backup := &mongodbv1alpha1.MongoDBBackup{}
	if err := r.Get(ctx, req.NamespacedName, backup); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("MongoDBBackup resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get MongoDBBackup")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !backup.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, backup)
	}

	// Add finalizer if needed
	if !commonsfinalizer.Has(backup, mongodbBackupFinalizer) {
		commonsfinalizer.Add(backup, mongodbBackupFinalizer)
		if err := r.Update(ctx, backup); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if backup is already completed or failed
	if backup.Status.Phase == backupPhaseCompleted || backup.Status.Phase == backupPhaseFailed {
		return ctrl.Result{}, nil
	}

	// F-IMP-01 / F04 (cycle 1): Restore path 분기. Spec.Restore 가 nil 이 아니면
	// 본 CR 은 *백업 capture 가 아닌 restore 작업* 으로 해석한다.
	//
	// 생명주기: Pending → Restoring (Job 생성) → Completed | Failed. 백업 path 의
	// updateBackupStatus 와 동일하게 Job condition 을 관찰해 종단 phase 로 전이한다
	// (updateRestoreStatus). 종단 도달 후 재진입은 본 함수 상단의
	// Completed/Failed guard 가 차단 — 멱등.
	if backup.Spec.Restore != nil {
		if backup.Status.Phase == "" || backup.Status.Phase == backupPhasePending {
			applyRestoringStatus := func() {
				backup.Status.Phase = backupPhaseRestoring
				backup.Status.StartTime = &metav1.Time{Time: time.Now()}
			}
			applyRestoringStatus()
			if err := commonsstatus.UpdateWithRetry(ctx, r.Client, backup, applyRestoringStatus); err != nil {
				return ctrl.Result{}, err
			}
		}

		// cycle 15: actual mongorestore Job creation.
		// SourceBackupName references a Completed MongoDBBackup CR's PVC; the
		// restore Job mounts that PVC and runs `mongorestore --uri ... [--oplogReplay --oplogLimit <pit>]`.
		connectionString, err := r.getClusterConnectionString(ctx, backup)
		if err != nil {
			logger.V(1).Info("connection string unavailable yet; will retry", "err", err)
			return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
		}
		restoreJob, err := resources.BuildRestoreJob(backup, connectionString)
		if err != nil {
			return r.updateStatusError(ctx, backup, err)
		}
		if err := r.createOrUpdate(ctx, backup, restoreJob); err != nil {
			return r.updateStatusError(ctx, backup, err)
		}
		logger.Info("Restore Job created/updated",
			"job", restoreJob.Name,
			"sourceBackup", backup.Spec.Restore.SourceBackupName,
			"pointInTime", backup.Spec.Restore.PointInTime)

		// Job 완료 감지 → 종단 phase 전이. Job 이 아직 진행 중이면 Restoring 유지 + 재큐.
		if err := r.updateRestoreStatus(ctx, backup, restoreJob.Name); err != nil {
			return ctrl.Result{}, err
		}
		if backup.Status.Phase == backupPhaseCompleted || backup.Status.Phase == backupPhaseFailed {
			logger.Info("Restore finished", "phase", backup.Status.Phase, "job", restoreJob.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
	}

	// Update status to Running if not set
	if backup.Status.Phase == "" {
		applyPendingStatus := func() {
			backup.Status.Phase = backupPhasePending
			backup.Status.StartTime = &metav1.Time{Time: time.Now()}
		}
		applyPendingStatus()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, backup, applyPendingStatus); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Get cluster connection string
	connectionString, err := r.getClusterConnectionString(ctx, backup)
	if err != nil {
		return r.updateStatusError(ctx, backup, err)
	}

	// Create backup job
	job := resources.BuildBackupJob(backup, connectionString)
	if err := r.createOrUpdate(ctx, backup, job); err != nil {
		return r.updateStatusError(ctx, backup, err)
	}

	// Update status based on job status
	if err := r.updateBackupStatus(ctx, backup, job.Name); err != nil {
		return ctrl.Result{}, err
	}

	// If still running, requeue
	if backup.Status.Phase == backupPhaseRunning || backup.Status.Phase == backupPhasePending {
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
	}

	logger.Info("Successfully reconciled MongoDBBackup")
	return ctrl.Result{}, nil
}

func (r *MongoDBBackupReconciler) handleDeletion(ctx context.Context, backup *mongodbv1alpha1.MongoDBBackup) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling MongoDBBackup deletion")
	// Backup Job은 OwnerReference만으로 GC되지 않으므로 명시 cleanup 필요.
	cleanup := func(ctx context.Context) error {
		job := &batchv1.Job{}
		if err := r.Get(ctx, types.NamespacedName{Name: backup.Name, Namespace: backup.Namespace}, job); err == nil {
			propagation := metav1.DeletePropagationBackground
			if err := r.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil {
				logger.Error(err, "Failed to delete backup job")
				// Job 삭제 실패는 finalizer 유지 사유로는 약하므로 silently 진행 (기존 동작 보존).
			}
		}
		return nil
	}
	return commonsreconcile.HandleFinalizerCleanup(ctx, r.Client, backup, mongodbBackupFinalizer, cleanup)
}

func (r *MongoDBBackupReconciler) getClusterConnectionString(ctx context.Context, backup *mongodbv1alpha1.MongoDBBackup) (string, error) {
	var host string
	var authSecretName string

	switch backup.Spec.ClusterRef.Kind {
	case "MongoDB":
		mdb := &mongodbv1alpha1.MongoDB{}
		if err := r.Get(ctx, types.NamespacedName{Name: backup.Spec.ClusterRef.Name, Namespace: backup.Namespace}, mdb); err != nil {
			return "", fmt.Errorf("failed to get MongoDB cluster: %w", err)
		}
		// Extract host from connection string (remove mongodb:// prefix)
		host = mdb.Name + "." + backup.Namespace + ".svc.cluster.local:27017"
		authSecretName = mdb.Spec.Auth.AdminCredentialsSecretRef.Name

	case "MongoDBSharded":
		mdbsh := &mongodbv1alpha1.MongoDBSharded{}
		if err := r.Get(ctx, types.NamespacedName{Name: backup.Spec.ClusterRef.Name, Namespace: backup.Namespace}, mdbsh); err != nil {
			return "", fmt.Errorf("failed to get MongoDBSharded cluster: %w", err)
		}
		host = mdbsh.Name + "-mongos." + backup.Namespace + ".svc.cluster.local:27017"
		authSecretName = mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name

	default:
		return "", fmt.Errorf("unknown cluster kind: %s", backup.Spec.ClusterRef.Kind)
	}

	// Get admin credentials from secret
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: authSecretName, Namespace: backup.Namespace}, secret); err != nil {
		return "", fmt.Errorf("failed to get auth secret %s: %w", authSecretName, err)
	}

	username := string(secret.Data["username"])
	password := string(secret.Data["password"])

	if username == "" || password == "" {
		return "", fmt.Errorf("auth secret %s missing username or password", authSecretName)
	}

	// Build connection string with authentication
	// Note: Don't include database path (/admin) - only authSource parameter
	// Otherwise mongodump will only backup the specified database
	connStr := fmt.Sprintf("mongodb://%s:%s@%s/?authSource=admin",
		username, password, host)

	secretName := backup.Name + "-backup-uri"
	uriSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: backup.Namespace,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"connectionString": connStr,
		},
	}
	if err := controllerutil.SetControllerReference(backup, uriSecret, r.Scheme); err != nil {
		return "", fmt.Errorf("failed to set owner reference on backup URI secret: %w", err)
	}
	if err := r.createOrUpdateSecret(ctx, uriSecret); err != nil {
		return "", fmt.Errorf("failed to create backup URI secret: %w", err)
	}

	return secretName, nil
}

// createOrUpdate — race-tolerant Job/PVC apply (postgres iteration 35 패턴 차용).
//
// iteration 42: *raw Get + Create + IsAlreadyExists guard* → *controllerutil.
// CreateOrUpdate* 마이그레이션. controller-runtime 이 *AlreadyExists 자동 retry*
// + Update mutate fn 호출 — race-tolerant 기본 보장. mutate fn 은 *no-op*
// (Job 존재 시 update 안 하는 기존 동작 보존).
//
// postgres-operator 의 우월한 추상화 패턴 차용 (HANDOFF iteration 41 §postgres
// 의 우월한 추상화 발견). it41 (a0a0cff) 의 *수동 IsAlreadyExists guard* 보다
// robust + 코드 simpler.
func (r *MongoDBBackupReconciler) createOrUpdate(ctx context.Context, backup *mongodbv1alpha1.MongoDBBackup, obj client.Object) error {
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, obj, func() error {
		// owner reference 만 설정 (Job/PVC spec 자체는 update 안 함 — 기존 동작 보존).
		return controllerutil.SetControllerReference(backup, obj, r.Scheme)
	})
	return err
}

func (r *MongoDBBackupReconciler) createOrUpdateSecret(ctx context.Context, secret *corev1.Secret) error {
	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return r.Create(ctx, secret)
		}
		return err
	}
	existing.StringData = secret.StringData
	return r.Update(ctx, existing)
}

// jobHasCondition 은 Job 이 주어진 condition type 을 True 로 갖는지 검사한다.
func jobHasCondition(job *batchv1.Job, condType batchv1.JobConditionType) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == condType && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func (r *MongoDBBackupReconciler) updateBackupStatus(ctx context.Context, backup *mongodbv1alpha1.MongoDBBackup, jobName string) error {
	job := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: backup.Namespace}, job); err != nil {
		return err
	}

	jobComplete := jobHasCondition(job, batchv1.JobComplete)

	// status.OplogStart 채우기 — base.meta.json{oplogEnd} 가 base 스냅샷의 oplog
	// 일관 시점(window/restore replay 하한)의 진본이다. 백업 script 가 S3 에 쓰지만
	// 그 값을 CR status 로 끌어오는 건 여기 컨트롤러 몫이다 (안 하면 uploader 의
	// window 계산이 앵커 없이 비어 PITR restore 가 base 시점만 허용). Job 완료 +
	// S3 + PITR 활성 + 아직 미기록일 때 *1회* 읽는다 (retry 클로저 밖 — 네트워크
	// 호출 중복 방지). best-effort: 읽기 실패해도 백업 완료 자체는 유효하므로
	// 로그만 남기고 진행한다 (다음 reconcile 이 재시도 — 단 Completed guard 에
	// 막히므로 실질 1회, OplogStart 는 nil 로 남아 window 만 degrade).
	var resolvedOplogStart *metav1.Time
	if jobComplete && backup.Status.OplogStart == nil && r.Store != nil &&
		backup.Spec.Storage.Type == "s3" && backup.Spec.Storage.S3 != nil {
		oplogStart, err := r.Store.ReadBaseOplogEnd(ctx, backup.Spec.Storage.S3, backup.Namespace, backup.Name)
		if err != nil {
			log.FromContext(ctx).Error(err, "base.meta.json 읽기 실패 — status.OplogStart 미기록(PITR window degrade)",
				"backup", backup.Name)
		} else {
			resolvedOplogStart = oplogStart // nil 이면 --oplog 없이 뜬 base
		}
	}

	// 직전 status mutation 들을 클로저로 묶어 conflict 재시도 시에도 동일 적용 보장.
	// job 은 위에서 한 번 Get 한 값을 그대로 사용한다 (job 상태 자체는 재조회 불필요).
	applyJobStatus := func() {
		// Check job conditions
		for _, condition := range job.Status.Conditions {
			if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
				backup.Status.Phase = backupPhaseCompleted
				backup.Status.CompletionTime = condition.LastTransitionTime.DeepCopy()
				break
			}
			if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
				backup.Status.Phase = backupPhaseFailed
				backup.Status.Error = condition.Message
				backup.Status.CompletionTime = condition.LastTransitionTime.DeepCopy()
				break
			}
		}

		// If job is running
		if job.Status.Active > 0 {
			backup.Status.Phase = "Running"
		}

		// base 스냅샷의 oplog 일관 시점 (한 번 해석했으면 매 retry 동일 적용).
		if resolvedOplogStart != nil {
			backup.Status.OplogStart = resolvedOplogStart.DeepCopy()
		}

		// Set location based on storage type
		if backup.Spec.Storage.Type == "s3" && backup.Spec.Storage.S3 != nil {
			backup.Status.Location = fmt.Sprintf("s3://%s/%s%s",
				backup.Spec.Storage.S3.Bucket,
				backup.Spec.Storage.S3.Prefix,
				backup.Name)
		}
	}
	applyJobStatus()

	return commonsstatus.UpdateWithRetry(ctx, r.Client, backup, applyJobStatus)
}

// updateRestoreStatus — restore Job 의 condition 을 관찰해 CR phase 를 종단
// (Completed/Failed) 으로 전이한다. updateBackupStatus 의 restore 대응물로,
// 구조는 동일하되 두 가지가 다르다:
//   - Job active 중 phase 는 Restoring 유지 (백업의 Running 대응). 별도 마킹
//     불필요 — 진입 시 이미 Restoring 이다.
//   - Status.Location 미설정. Location 은 *백업이 쓴 산출물* 의 위치이고 restore
//     는 그 위치를 읽기만 하므로 restore CR 에 기록하면 오독을 부른다.
func (r *MongoDBBackupReconciler) updateRestoreStatus(ctx context.Context, backup *mongodbv1alpha1.MongoDBBackup, jobName string) error {
	job := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: backup.Namespace}, job); err != nil {
		return err
	}

	// 직전 status mutation 을 클로저로 묶어 conflict 재시도 시에도 동일 적용 보장
	// (updateBackupStatus 동일 패턴). job 은 위에서 한 번 Get 한 값을 그대로 사용.
	applyJobStatus := func() {
		for _, condition := range job.Status.Conditions {
			if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
				backup.Status.Phase = backupPhaseCompleted
				backup.Status.CompletionTime = condition.LastTransitionTime.DeepCopy()
				break
			}
			if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
				backup.Status.Phase = backupPhaseFailed
				backup.Status.Error = condition.Message
				backup.Status.CompletionTime = condition.LastTransitionTime.DeepCopy()
				break
			}
		}
	}
	applyJobStatus()

	return commonsstatus.UpdateWithRetry(ctx, r.Client, backup, applyJobStatus)
}

func (r *MongoDBBackupReconciler) updateStatusError(ctx context.Context, backup *mongodbv1alpha1.MongoDBBackup, err error) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Error(err, "Backup failed")

	applyFailedStatus := func() {
		backup.Status.Phase = backupPhaseFailed
		backup.Status.Error = err.Error()
		backup.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	}
	applyFailedStatus()

	if statusErr := commonsstatus.UpdateWithRetry(ctx, r.Client, backup, applyFailedStatus); statusErr != nil {
		logger.Error(statusErr, "Failed to update status")
	}

	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *MongoDBBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDBBackup{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
