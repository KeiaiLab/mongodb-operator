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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

const (
	mongodbBackupFinalizer = "mongodbbackup.keiailab.com/finalizer"

	backupPhaseCompleted = "Completed"
	backupPhaseFailed    = "Failed"
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
		if errors.IsNotFound(err) {
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
	if !controllerutil.ContainsFinalizer(backup, mongodbBackupFinalizer) {
		controllerutil.AddFinalizer(backup, mongodbBackupFinalizer)
		if err := r.Update(ctx, backup); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if backup is already completed or failed
	if backup.Status.Phase == backupPhaseCompleted || backup.Status.Phase == backupPhaseFailed {
		return ctrl.Result{}, nil
	}

	// Update status to Running if not set
	if backup.Status.Phase == "" {
		backup.Status.Phase = "Pending"
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
	if backup.Status.Phase == "Running" || backup.Status.Phase == "Pending" {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
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
	connectionString := fmt.Sprintf("mongodb://%s:%s@%s/?authSource=admin",
		username, password, host)

	return connectionString, nil
}

func (r *MongoDBBackupReconciler) createOrUpdate(ctx context.Context, backup *mongodbv1alpha1.MongoDBBackup, obj client.Object) error {
	// Set owner reference
	if err := controllerutil.SetControllerReference(backup, obj, r.Scheme); err != nil {
		return err
	}

	// Check if object exists
	existing := obj.DeepCopyObject().(client.Object)
	err := r.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, existing)

	if err != nil {
		if errors.IsNotFound(err) {
			// Create the object
			return r.Create(ctx, obj)
		}
		return err
	}

	// Job already exists, don't update
	return nil
}

func (r *MongoDBBackupReconciler) updateBackupStatus(ctx context.Context, backup *mongodbv1alpha1.MongoDBBackup, jobName string) error {
	job := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: backup.Namespace}, job); err != nil {
		return err
	}

	// Check job conditions
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			backup.Status.Phase = "Completed"
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
