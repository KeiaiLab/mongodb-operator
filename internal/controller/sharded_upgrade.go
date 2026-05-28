package controller

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func (r *MongoDBShardedReconciler) reconcileShardedUpgrade(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)

	desiredVersion := mdbsh.Spec.Version.Version
	currentVersion := mdbsh.Status.Version

	if currentVersion == "" || currentVersion == desiredVersion {
		if mdbsh.Status.UpgradePhase != "" {
			mdbsh.Status.UpgradePhase = ""
			mdbsh.Status.UpgradeStartTime = nil
			if err := updateStatusWithRetry(ctx, r.Client, mdbsh); err != nil {
				return ctrl.Result{}, false, err
			}
		}
		return ctrl.Result{}, false, nil
	}

	switch mdbsh.Status.UpgradePhase {
	case "":
		logger.Info("sharded upgrade detected", "from", currentVersion, "to", desiredVersion)
		mdbsh.Status.PreviousVersion = currentVersion
		now := metav1.Now()
		mdbsh.Status.UpgradeStartTime = &now

		if mdbsh.Spec.UpgradeStrategy != nil && mdbsh.Spec.UpgradeStrategy.PreUpgradeBackup {
			mdbsh.Status.UpgradePhase = UpgradePhaseBackingUp
		} else {
			mdbsh.Status.UpgradePhase = UpgradePhaseUpgrading
		}

		if err := updateStatusWithRetry(ctx, r.Client, mdbsh); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, true, nil

	case UpgradePhaseBackingUp:
		return r.reconcileShardedUpgradeBackup(ctx, mdbsh, desiredVersion)

	case UpgradePhaseUpgrading:
		return ctrl.Result{}, false, nil

	case UpgradePhaseValidating:
		return r.reconcileShardedUpgradeValidation(ctx, mdbsh, desiredVersion)

	case UpgradePhaseRollingBack:
		return r.reconcileShardedUpgradeRollback(ctx, mdbsh)
	}

	return ctrl.Result{}, false, nil
}

func (r *MongoDBShardedReconciler) reconcileShardedUpgradeBackup(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, desiredVersion string) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)
	backupName := fmt.Sprintf("%s-pre-upgrade-%s", mdbsh.Name, desiredVersion)

	backup := &mongodbv1alpha1.MongoDBBackup{}
	err := r.Get(ctx, types.NamespacedName{Name: backupName, Namespace: mdbsh.Namespace}, backup)
	if err != nil {
		backup = &mongodbv1alpha1.MongoDBBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      backupName,
				Namespace: mdbsh.Namespace,
			},
			Spec: mongodbv1alpha1.MongoDBBackupSpec{
				ClusterRef: mongodbv1alpha1.ClusterReference{
					Name: mdbsh.Name,
					Kind: "MongoDBSharded",
				},
			},
		}
		if err := r.Create(ctx, backup); err != nil {
			logger.Error(err, "failed to create pre-upgrade backup")
			return ctrl.Result{}, false, err
		}
		logger.Info("pre-upgrade backup created", "backup", backupName)
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	switch backup.Status.Phase {
	case "Completed":
		logger.Info("pre-upgrade backup completed, proceeding to upgrade")
		mdbsh.Status.UpgradePhase = UpgradePhaseUpgrading
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, true, nil
	case "Failed":
		logger.Info("pre-upgrade backup failed, aborting upgrade")
		mdbsh.Status.UpgradePhase = ""
		mdbsh.Status.UpgradeStartTime = nil
		setShardedUpgradeCondition(mdbsh, "UpgradeFailed", metav1.ConditionTrue,
			"BackupFailed", "Pre-upgrade backup failed, upgrade aborted")
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	default:
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}
}

func (r *MongoDBShardedReconciler) reconcileShardedUpgradeValidation(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, desiredVersion string) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)
	interval := parseValidationInterval(mdbsh.Spec.UpgradeStrategy)
	validationTimeout := interval * 3

	if mdbsh.Status.UpgradeStartTime == nil {
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	elapsed := time.Since(mdbsh.Status.UpgradeStartTime.Time)
	if elapsed < interval {
		return ctrl.Result{RequeueAfter: interval - elapsed}, true, nil
	}

	allReady := mdbsh.Status.ConfigServer.Ready >= mdbsh.Status.ConfigServer.Total &&
		mdbsh.Status.Mongos.Ready >= mdbsh.Status.Mongos.Total
	for _, s := range mdbsh.Status.Shards {
		if s.Ready < s.Total {
			allReady = false
			break
		}
	}

	if allReady {
		logger.Info("sharded upgrade validation passed", "version", desiredVersion)
		mdbsh.Status.Version = desiredVersion
		mdbsh.Status.UpgradePhase = ""
		mdbsh.Status.UpgradeStartTime = nil
		setShardedUpgradeCondition(mdbsh, "UpgradeComplete", metav1.ConditionTrue,
			"Upgraded", fmt.Sprintf("Successfully upgraded to %s", desiredVersion))
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	if elapsed < validationTimeout {
		logger.Info("sharded upgrade validation pending",
			"elapsed", elapsed.String(), "timeout", validationTimeout.String())
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	if mdbsh.Spec.UpgradeStrategy != nil && mdbsh.Spec.UpgradeStrategy.RollbackOnFailure {
		logger.Info("sharded upgrade validation timed out, initiating rollback")
		mdbsh.Status.UpgradePhase = UpgradePhaseRollingBack
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, true, nil
	}

	setShardedUpgradeCondition(mdbsh, "UpgradeFailed", metav1.ConditionTrue,
		"ValidationTimeout", fmt.Sprintf("Upgrade to %s timed out, manual intervention required", desiredVersion))
	mdbsh.Status.UpgradePhase = ""
	mdbsh.Status.UpgradeStartTime = nil
	if err := updateStatusWithRetry(ctx, r.Client, mdbsh); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{}, true, nil
}

func (r *MongoDBShardedReconciler) reconcileShardedUpgradeRollback(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)

	if mdbsh.Status.PreviousVersion == "" {
		mdbsh.Status.UpgradePhase = ""
		mdbsh.Status.UpgradeStartTime = nil
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	if mdbsh.Spec.UpgradeStrategy == nil || !mdbsh.Spec.UpgradeStrategy.RollbackOnFailure {
		mdbsh.Status.UpgradePhase = ""
		mdbsh.Status.UpgradeStartTime = nil
		setShardedUpgradeCondition(mdbsh, "UpgradeFailed", metav1.ConditionTrue,
			"ValidationFailed", "Upgrade validation failed, manual intervention required")
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	logger.Info("rolling back sharded cluster to previous version", "version", mdbsh.Status.PreviousVersion)
	mdbsh.Spec.Version.Version = mdbsh.Status.PreviousVersion
	if err := r.Update(ctx, mdbsh); err != nil {
		return ctrl.Result{}, false, err
	}

	mdbsh.Status.UpgradePhase = ""
	mdbsh.Status.UpgradeStartTime = nil
	setShardedUpgradeCondition(mdbsh, "UpgradeRolledBack", metav1.ConditionTrue,
		"RolledBack", fmt.Sprintf("Rolled back to %s", mdbsh.Status.PreviousVersion))
	if err := updateStatusWithRetry(ctx, r.Client, mdbsh); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{}, true, nil
}

func setShardedUpgradeCondition(mdbsh *mongodbv1alpha1.MongoDBSharded, condType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	for i, c := range mdbsh.Status.Conditions {
		if c.Type == condType {
			mdbsh.Status.Conditions[i].Status = status
			mdbsh.Status.Conditions[i].Reason = reason
			mdbsh.Status.Conditions[i].Message = message
			mdbsh.Status.Conditions[i].LastTransitionTime = now
			return
		}
	}
	mdbsh.Status.Conditions = append(mdbsh.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	})
}
