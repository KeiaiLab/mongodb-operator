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

const (
	UpgradePhaseBackingUp   = "BackingUp"
	UpgradePhaseUpgrading   = "Upgrading"
	UpgradePhaseValidating  = "Validating"
	UpgradePhaseRollingBack = "RollingBack"

	defaultValidationInterval = 60 * time.Second
	upgradeRequeueInterval    = 30 * time.Second
)

// reconcileUpgrade orchestrates version upgrades with optional backup, validation, and rollback.
// Returns (result, handled, error). When handled=true the caller should return early.
func (r *MongoDBReconciler) reconcileUpgrade(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)

	desiredVersion := mdb.Spec.Version.Version
	currentVersion := mdb.Status.Version

	if currentVersion == "" || currentVersion == desiredVersion {
		if mdb.Status.UpgradePhase != "" {
			mdb.Status.UpgradePhase = ""
			mdb.Status.UpgradeStartTime = nil
			if err := updateStatusWithRetry(ctx, r.Client, mdb); err != nil {
				return ctrl.Result{}, false, err
			}
		}
		return ctrl.Result{}, false, nil
	}

	switch mdb.Status.UpgradePhase {
	case "":
		logger.Info("upgrade detected", "from", currentVersion, "to", desiredVersion)
		mdb.Status.PreviousVersion = currentVersion
		now := metav1.Now()
		mdb.Status.UpgradeStartTime = &now

		if mdb.Spec.UpgradeStrategy != nil && mdb.Spec.UpgradeStrategy.PreUpgradeBackup {
			mdb.Status.UpgradePhase = UpgradePhaseBackingUp
		} else {
			mdb.Status.UpgradePhase = UpgradePhaseUpgrading
		}

		if err := updateStatusWithRetry(ctx, r.Client, mdb); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, true, nil

	case UpgradePhaseBackingUp:
		return r.reconcileUpgradeBackup(ctx, mdb, desiredVersion)

	case UpgradePhaseUpgrading:
		return ctrl.Result{}, false, nil

	case UpgradePhaseValidating:
		return r.reconcileUpgradeValidation(ctx, mdb, desiredVersion)

	case UpgradePhaseRollingBack:
		return r.reconcileUpgradeRollback(ctx, mdb)
	}

	return ctrl.Result{}, false, nil
}

func (r *MongoDBReconciler) reconcileUpgradeBackup(ctx context.Context, mdb *mongodbv1alpha1.MongoDB, desiredVersion string) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)
	backupName := fmt.Sprintf("%s-pre-upgrade-%s", mdb.Name, desiredVersion)

	backup := &mongodbv1alpha1.MongoDBBackup{}
	err := r.Get(ctx, types.NamespacedName{Name: backupName, Namespace: mdb.Namespace}, backup)
	if err != nil {
		backup = &mongodbv1alpha1.MongoDBBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      backupName,
				Namespace: mdb.Namespace,
			},
			Spec: mongodbv1alpha1.MongoDBBackupSpec{
				ClusterRef: mongodbv1alpha1.ClusterReference{
					Name: mdb.Name,
					Kind: "MongoDB",
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
		mdb.Status.UpgradePhase = UpgradePhaseUpgrading
		if err := updateStatusWithRetry(ctx, r.Client, mdb); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, true, nil
	case "Failed":
		logger.Info("pre-upgrade backup failed, aborting upgrade")
		mdb.Status.UpgradePhase = ""
		mdb.Status.UpgradeStartTime = nil
		setUpgradeCondition(mdb, "UpgradeFailed", metav1.ConditionTrue,
			"BackupFailed", "Pre-upgrade backup failed, upgrade aborted")
		if err := updateStatusWithRetry(ctx, r.Client, mdb); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	default:
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}
}

func (r *MongoDBReconciler) reconcileUpgradeValidation(ctx context.Context, mdb *mongodbv1alpha1.MongoDB, desiredVersion string) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)
	interval := parseValidationInterval(mdb.Spec.UpgradeStrategy)
	validationTimeout := interval * 3

	if mdb.Status.UpgradeStartTime == nil {
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	elapsed := time.Since(mdb.Status.UpgradeStartTime.Time)

	if elapsed < interval {
		return ctrl.Result{RequeueAfter: interval - elapsed}, true, nil
	}

	ready := mdb.Status.ReadyMembers >= int32(mdb.Spec.Members)
	if ready {
		logger.Info("upgrade validation passed", "version", desiredVersion)
		mdb.Status.Version = desiredVersion
		mdb.Status.UpgradePhase = ""
		mdb.Status.UpgradeStartTime = nil
		setUpgradeCondition(mdb, "UpgradeComplete", metav1.ConditionTrue,
			"Upgraded", fmt.Sprintf("Successfully upgraded to %s", desiredVersion))
		if err := updateStatusWithRetry(ctx, r.Client, mdb); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	if elapsed < validationTimeout {
		logger.Info("upgrade validation pending, pods not ready yet",
			"readyMembers", mdb.Status.ReadyMembers, "expected", mdb.Spec.Members,
			"elapsed", elapsed.String(), "timeout", validationTimeout.String())
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	logger.Info("upgrade validation timed out",
		"readyMembers", mdb.Status.ReadyMembers, "expected", mdb.Spec.Members,
		"elapsed", elapsed.String())

	if mdb.Spec.UpgradeStrategy != nil && mdb.Spec.UpgradeStrategy.RollbackOnFailure {
		logger.Info("initiating rollback due to validation timeout")
		mdb.Status.UpgradePhase = UpgradePhaseRollingBack
		if err := updateStatusWithRetry(ctx, r.Client, mdb); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, true, nil
	}

	setUpgradeCondition(mdb, "UpgradeFailed", metav1.ConditionTrue,
		"ValidationTimeout", fmt.Sprintf("Upgrade to %s timed out after %s, manual intervention required", desiredVersion, validationTimeout))
	mdb.Status.UpgradePhase = ""
	mdb.Status.UpgradeStartTime = nil
	if err := updateStatusWithRetry(ctx, r.Client, mdb); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{}, true, nil
}

func (r *MongoDBReconciler) reconcileUpgradeRollback(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)

	if mdb.Status.PreviousVersion == "" {
		logger.Info("rollback requested but no previous version recorded, clearing upgrade state")
		mdb.Status.UpgradePhase = ""
		mdb.Status.UpgradeStartTime = nil
		if err := updateStatusWithRetry(ctx, r.Client, mdb); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	if mdb.Spec.UpgradeStrategy == nil || !mdb.Spec.UpgradeStrategy.RollbackOnFailure {
		logger.Info("rollback phase reached but RollbackOnFailure is disabled, clearing upgrade state")
		mdb.Status.UpgradePhase = ""
		mdb.Status.UpgradeStartTime = nil
		setUpgradeCondition(mdb, "UpgradeFailed", metav1.ConditionTrue,
			"ValidationFailed", "Upgrade validation failed, manual intervention required")
		if err := updateStatusWithRetry(ctx, r.Client, mdb); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	logger.Info("rolling back to previous version", "version", mdb.Status.PreviousVersion)

	mdb.Spec.Version.Version = mdb.Status.PreviousVersion
	if err := r.Update(ctx, mdb); err != nil {
		return ctrl.Result{}, false, err
	}

	mdb.Status.UpgradePhase = ""
	mdb.Status.UpgradeStartTime = nil
	setUpgradeCondition(mdb, "UpgradeRolledBack", metav1.ConditionTrue,
		"RolledBack", fmt.Sprintf("Rolled back to %s", mdb.Status.PreviousVersion))
	if err := updateStatusWithRetry(ctx, r.Client, mdb); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{}, true, nil
}

func parseValidationInterval(strategy *mongodbv1alpha1.UpgradeStrategySpec) time.Duration {
	if strategy == nil || strategy.ValidationInterval == "" {
		return defaultValidationInterval
	}
	d, err := time.ParseDuration(strategy.ValidationInterval)
	if err != nil {
		return defaultValidationInterval
	}
	return d
}

func setUpgradeCondition(mdb *mongodbv1alpha1.MongoDB, condType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	for i, c := range mdb.Status.Conditions {
		if c.Type == condType {
			mdb.Status.Conditions[i].Status = status
			mdb.Status.Conditions[i].Reason = reason
			mdb.Status.Conditions[i].Message = message
			mdb.Status.Conditions[i].LastTransitionTime = now
			return
		}
	}
	mdb.Status.Conditions = append(mdb.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	})
}
