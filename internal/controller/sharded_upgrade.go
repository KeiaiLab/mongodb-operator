package controller

import (
	"context"
	"fmt"
	"time"

	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/mongodb"
)

// upgradeReasonFCVNotCommitted — 바이너리는 굴렀지만 FCV 는 올리지 않고 업그레이드를 닫았다.
const upgradeReasonFCVNotCommitted = "FCVNotCommitted"

func (r *MongoDBShardedReconciler) reconcileShardedUpgrade(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)

	desiredVersion := mdbsh.Spec.Version.Version

	// Status.EffectiveVersion + Status.Version 초기화(무중단 업그레이드 SSOT).
	// **adopt된 기존 클러스터 버그 fix**: Helm→OLM 인수 클러스터는 Status.Version 이 비어 있어
	// (completeUpgrade에서만 set) reconcile 진입 시 currentVersion="" → 업그레이드 orchestration
	// 영구 skip + EffectiveVersion stale 고착(keiailab-mongo 8.3.4 패치 사고). Version을
	// EffectiveVersion 으로 초기화해 spec.Version 변경을 업그레이드로 감지 가능하게 한다.
	if mdbsh.Status.EffectiveVersion == "" || mdbsh.Status.Version == "" {
		seed := func() {
			if mdbsh.Status.EffectiveVersion == "" {
				mdbsh.Status.EffectiveVersion = desiredVersion
			}
			if mdbsh.Status.Version == "" {
				mdbsh.Status.Version = mdbsh.Status.EffectiveVersion
			}
		}
		seed()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, seed); err != nil {
			return ctrl.Result{}, false, err
		}
	}

	currentVersion := mdbsh.Status.Version // seed 후 읽음(adopt 클러스터 초기화 반영).

	// 진행 중인 중단(spec 되돌림)은 spec 이 다시 바뀌어도 끝까지 간다.
	if shardedAbortRolling(mdbsh) {
		return r.continueShardedAbort(ctx, mdbsh)
	}

	// spec 이 status.version 으로 돌아왔는데 EffectiveVersion 이 새 버전이면 업그레이드 중단이다.
	if currentVersion == desiredVersion {
		if result, handled, err := r.reconcileShardedRevert(ctx, mdbsh); handled || err != nil {
			return result, handled, err
		}
	}

	if currentVersion == "" || currentVersion == desiredVersion {
		if mdbsh.Status.UpgradePhase != "" {
			applyStatus := func() {
				mdbsh.Status.UpgradePhase = ""
				mdbsh.Status.UpgradeStartTime = nil
			}
			applyStatus()
			if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
				return ctrl.Result{}, false, err
			}
		}
		return ctrl.Result{}, false, nil
	}

	// 루프 안정성(갭6): 같은 generation에서 이미 terminal 결과면 재업그레이드 skip
	// (검증실패↔롤백 무한루프 차단). 사용자 spec 변경(generation++) 시에만 retry 리셋.
	if mdbsh.Status.UpgradePhase == "" &&
		mdbsh.Status.ObservedUpgradeGeneration == mdbsh.Generation &&
		mdbsh.Status.ObservedUpgradeGeneration != 0 {
		logger.Info("sharded upgrade already terminal for this generation, awaiting spec change",
			"generation", mdbsh.Generation, "retries", mdbsh.Status.UpgradeRetryCount)
		return ctrl.Result{}, false, nil
	}

	switch mdbsh.Status.UpgradePhase {
	case "":
		logger.Info("sharded upgrade detected", "from", currentVersion, "to", desiredVersion,
			"attempt", mdbsh.Status.UpgradeRetryCount+1)
		applyStatus := func() {
			mdbsh.Status.PreviousVersion = currentVersion
			mdbsh.Status.EffectiveVersion = desiredVersion
			mdbsh.Status.RollbackActive = false
			now := metav1.Now()
			mdbsh.Status.UpgradeStartTime = &now

			if mdbsh.Spec.UpgradeStrategy != nil && mdbsh.Spec.UpgradeStrategy.PreUpgradeBackup {
				mdbsh.Status.UpgradePhase = UpgradePhaseBackingUp
			} else {
				mdbsh.Status.UpgradePhase = UpgradePhaseUpgrading
			}
		}
		applyStatus()

		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
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
		// 롤아웃 예산은 Upgrading 시작부터 잰다 — 백업에 쓴 시간을 예산에서 빼지 않는다.
		upgradingSince := metav1.Now()
		applyStatus := func() {
			mdbsh.Status.UpgradePhase = UpgradePhaseUpgrading
			mdbsh.Status.UpgradeStartTime = &upgradingSince
		}
		applyStatus()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
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
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
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
	validationTimeout := shardedValidationTimeout(mdbsh)

	if mdbsh.Status.UpgradeStartTime == nil {
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	elapsed := time.Since(mdbsh.Status.UpgradeStartTime.Time)
	if elapsed < interval {
		return ctrl.Result{RequeueAfter: interval - elapsed}, true, nil
	}

	// 롤아웃 완료를 라이브로 판정한다. Status 의 Ready 수는 Upgrading · Validating 동안
	// 갱신되지 않아(updateStatus 미도달) 업그레이드 전 값으로 굳어 있다.
	rolledOut := r.shardedRolloutComplete(ctx, mdbsh)

	if rolledOut {
		logger.Info("sharded upgrade validation passed", "version", desiredVersion)
		// 바이너리 버전을 알 수 없으면(spec.version.image 가 목표 버전을 선언하지 않음) FCV 를
		// 올리지 않는다 — version 만 올린 CR 은 template 이 안 바뀌어 옛 바이너리가 그대로다.
		if !shardedBinaryKnown(mdbsh) {
			return r.completeShardedUpgradeNoFCV(ctx, mdbsh, desiredVersion)
		}
		// FCV 자동 commit(갭5): sharded는 mongos를 통해 setFCV하면 config+shards 전체에
		// 전파된다. 검증 통과 후이므로 안전(point-of-no-return). mongos 연결로 commit.
		fcv, err := r.commitShardedFCV(ctx, mdbsh, desiredVersion)
		if err != nil {
			logger.Error(err, "sharded FCV commit failed, will retry (upgrade not yet complete)")
			return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
		}
		applyStatus := func() {
			mdbsh.Status.Version = desiredVersion
			mdbsh.Status.EffectiveVersion = desiredVersion
			mdbsh.Status.FCVVersion = fcv // 클로저 안에서 설정(UpdateWithRetry refetch 덮어쓰기 방지).
			mdbsh.Status.RollbackActive = false
			mdbsh.Status.UpgradePhase = ""
			mdbsh.Status.UpgradeStartTime = nil
			mdbsh.Status.UpgradeRetryCount = 0
			mdbsh.Status.ObservedUpgradeGeneration = mdbsh.Generation
			setShardedUpgradeCondition(mdbsh, "UpgradeComplete", metav1.ConditionTrue,
				"Upgraded", fmt.Sprintf("Successfully upgraded to %s (FCV %s committed)", desiredVersion, fcv))
		}
		applyStatus()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	if elapsed < validationTimeout {
		logger.Info("sharded upgrade validation pending",
			"elapsed", elapsed.String(), "timeout", validationTimeout.String())
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	return r.shardedUpgradeTimedOut(ctx, mdbsh, desiredVersion)
}

// completeShardedUpgradeNoFCV 는 롤아웃은 끝났지만 바이너리 버전을 확인할 수 없는
// 업그레이드를 FCV 를 건드리지 않고 닫는다. FCV 는 운영자가 바이너리를 확인한 뒤 올린다.
func (r *MongoDBShardedReconciler) completeShardedUpgradeNoFCV(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, desiredVersion string) (ctrl.Result, bool, error) {
	log.FromContext(ctx).Info("sharded upgrade closed without FCV commit: spec.version.image does not declare the target version",
		"version", desiredVersion, "image", mdbsh.Spec.Version.Image)

	applyStatus := func() {
		mdbsh.Status.Version = desiredVersion
		mdbsh.Status.EffectiveVersion = desiredVersion
		mdbsh.Status.RollbackActive = false
		mdbsh.Status.UpgradePhase = ""
		mdbsh.Status.UpgradeStartTime = nil
		mdbsh.Status.UpgradeRetryCount = 0
		mdbsh.Status.ObservedUpgradeGeneration = mdbsh.Generation
		setShardedUpgradeCondition(mdbsh, "UpgradeComplete", metav1.ConditionTrue, upgradeReasonFCVNotCommitted,
			fmt.Sprintf("Rolled out for %s; FCV left at %q: spec.version.image %q does not declare version %s, set featureCompatibilityVersion manually once the binaries are confirmed",
				desiredVersion, mdbsh.Status.FCVVersion, mdbsh.Spec.Version.Image, desiredVersion))
	}
	applyStatus()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{}, true, nil
}

// shardedUpgradeTimedOut 은 롤아웃 예산을 넘긴 업그레이드를 롤백(RollbackOnFailure) 또는
// terminal 실패로 보낸다. FCV 는 미commit 이라 바이너리 복귀가 안전하다.
func (r *MongoDBShardedReconciler) shardedUpgradeTimedOut(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, desiredVersion string) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)

	if mdbsh.Spec.UpgradeStrategy != nil && mdbsh.Spec.UpgradeStrategy.RollbackOnFailure {
		logger.Info("sharded upgrade validation timed out, initiating rollback")
		applyStatus := func() {
			mdbsh.Status.UpgradePhase = UpgradePhaseRollingBack
		}
		applyStatus()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, true, nil
	}

	applyStatus := func() {
		setShardedUpgradeCondition(mdbsh, "UpgradeFailed", metav1.ConditionTrue,
			"ValidationTimeout", fmt.Sprintf("Upgrade to %s timed out, manual intervention required", desiredVersion))
		mdbsh.Status.UpgradePhase = ""
		mdbsh.Status.UpgradeStartTime = nil
		mdbsh.Status.ObservedUpgradeGeneration = mdbsh.Generation
	}
	applyStatus()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
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
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	if mdbsh.Spec.UpgradeStrategy == nil || !mdbsh.Spec.UpgradeStrategy.RollbackOnFailure {
		applyStatus := func() {
			mdbsh.Status.UpgradePhase = ""
			mdbsh.Status.UpgradeStartTime = nil
			mdbsh.Status.ObservedUpgradeGeneration = mdbsh.Generation
			setShardedUpgradeCondition(mdbsh, "UpgradeFailed", metav1.ConditionTrue,
				"ValidationFailed", "Upgrade validation failed, manual intervention required")
		}
		applyStatus()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
			return ctrl.Result{}, false, err
		}
		return ctrl.Result{}, true, nil
	}

	// 롤백 GitOps화(갭3): spec.Version 직접 Update 제거(webhook downgrade 차단 + Flux SSOT
	// 충돌 회피). Status.EffectiveVersion=PreviousVersion → builder가 STS를 이전 버전으로
	// 재빌드 → 무중단 롤링 복귀. spec.Version(git SSOT) 불변. FCV는 검증 통과 전이라 미commit.
	previousVersion := mdbsh.Status.PreviousVersion
	logger.Info("rolling back sharded cluster (status-based, spec preserved)",
		"effectiveVersion", previousVersion, "specVersion", mdbsh.Spec.Version.Version)

	applyStatus := func() {
		mdbsh.Status.EffectiveVersion = previousVersion
		mdbsh.Status.RollbackActive = true
		mdbsh.Status.UpgradePhase = ""
		mdbsh.Status.UpgradeStartTime = nil
		// 루프 안정성(갭6): 롤백도 terminal — 같은 generation 재업그레이드 차단.
		mdbsh.Status.UpgradeRetryCount++
		mdbsh.Status.ObservedUpgradeGeneration = mdbsh.Generation
		setShardedUpgradeCondition(mdbsh, "UpgradeRolledBack", metav1.ConditionTrue,
			"RolledBack", fmt.Sprintf("Rolled back to %s (spec.Version %s preserved; change spec to retry)",
				previousVersion, mdbsh.Spec.Version.Version))
	}
	applyStatus()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{}, true, nil
}

// commitShardedFCV는 검증 통과 후 mongos를 통해 featureCompatibilityVersion을 상향한다(갭5).
// mongos setFCV는 config server + 모든 shard에 전파된다(sharded 클러스터 전체 commit).
// point-of-no-return — 이후 바이너리 다운그레이드 불가. 검증 통과 후이므로 안전.
// 이미 같은 FCV면 SetFCV가 idempotent no-op.
func (r *MongoDBShardedReconciler) commitShardedFCV(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, desiredVersion string) (string, error) {
	fcv := fcvMajorMinor(desiredVersion)
	if fcv == "" {
		return "", fmt.Errorf("cannot derive FCV from version %q", desiredVersion)
	}
	if mdbsh.Status.FCVVersion == fcv {
		return fcv, nil
	}
	adminPassword, err := r.getAdminPassword(ctx, mdbsh)
	if err != nil {
		return "", fmt.Errorf("get admin password for FCV commit: %w", err)
	}
	mongosPod, err := r.getMongosPodName(ctx, mdbsh)
	if err != nil {
		return "", fmt.Errorf("get mongos pod for FCV commit: %w", err)
	}
	// mongos(27017) 경유 setFCV — 클러스터 전체 전파. status 기록은 호출자 클로저가 담당.
	mgr := mongodb.NewReplicaSetManagerWithFactory(
		mongodb.NewServiceConnectFactory(mdbsh.Name+"-mongos", mdbsh.Namespace, 27017, "admin", adminPassword, "admin"),
	)
	if err := mgr.SetFCV(ctx, mongosPod, mdbsh.Namespace, fcv); err != nil {
		return "", err
	}
	return fcv, nil
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
