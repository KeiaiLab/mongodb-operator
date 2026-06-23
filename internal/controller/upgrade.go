package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"

	appsv1 "k8s.io/api/apps/v1"
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

	// EffectiveVersion 초기화: 빈 값(신규/기존 클러스터)이면 desired로 seed(첫 reconcile
	// 시 spec.Version과 동일 → 무롤링). 이후 업그레이드/롤백이 이 값을 SSOT로 조작한다.
	if mdb.Status.EffectiveVersion == "" {
		seed := func() { mdb.Status.EffectiveVersion = desiredVersion }
		seed()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, seed); err != nil {
			return ctrl.Result{}, false, err
		}
	}

	if currentVersion == "" || currentVersion == desiredVersion {
		if mdb.Status.UpgradePhase != "" {
			clearUpgrade := func() {
				mdb.Status.UpgradePhase = ""
				mdb.Status.UpgradeStartTime = nil
			}
			clearUpgrade()
			if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, clearUpgrade); err != nil {
				return ctrl.Result{}, false, err
			}
		}
		return ctrl.Result{}, false, nil
	}

	// 루프 안정성(갭6): 같은 spec generation에서 이미 terminal 결과(실패/롤백)에
	// 도달했으면 재업그레이드를 시도하지 않는다. 사용자가 spec을 변경(generation 증가)
	// 해야 retry가 리셋된다 → 검증실패↔롤백 무한루프 구조적 차단.
	if mdb.Status.UpgradePhase == "" &&
		mdb.Status.ObservedUpgradeGeneration == mdb.Generation &&
		mdb.Status.ObservedUpgradeGeneration != 0 {
		logger.Info("upgrade already terminal for this generation, awaiting spec change",
			"generation", mdb.Generation, "retries", mdb.Status.UpgradeRetryCount)
		return ctrl.Result{}, false, nil
	}

	switch mdb.Status.UpgradePhase {
	case "":
		logger.Info("upgrade detected", "from", currentVersion, "to", desiredVersion,
			"attempt", mdb.Status.UpgradeRetryCount+1)
		initUpgrade := func() {
			mdb.Status.PreviousVersion = currentVersion
			mdb.Status.EffectiveVersion = desiredVersion
			mdb.Status.RollbackActive = false
			now := metav1.Now()
			mdb.Status.UpgradeStartTime = &now

			if mdb.Spec.UpgradeStrategy != nil && mdb.Spec.UpgradeStrategy.PreUpgradeBackup {
				mdb.Status.UpgradePhase = UpgradePhaseBackingUp
			} else {
				mdb.Status.UpgradePhase = UpgradePhaseUpgrading
			}
		}
		initUpgrade()

		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, initUpgrade); err != nil {
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
		setUpgrading := func() {
			mdb.Status.UpgradePhase = UpgradePhaseUpgrading
		}
		setUpgrading()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, setUpgrading); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, true, nil
	case "Failed":
		logger.Info("pre-upgrade backup failed, aborting upgrade")
		abortUpgrade := func() {
			mdb.Status.UpgradePhase = ""
			mdb.Status.UpgradeStartTime = nil
			setUpgradeCondition(mdb, "UpgradeFailed", metav1.ConditionTrue,
				"BackupFailed", "Pre-upgrade backup failed, upgrade aborted")
		}
		abortUpgrade()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, abortUpgrade); err != nil {
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

	// 버전-aware 검증(갭2): ReadyMembers 수만 보지 않고, STS rollout이 완료되어
	// 모든 pod가 desired 버전으로 교체됐는지 확인한다(UpdatedReplicas==Replicas &&
	// CurrentRevision==UpdateRevision && ReadyReplicas==Replicas).
	rolloutDone, err := r.stsRolloutComplete(ctx, mdb.Name, mdb.Namespace, mdb.Spec.Members)
	if err != nil {
		logger.Error(err, "failed to read STS rollout status during validation")
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	if rolloutDone {
		logger.Info("upgrade validation passed (STS rollout complete)", "version", desiredVersion)
		// FCV 자동 commit(갭5, point-of-no-return): 검증 통과 후 FCV 상향 → 새 기능 활성.
		// 이 시점 이후 바이너리 다운그레이드(롤백) 불가. 검증 통과 후이므로 안전.
		if err := r.commitFCV(ctx, mdb, desiredVersion); err != nil {
			logger.Error(err, "FCV commit failed, will retry (upgrade not yet complete)")
			return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
		}
		completeUpgrade := func() {
			mdb.Status.Version = desiredVersion
			mdb.Status.EffectiveVersion = desiredVersion
			mdb.Status.RollbackActive = false
			mdb.Status.UpgradePhase = ""
			mdb.Status.UpgradeStartTime = nil
			mdb.Status.UpgradeRetryCount = 0
			mdb.Status.ObservedUpgradeGeneration = mdb.Generation
			setUpgradeCondition(mdb, "UpgradeComplete", metav1.ConditionTrue,
				"Upgraded", fmt.Sprintf("Successfully upgraded to %s (FCV committed)", desiredVersion))
		}
		completeUpgrade()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, completeUpgrade); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	if elapsed < validationTimeout {
		logger.Info("upgrade validation pending, STS rollout not complete yet",
			"readyMembers", mdb.Status.ReadyMembers, "expected", mdb.Spec.Members,
			"elapsed", elapsed.String(), "timeout", validationTimeout.String())
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	logger.Info("upgrade validation timed out",
		"readyMembers", mdb.Status.ReadyMembers, "expected", mdb.Spec.Members,
		"elapsed", elapsed.String())

	// 검증 실패 → 롤백(RollbackOnFailure) 또는 terminal 실패. FCV는 아직 commit 전이라
	// 롤백(바이너리 다운그레이드) 안전.
	if mdb.Spec.UpgradeStrategy != nil && mdb.Spec.UpgradeStrategy.RollbackOnFailure {
		logger.Info("initiating rollback due to validation timeout")
		setRollingBack := func() {
			mdb.Status.UpgradePhase = UpgradePhaseRollingBack
		}
		setRollingBack()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, setRollingBack); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, true, nil
	}

	// RollbackOnFailure=false → terminal 실패(루프 안정성: generation 기록).
	failUpgrade := func() {
		setUpgradeCondition(mdb, "UpgradeFailed", metav1.ConditionTrue,
			"ValidationTimeout", fmt.Sprintf("Upgrade to %s timed out after %s, manual intervention required", desiredVersion, validationTimeout))
		mdb.Status.UpgradePhase = ""
		mdb.Status.UpgradeStartTime = nil
		mdb.Status.ObservedUpgradeGeneration = mdb.Generation
	}
	failUpgrade()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, failUpgrade); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{}, true, nil
}

// stsRolloutComplete는 StatefulSet의 rollout이 완료(모든 pod가 최신 revision으로 ready)
// 됐는지 판정한다. 버전-aware 업그레이드 검증의 핵심(ReadyMembers 수만 보는 것보다 정확).
func (r *MongoDBReconciler) stsRolloutComplete(ctx context.Context, name, namespace string, members int32) (bool, error) {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sts); err != nil {
		return false, err
	}
	return sts.Status.UpdatedReplicas == members &&
		sts.Status.ReadyReplicas == members &&
		sts.Status.CurrentRevision == sts.Status.UpdateRevision, nil
}

func (r *MongoDBReconciler) reconcileUpgradeRollback(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)

	if mdb.Status.PreviousVersion == "" {
		logger.Info("rollback requested but no previous version recorded, clearing upgrade state")
		clearUpgrade := func() {
			mdb.Status.UpgradePhase = ""
			mdb.Status.UpgradeStartTime = nil
		}
		clearUpgrade()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, clearUpgrade); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	if mdb.Spec.UpgradeStrategy == nil || !mdb.Spec.UpgradeStrategy.RollbackOnFailure {
		logger.Info("rollback phase reached but RollbackOnFailure is disabled, clearing upgrade state")
		failUpgrade := func() {
			mdb.Status.UpgradePhase = ""
			mdb.Status.UpgradeStartTime = nil
			mdb.Status.ObservedUpgradeGeneration = mdb.Generation
			setUpgradeCondition(mdb, "UpgradeFailed", metav1.ConditionTrue,
				"ValidationFailed", "Upgrade validation failed, manual intervention required")
		}
		failUpgrade()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, failUpgrade); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	// 롤백 GitOps화(갭3): spec.Version을 직접 Update하지 않는다(webhook downgrade 차단 +
	// Flux SSOT 충돌 회피). 대신 Status.EffectiveVersion=PreviousVersion으로 설정 →
	// builder가 EffectiveVersion으로 STS를 재빌드 → 무중단 롤링으로 이전 버전 복귀.
	// 사용자 spec.Version(git SSOT)은 불변. FCV는 검증 통과 전이므로 미commit → 안전.
	logger.Info("rolling back to previous version (status-based, spec preserved)",
		"effectiveVersion", mdb.Status.PreviousVersion, "specVersion", mdb.Spec.Version.Version)

	finishRollback := func() {
		mdb.Status.EffectiveVersion = mdb.Status.PreviousVersion
		mdb.Status.RollbackActive = true
		mdb.Status.UpgradePhase = ""
		mdb.Status.UpgradeStartTime = nil
		// 루프 안정성(갭6): 롤백도 terminal. 같은 generation 재업그레이드 차단 →
		// 검증실패↔롤백 무한루프 방지. 사용자가 spec.Version을 고쳐야(generation++) 재시도.
		mdb.Status.UpgradeRetryCount++
		mdb.Status.ObservedUpgradeGeneration = mdb.Generation
		setUpgradeCondition(mdb, "UpgradeRolledBack", metav1.ConditionTrue,
			"RolledBack", fmt.Sprintf("Rolled back to %s (spec.Version %s preserved; change spec to retry)",
				mdb.Status.PreviousVersion, mdb.Spec.Version.Version))
	}
	finishRollback()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, finishRollback); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{}, true, nil
}

// commitFCV는 업그레이드 검증 통과 후 featureCompatibilityVersion을 desired major.minor로
// 자동 상향한다(갭5, point-of-no-return). 이미 같은 FCV면 SetFCV가 idempotent no-op.
// 실패 시 error 반환 → 호출자가 업그레이드 미완료로 처리(롤백 가능 상태 유지).
func (r *MongoDBReconciler) commitFCV(ctx context.Context, mdb *mongodbv1alpha1.MongoDB, desiredVersion string) error {
	fcv := fcvMajorMinor(desiredVersion)
	if fcv == "" {
		return fmt.Errorf("cannot derive FCV from version %q", desiredVersion)
	}
	if mdb.Status.FCVVersion == fcv {
		return nil // 이미 commit됨
	}
	mgr, err := r.newRSManager(ctx, mdb)
	if err != nil {
		return fmt.Errorf("rs manager for FCV commit: %w", err)
	}
	if err := mgr.SetFCV(ctx, mdb.Name+"-0", mdb.Namespace, fcv); err != nil {
		return err
	}
	mdb.Status.FCVVersion = fcv
	return nil
}

// fcvMajorMinor는 "8.2.1" → "8.2" 로 major.minor만 추출한다(FCV 형식).
func fcvMajorMinor(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
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

//nolint:unparam // status param reserved for future ConditionFalse cases
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
