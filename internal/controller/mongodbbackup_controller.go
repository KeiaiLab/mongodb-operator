/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
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

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/resources"
	commonsfinalizer "github.com/keiailab/operator-commons/pkg/finalizer"
)

const (
	// mongodbBackupFinalizer — api/v1alpha1.FinalizerMongoDBBackup local alias.
	mongodbBackupFinalizer = mongodbv1alpha1.FinalizerMongoDBBackup

	backupPhaseCompleted = "Completed"
	backupPhaseFailed    = "Failed"
	backupPhasePending   = "Pending"
	backupPhaseRunning   = "Running"
	backupPhaseRestoring = "Restoring"
)

// MongoDBBackupReconciler reconciles a MongoDBBackup object
type MongoDBBackupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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
	// 본 CR 은 *백업 capture 가 아닌 restore 작업* 으로 해석한다. 본 cycle 의
	// acceptance 는 *path 식별 + Phase=Restoring 진입* 까지. 실제 mongorestore
	// + --oplogReplay 명령 실행은 cycle 6 (KMS encryption + ETag verify) 통합
	// 시점에서 강화 — 그 사이 phase 는 사용자가 의도적으로 reset 하지 않는 한
	// Restoring 유지.
	if backup.Spec.Restore != nil {
		if backup.Status.Phase == "" || backup.Status.Phase == backupPhasePending {
			backup.Status.Phase = backupPhaseRestoring
			backup.Status.StartTime = &metav1.Time{Time: time.Now()}
			if err := updateStatusWithRetry(ctx, r.Client, backup); err != nil {
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
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
	}

	// Update status to Running if not set
	if backup.Status.Phase == "" {
		backup.Status.Phase = backupPhasePending
		backup.Status.StartTime = &metav1.Time{Time: time.Now()}
		if err := updateStatusWithRetry(ctx, r.Client, backup); err != nil {
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
	return handleFinalizerCleanup(ctx, r.Client, backup, mongodbBackupFinalizer, cleanup)
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

func (r *MongoDBBackupReconciler) updateBackupStatus(ctx context.Context, backup *mongodbv1alpha1.MongoDBBackup, jobName string) error {
	job := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: backup.Namespace}, job); err != nil {
		return err
	}

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

	// Set location based on storage type
	if backup.Spec.Storage.Type == "s3" && backup.Spec.Storage.S3 != nil {
		backup.Status.Location = fmt.Sprintf("s3://%s/%s%s",
			backup.Spec.Storage.S3.Bucket,
			backup.Spec.Storage.S3.Prefix,
			backup.Name)
	}

	return updateStatusWithRetry(ctx, r.Client, backup)
}

func (r *MongoDBBackupReconciler) updateStatusError(ctx context.Context, backup *mongodbv1alpha1.MongoDBBackup, err error) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Error(err, "Backup failed")

	backup.Status.Phase = backupPhaseFailed
	backup.Status.Error = err.Error()
	backup.Status.CompletionTime = &metav1.Time{Time: time.Now()}

	if statusErr := updateStatusWithRetry(ctx, r.Client, backup); statusErr != nil {
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
