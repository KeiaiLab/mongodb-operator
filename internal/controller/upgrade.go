package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

	// perMemberRolloutBudget — 멤버 1개당 rollout 여유 시간(stepDown + 재시작 + ready).
	// validationTimeout = interval + members×budget. 정상 rollout이 느려도(큰 클러스터/느린
	// 노드) timeout으로 오판 rollback 하지 않도록 멤버 수에 비례해 충분히 준다. 진짜 실패
	// (ImagePull/CrashLoop)는 stsRolloutStuck이 budget 무관하게 즉시 감지한다.
	perMemberRolloutBudget = 3 * time.Minute
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
	// validationTimeout = interval + members×budget. 정상 rollout이 느려도(큰 클러스터)
	// timeout으로 오판 rollback 하지 않도록 멤버 비례 budget(갭3 fix — 기존 interval×3은
	// rollout보다 짧아 정상 업그레이드를 rollback으로 오판했음).
	members := mdb.Spec.Members
	if members < 1 {
		members = 1
	}
	validationTimeout := interval + time.Duration(members)*perMemberRolloutBudget

	if mdb.Status.UpgradeStartTime == nil {
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	elapsed := time.Since(mdb.Status.UpgradeStartTime.Time)

	// 버전-aware 검증(갭2): STS rollout 완료(UpdatedReplicas==Replicas &&
	// CurrentRevision==UpdateRevision && ReadyReplicas==Replicas) 확인.
	rolloutDone, err := r.stsRolloutComplete(ctx, mdb.Name, mdb.Namespace, mdb.Spec.Members)
	if err != nil {
		logger.Error(err, "failed to read STS rollout status during validation")
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	if rolloutDone {
		// rollout 완료 후 interval 동안 안정성 관찰(즉시 complete 아님).
		if elapsed < interval {
			return ctrl.Result{RequeueAfter: interval - elapsed}, true, nil
		}
		logger.Info("upgrade validation passed (STS rollout complete)", "version", desiredVersion)
		// FCV 자동 commit(갭5, point-of-no-return): 검증 통과 후 FCV 상향. 이후 다운그레이드 불가.
		fcv, ferr := r.commitFCV(ctx, mdb, desiredVersion)
		if ferr != nil {
			logger.Error(ferr, "FCV commit failed, will retry (upgrade not yet complete)")
			return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
		}
		completeUpgrade := func() {
			mdb.Status.Version = desiredVersion
			mdb.Status.EffectiveVersion = desiredVersion
			mdb.Status.FCVVersion = fcv // 클로저 안에서 설정(UpdateWithRetry refetch 덮어쓰기 방지).
			mdb.Status.RollbackActive = false
			mdb.Status.UpgradePhase = ""
			mdb.Status.UpgradeStartTime = nil
			mdb.Status.UpgradeRetryCount = 0
			mdb.Status.ObservedUpgradeGeneration = mdb.Generation
			setUpgradeCondition(mdb, "UpgradeComplete", metav1.ConditionTrue,
				"Upgraded", fmt.Sprintf("Successfully upgraded to %s (FCV %s committed)", desiredVersion, fcv))
		}
		completeUpgrade()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, completeUpgrade); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	// rollout 미완료: 진짜 stuck(ImagePull/CrashLoop 등)이면 budget 무관 즉시 롤백/실패
	// (잘못된 버전 이미지 등 — 기다릴 가치 없음). 정상 진행 중이면 budget까지 대기.
	if stuck, reason := r.stsRolloutStuck(ctx, mdb.Name, mdb.Namespace); stuck {
		logger.Info("upgrade rollout stuck — failing fast", "reason", reason)
		return r.upgradeValidationFailed(ctx, mdb, desiredVersion,
			fmt.Sprintf("rollout stuck: %s", reason))
	}

	if elapsed < validationTimeout {
		logger.Info("upgrade validation pending, STS rollout in progress",
			"readyMembers", mdb.Status.ReadyMembers, "expected", mdb.Spec.Members,
			"elapsed", elapsed.String(), "timeout", validationTimeout.String())
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	// budget 초과(정상 진행 신호도 없이 안 끝남) → 실패 처리.
	logger.Info("upgrade validation timed out",
		"readyMembers", mdb.Status.ReadyMembers, "expected", mdb.Spec.Members, "elapsed", elapsed.String())
	return r.upgradeValidationFailed(ctx, mdb, desiredVersion,
		fmt.Sprintf("timed out after %s", validationTimeout))
}

// upgradeValidationFailed는 검증 실패 시 롤백(RollbackOnFailure) 또는 terminal 실패로
// 분기한다. FCV는 검증 통과 전이라 미commit → 롤백(바이너리 다운그레이드) 안전.
func (r *MongoDBReconciler) upgradeValidationFailed(ctx context.Context, mdb *mongodbv1alpha1.MongoDB, desiredVersion, reason string) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)
	if mdb.Spec.UpgradeStrategy != nil && mdb.Spec.UpgradeStrategy.RollbackOnFailure {
		logger.Info("initiating rollback", "reason", reason)
		setRollingBack := func() { mdb.Status.UpgradePhase = UpgradePhaseRollingBack }
		setRollingBack()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdb, setRollingBack); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, true, nil
	}
	// RollbackOnFailure=false → terminal 실패(루프 안정성: generation 기록).
	failUpgrade := func() {
		setUpgradeCondition(mdb, "UpgradeFailed", metav1.ConditionTrue,
			"ValidationFailed", fmt.Sprintf("Upgrade to %s failed (%s), manual intervention required", desiredVersion, reason))
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

// stsRolloutStuck은 rollout이 진짜 실패(잘못된 이미지/CrashLoop 등)로 멈췄는지 판정한다.
// "느린 정상 rollout"과 "진짜 stuck"을 구분 — stuck이면 budget 무관 즉시 롤백(갭3 핵심).
// pod 컨테이너가 ImagePullBackOff/ErrImagePull/CrashLoopBackOff/InvalidImageName waiting이면 stuck.
func (r *MongoDBReconciler) stsRolloutStuck(ctx context.Context, name, namespace string) (bool, string) {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sts); err != nil {
		return false, ""
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(namespace),
		client.MatchingLabels(sts.Spec.Selector.MatchLabels)); err != nil {
		return false, ""
	}
	for i := range pods.Items {
		for _, cs := range pods.Items[i].Status.ContainerStatuses {
			if cs.State.Waiting == nil {
				continue
			}
			switch cs.State.Waiting.Reason {
			case "ImagePullBackOff", "ErrImagePull", "CrashLoopBackOff", "InvalidImageName":
				return true, fmt.Sprintf("%s/%s: %s", pods.Items[i].Name, cs.Name, cs.State.Waiting.Reason)
			}
		}
	}
	return false, ""
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
// 자동 상향하고(갭5, point-of-no-return) commit된 fcv 문자열을 반환한다. 이미 같은 FCV면
// SetFCV가 idempotent no-op. 실패 시 error → 호출자가 업그레이드 미완료로 처리(롤백 가능 유지).
// status 기록은 호출자의 completeUpgrade 클로저가 담당한다(UpdateWithRetry refetch가 여기서
// 설정한 status를 덮어쓰는 것을 방지 — 갭 fix).
func (r *MongoDBReconciler) commitFCV(ctx context.Context, mdb *mongodbv1alpha1.MongoDB, desiredVersion string) (string, error) {
	fcv := fcvMajorMinor(desiredVersion)
	if fcv == "" {
		return "", fmt.Errorf("cannot derive FCV from version %q", desiredVersion)
	}
	if mdb.Status.FCVVersion == fcv {
		return fcv, nil // 이미 commit됨
	}
	mgr, err := r.newRSManager(ctx, mdb)
	if err != nil {
		return "", fmt.Errorf("rs manager for FCV commit: %w", err)
	}
	if err := mgr.SetFCV(ctx, mdb.Name+"-0", mdb.Namespace, fcv); err != nil {
		return "", err
	}
	return fcv, nil
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
