/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

package controller

import (
	"context"
	"fmt"
	"time"

	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// 업그레이드 중단(spec.version 을 이전 버전으로 되돌림)의 condition.
//
//	UpgradeAborted=False/RollingBack            컴포넌트를 옛 버전으로 되돌리는 중
//	UpgradeAborted=True/Reverted                전부 옛 버전으로 돌아옴
//	UpgradeAborted=True/CancelledBeforeRollout  구르기 전(BackingUp)에 취소
//	UpgradeAborted=False/FCVCommitted           FCV 가 이미 올라가 되돌리지 않음
const (
	upgradeConditionAborted     = "UpgradeAborted"
	abortReasonRollingBack      = "RollingBack"
	abortReasonReverted         = "Reverted"
	abortReasonCancelledEarly   = "CancelledBeforeRollout"
	abortReasonFCVCommitted     = "FCVCommitted"
	abortStatusRequeue          = 1 * time.Second
	abortOrderMongos            = "mongos"
	abortOrderShards            = "shards"
	abortOrderConfigServer      = "config server"
	abortOrderDescription       = "mongos → shards → config servers"
	abortBlockedMessageTemplate = "spec.version reverted to %s, but FCV %s for %s is already committed: " +
		"binaries stay on %s (older binaries cannot start on that FCV). Set spec.version back to %s"
)

// shardedRevert 는 spec.version 이 status.version 과 같은데 EffectiveVersion 이 다른 버전을
// 가리킬 때(= 업그레이드 도중 spec 을 되돌림) 할 일이다.
type shardedRevert int

const (
	// revertNone — 되돌릴 것이 없다.
	revertNone shardedRevert = iota
	// revertBlocked — 새 버전의 FCV 가 commit 돼 바이너리를 내릴 수 없다.
	revertBlocked
	// revertCancel — 아직 아무것도 구르지 않았다(BackingUp).
	revertCancel
	// revertStart — 일부 또는 전부가 새 바이너리로 갔을 수 있다. 순서대로 되돌린다.
	revertStart
)

// shardedFCVCommittedFor 는 status 가 version 의 FCV commit 을 기록했고, 그 FCV 가 현재
// 버전(status.version)의 것과 다른지 본다. 같은 FCV 안의 패치 되돌림은 막지 않는다.
func shardedFCVCommittedFor(mdbsh *mongodbv1alpha1.MongoDBSharded, version string) bool {
	fcv := fcvMajorMinor(version)

	return fcv != "" &&
		mdbsh.Status.FCVVersion == fcv &&
		fcv != fcvMajorMinor(mdbsh.Status.Version)
}

func classifyShardedRevert(mdbsh *mongodbv1alpha1.MongoDBSharded) shardedRevert {
	effective := mdbsh.Status.EffectiveVersion
	if effective == "" || effective == mdbsh.Spec.Version.Version {
		return revertNone
	}

	if shardedFCVCommittedFor(mdbsh, effective) {
		return revertBlocked
	}
	if mdbsh.Status.UpgradePhase == UpgradePhaseBackingUp {
		return revertCancel
	}
	return revertStart
}

// shardedAbortRolling 은 중단이 진행 중인지 CR status 만으로 답한다(오퍼레이터 재시작 뒤에도
// 같은 답). timeout 롤백이 잠깐 거치는 RollingBack 은 EffectiveVersion 이 아직 새 버전이라
// 여기 걸리지 않는다.
func shardedAbortRolling(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return mdbsh.Status.UpgradePhase == UpgradePhaseRollingBack &&
		mdbsh.Status.Version != "" &&
		mdbsh.Status.EffectiveVersion == mdbsh.Status.Version
}

// reconcileShardedRevert 는 spec.version == status.version 일 때 불린다. 업그레이드가 남긴
// EffectiveVersion 을 그대로 두면 다음 reconcile 이 모든 컴포넌트를 게이트 없이 새 버전으로
// 올린다 — 되돌림을 중단으로 처리한다.
func (r *MongoDBShardedReconciler) reconcileShardedRevert(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, bool, error) {
	switch classifyShardedRevert(mdbsh) {
	case revertBlocked:
		return r.holdShardedRevert(ctx, mdbsh)
	case revertCancel:
		return r.cancelShardedUpgrade(ctx, mdbsh)
	case revertStart:
		return r.startShardedAbort(ctx, mdbsh)
	}
	return ctrl.Result{}, false, nil
}

// holdShardedRevert 는 FCV 가 이미 올라간 클러스터를 그대로 둔다: 워크로드도 upgrade status 도
// 건드리지 않고 이유만 남긴다. spec.version 을 새 버전으로 돌려놓으면 멈춘 자리에서 이어 간다.
func (r *MongoDBShardedReconciler) holdShardedRevert(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, bool, error) {
	running := mdbsh.Status.EffectiveVersion
	message := fmt.Sprintf(abortBlockedMessageTemplate,
		mdbsh.Spec.Version.Version, mdbsh.Status.FCVVersion, running, running, running)

	// 이미 같은 내용이면 다시 쓰지 않는다 — reconcile 마다 status 를 흔들지 않는다.
	recorded := meta.FindStatusCondition(mdbsh.Status.Conditions, upgradeConditionAborted)
	if recorded != nil && recorded.Reason == abortReasonFCVCommitted && recorded.Message == message {
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
	}

	log.FromContext(ctx).Info("sharded upgrade revert refused: FCV already committed",
		"specVersion", mdbsh.Spec.Version.Version, "runningVersion", running, "fcv", mdbsh.Status.FCVVersion)

	applyStatus := func() {
		setShardedUpgradeCondition(mdbsh, upgradeConditionAborted, metav1.ConditionFalse, abortReasonFCVCommitted, message)
	}
	applyStatus()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, true, nil
}

// cancelShardedUpgrade 는 BackingUp 에서 되돌린 업그레이드를 닫는다. 이 단계는 워크로드를
// 적용하지 않으므로 되돌릴 바이너리가 없다.
func (r *MongoDBShardedReconciler) cancelShardedUpgrade(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, bool, error) {
	abandoned := mdbsh.Status.EffectiveVersion
	current := mdbsh.Status.Version
	log.FromContext(ctx).Info("sharded upgrade cancelled before rollout", "from", current, "abandoned", abandoned)

	applyStatus := func() {
		mdbsh.Status.EffectiveVersion = current
		mdbsh.Status.RollbackActive = false
		mdbsh.Status.UpgradePhase = ""
		mdbsh.Status.UpgradeStartTime = nil
		setShardedUpgradeCondition(mdbsh, upgradeConditionAborted, metav1.ConditionTrue, abortReasonCancelledEarly,
			fmt.Sprintf("Upgrade to %s cancelled during pre-upgrade backup: spec.version reverted to %s, no component was rolled",
				abandoned, current))
	}
	applyStatus()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{RequeueAfter: abortStatusRequeue}, true, nil
}

// startShardedAbort 는 중단을 status 에 먼저 기록한다. 워크로드는 다음 reconcile 부터
// continueShardedAbort 가 순서대로 되돌린다.
func (r *MongoDBShardedReconciler) startShardedAbort(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, bool, error) {
	abandoned := mdbsh.Status.EffectiveVersion
	current := mdbsh.Status.Version
	log.FromContext(ctx).Info("sharded upgrade aborted by spec revert, returning components in reverse order",
		"to", current, "abandoned", abandoned, "phase", mdbsh.Status.UpgradePhase)

	applyStatus := func() {
		mdbsh.Status.EffectiveVersion = current
		mdbsh.Status.RollbackActive = false
		mdbsh.Status.UpgradePhase = UpgradePhaseRollingBack
		mdbsh.Status.UpgradeStartTime = nil
		setShardedUpgradeCondition(mdbsh, upgradeConditionAborted, metav1.ConditionFalse, abortReasonRollingBack,
			fmt.Sprintf("Upgrade to %s aborted: spec.version reverted to %s, returning components in order %s",
				abandoned, current, abortOrderDescription))
	}
	applyStatus()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{RequeueAfter: abortStatusRequeue}, true, nil
}

// continueShardedAbort 는 업그레이드의 역순으로 한 단계씩 옛 버전을 적용한다. 앞 단계가 옛
// 이미지로 다 구른 것을 라이브로 확인한 뒤에만 다음 단계로 간다. 아직 옛 바이너리인
// 컴포넌트에는 같은 이미지가 적용돼 구르지 않는다. FCV 는 건드리지 않는다.
//
//	mongos ──rolled out──▶ shards ──rolled out──▶ config server ──rolled out──▶ 닫힘
//
// 목표는 spec 이 아니라 status.version 이다 — 중단 도중 spec 이 다시 바뀌어도 중단을 끝낸 뒤에
// 새 업그레이드를 감지한다.
func (r *MongoDBShardedReconciler) continueShardedAbort(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, bool, error) {
	target := mdbsh.DeepCopy()
	target.Spec.Version.Version = mdbsh.Status.Version

	if err := r.reconcileMongos(ctx, target); err != nil {
		return ctrl.Result{}, false, err
	}
	if !r.mongosRolledOut(ctx, target) {
		return awaitShardedAbort(ctx, abortOrderMongos), true, nil
	}

	for i := int32(0); i < target.Spec.Shards.Count; i++ {
		if err := r.reconcileShard(ctx, target, i); err != nil {
			return ctrl.Result{}, false, err
		}
	}
	if !r.shardsRolledOut(ctx, target) {
		return awaitShardedAbort(ctx, abortOrderShards), true, nil
	}

	if err := r.reconcileConfigServer(ctx, target); err != nil {
		return ctrl.Result{}, false, err
	}
	if !r.configServerRolledOut(ctx, target) {
		return awaitShardedAbort(ctx, abortOrderConfigServer), true, nil
	}

	return r.finishShardedAbort(ctx, mdbsh)
}

func awaitShardedAbort(ctx context.Context, component string) ctrl.Result {
	log.FromContext(ctx).Info("sharded upgrade abort waiting for rollout before next component", "rolling", component)
	return ctrl.Result{RequeueAfter: upgradeRequeueInterval}
}

func (r *MongoDBShardedReconciler) finishShardedAbort(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) (ctrl.Result, bool, error) {
	current := mdbsh.Status.Version
	log.FromContext(ctx).Info("sharded upgrade abort complete", "version", current)

	applyStatus := func() {
		mdbsh.Status.UpgradePhase = ""
		mdbsh.Status.UpgradeStartTime = nil
		setShardedUpgradeCondition(mdbsh, upgradeConditionAborted, metav1.ConditionTrue, abortReasonReverted,
			fmt.Sprintf("Upgrade aborted: every component is back on %s (order %s), FCV left at %q",
				current, abortOrderDescription, mdbsh.Status.FCVVersion))
	}
	applyStatus()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{RequeueAfter: abortStatusRequeue}, true, nil
}
