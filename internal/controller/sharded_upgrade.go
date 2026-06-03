package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
			applyStatus := func() {
				mdbsh.Status.UpgradePhase = ""
				mdbsh.Status.UpgradeStartTime = nil
			}
			applyStatus()
			if err := updateStatusWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
				return ctrl.Result{}, false, err
			}
		}
		return ctrl.Result{}, false, nil
	}

	switch mdbsh.Status.UpgradePhase {
	case "":
		logger.Info("sharded upgrade detected", "from", currentVersion, "to", desiredVersion)
		applyStatus := func() {
			mdbsh.Status.PreviousVersion = currentVersion
			now := metav1.Now()
			mdbsh.Status.UpgradeStartTime = &now

			if mdbsh.Spec.UpgradeStrategy != nil && mdbsh.Spec.UpgradeStrategy.PreUpgradeBackup {
				mdbsh.Status.UpgradePhase = UpgradePhaseBackingUp
			} else {
				mdbsh.Status.UpgradePhase = UpgradePhaseUpgrading
			}
		}
		applyStatus()

		if err := updateStatusWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
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
		// NotFound가 아닌 에러(일시적 API 오류 등)는 신규 생성으로 진행하지 않고 전파한다.
		// 일시 오류를 NotFound로 오인해 Create하면 이미 존재하는 백업과 충돌하거나
		// 백업을 중복 생성할 수 있다.
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, false, err
		}
		// NotFound일 때만 pre-upgrade 백업을 신규 생성한다.
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
		applyStatus := func() {
			mdbsh.Status.UpgradePhase = UpgradePhaseUpgrading
		}
		applyStatus()
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, true, nil
	case "Failed":
		logger.Info("pre-upgrade backup failed, aborting upgrade")
		applyStatus := func() {
			mdbsh.Status.UpgradePhase = ""
			mdbsh.Status.UpgradeStartTime = nil
			setShardedUpgradeCondition(mdbsh, "UpgradeFailed", metav1.ConditionTrue,
				"BackupFailed", "Pre-upgrade backup failed, upgrade aborted")
		}
		applyStatus()
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
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
		applyStatus := func() {
			mdbsh.Status.Version = desiredVersion
			mdbsh.Status.UpgradePhase = ""
			mdbsh.Status.UpgradeStartTime = nil
			setShardedUpgradeCondition(mdbsh, "UpgradeComplete", metav1.ConditionTrue,
				"Upgraded", fmt.Sprintf("Successfully upgraded to %s", desiredVersion))
		}
		applyStatus()
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
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
		applyStatus := func() {
			mdbsh.Status.UpgradePhase = UpgradePhaseRollingBack
		}
		applyStatus()
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, true, nil
	}

	applyStatus := func() {
		setShardedUpgradeCondition(mdbsh, "UpgradeFailed", metav1.ConditionTrue,
			"ValidationTimeout", fmt.Sprintf("Upgrade to %s timed out, manual intervention required", desiredVersion))
		mdbsh.Status.UpgradePhase = ""
		mdbsh.Status.UpgradeStartTime = nil
	}
	applyStatus()
	if err := updateStatusWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{}, true, nil
}

func (r *MongoDBShardedReconciler) reconcileShardedUpgradeRollback(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)

	if mdbsh.Status.PreviousVersion == "" {
		applyStatus := func() {
			mdbsh.Status.UpgradePhase = ""
			mdbsh.Status.UpgradeStartTime = nil
		}
		applyStatus()
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	if mdbsh.Spec.UpgradeStrategy == nil || !mdbsh.Spec.UpgradeStrategy.RollbackOnFailure {
		applyStatus := func() {
			mdbsh.Status.UpgradePhase = ""
			mdbsh.Status.UpgradeStartTime = nil
			setShardedUpgradeCondition(mdbsh, "UpgradeFailed", metav1.ConditionTrue,
				"ValidationFailed", "Upgrade validation failed, manual intervention required")
		}
		applyStatus()
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	logger.Info("rolling back sharded cluster to previous version", "version", mdbsh.Status.PreviousVersion)

	// Spec→Status 순서 안전: 먼저 Spec(Version)을 Update한다. r.Update는 성공 시 서버가
	// 발급한 최신 ResourceVersion으로 mdbsh를 in-place 갱신하므로, 이후 status 갱신은
	// 그 최신 RV 위에서 시작한다. 즉 Spec update 이전의 stale RV로 status를 덮어쓰지 않는다.
	previousVersion := mdbsh.Status.PreviousVersion
	mdbsh.Spec.Version.Version = previousVersion
	if err := r.Update(ctx, mdbsh); err != nil {
		return ctrl.Result{}, false, err
	}

	// Spec update로 갱신된 mdbsh(최신 RV) 위에서 status를 갱신한다. conflict가 나더라도
	// updateStatusWithRetry가 refetch 후 applyStatus를 재적용하므로 stale 덮어쓰기가 없다.
	applyStatus := func() {
		mdbsh.Status.UpgradePhase = ""
		mdbsh.Status.UpgradeStartTime = nil
		setShardedUpgradeCondition(mdbsh, "UpgradeRolledBack", metav1.ConditionTrue,
			"RolledBack", fmt.Sprintf("Rolled back to %s", previousVersion))
	}
	applyStatus()
	if err := updateStatusWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{}, true, nil
}

//nolint:unparam // status param reserved for future ConditionFalse cases
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
