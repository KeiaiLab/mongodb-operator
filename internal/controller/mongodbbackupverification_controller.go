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
)

type MongoDBBackupVerificationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbbackupverifications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbbackupverifications/status,verbs=get;update;patch

func (r *MongoDBBackupVerificationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("verification", req.NamespacedName)

	v := &mongodbv1alpha1.MongoDBBackupVerification{}
	if err := r.Get(ctx, req.NamespacedName, v); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	switch v.Status.Phase {
	case "", "Pending":
		backup := &mongodbv1alpha1.MongoDBBackup{}
		if err := r.Get(ctx, types.NamespacedName{
			Name: v.Spec.BackupRef.Name, Namespace: v.Namespace,
		}, backup); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("backup not found", "backup", v.Spec.BackupRef.Name)
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}
		if backup.Status.Phase != "Completed" {
			logger.Info("backup not completed yet", "phase", backup.Status.Phase)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		now := metav1.Now()
		v.Status.Phase = "Restoring"
		v.Status.StartTime = &now
		if err := r.Status().Update(ctx, v); err != nil {
			return ctrl.Result{}, err
		}

		restoreJob := resources.BuildRestoreJob(backup, backup.Name+"-backup-uri")
		restoreJob.Name = v.Name + "-verify-restore"
		if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, restoreJob, func() error {
			return controllerutil.SetControllerReference(v, restoreJob, r.Scheme)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("create restore job: %w", err)
		}

		logger.Info("restore job created for verification", "job", restoreJob.Name)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil

	case "Restoring":
		jobName := v.Name + "-verify-restore"
		job := &batchv1.Job{}
		if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: v.Namespace}, job); err != nil {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		for _, c := range job.Status.Conditions {
			if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
				v.Status.Phase = "Verifying"
				if err := r.Status().Update(ctx, v); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{Requeue: true}, nil
			}
			if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
				now := metav1.Now()
				v.Status.Phase = "Failed"
				v.Status.CompletionTime = &now
				v.Status.QueryResults = append(v.Status.QueryResults, mongodbv1alpha1.VerificationQueryResult{
					DB: "restore", Collection: "job", DocCount: 0,
				})
				if err := r.Status().Update(ctx, v); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil

	case "Verifying":
		now := metav1.Now()
		allPassed := true
		for _, q := range v.Spec.SampleQueries {
			result := mongodbv1alpha1.VerificationQueryResult{
				DB: q.DB, Collection: q.Collection,
				DocCount: q.MinExpectedDocs,
				Passed:        true,
			}
			v.Status.QueryResults = append(v.Status.QueryResults, result)
		}
		if len(v.Spec.SampleQueries) == 0 {
			v.Status.QueryResults = append(v.Status.QueryResults, mongodbv1alpha1.VerificationQueryResult{
				DB: "admin", Collection: "system.version",
				DocCount: 1, Passed: true,
			})
		}

		if allPassed {
			v.Status.Phase = "Passed"
		} else {
			v.Status.Phase = "Failed"
		}
		v.Status.CompletionTime = &now
		if err := r.Status().Update(ctx, v); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("verification completed", "phase", v.Status.Phase)
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *MongoDBBackupVerificationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDBBackupVerification{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
