/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

package controller

import (
	"context"
	"errors"
	"fmt"

	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/mongodb"
)

// abortReasonFCVUnconfirmed — 라이브 FCV 를 확정하지 못해 중단을 멈춰 둔다.
const abortReasonFCVUnconfirmed = "FCVUnconfirmed"

const (
	configServerPort = 27019
	shardPort        = 27018
	headlessSuffix   = "-headless"
)

// shardedFCVReader 는 cfg 와 모든 shard 의 mongod 멤버가 지금 들고 있는 FCV 문서를 돌려준다.
type shardedFCVReader func(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) ([]mongodb.FCVState, error)

// liveFCV 는 바이너리를 내려도 되는지에 대한 라이브 판정이다.
type liveFCV int

const (
	// liveFCVUnknown — 읽기 실패 · 전환 중 · 멤버끼리 불일치. 내리지 않는다.
	liveFCVUnknown liveFCV = iota
	// liveFCVOld — 모든 멤버가 옛 버전의 FCV 로 확정돼 있다. 내려도 된다.
	liveFCVOld
	// liveFCVCommitted — 모든 멤버가 다른 FCV 로 확정돼 있다. 옛 바이너리는 뜨지 못한다.
	liveFCVCommitted
)

// judgeLiveFCV 는 멤버 전원이 같은 확정 FCV 일 때만 답을 낸다.
//
//	전원 == oldFCV                      → liveFCVOld
//	전원 == abandonedFCV(모르면 아무 값) → liveFCVCommitted
//	그 밖(빈 목록 · targetVersion · 불일치) → liveFCVUnknown
//
// 둘째 반환값은 전원이 합의한 FCV(Old · Committed) 또는 확정하지 못한 이유(Unknown)다.
func judgeLiveFCV(states []mongodb.FCVState, oldFCV, abandonedFCV string) (liveFCV, string) {
	if oldFCV == "" {
		return liveFCVUnknown, "status.version does not yield an FCV"
	}
	if len(states) == 0 {
		return liveFCVUnknown, "no mongod member reported an FCV"
	}

	agreed := states[0].Version
	for _, state := range states {
		if !state.Stable() {
			return liveFCVUnknown, fmt.Sprintf("FCV transition in progress (version %q, targetVersion %q)",
				state.Version, state.TargetVersion)
		}
		if state.Version != agreed {
			return liveFCVUnknown, fmt.Sprintf("members disagree on FCV (%q and %q)", agreed, state.Version)
		}
	}

	if agreed == oldFCV {
		return liveFCVOld, agreed
	}
	if abandonedFCV == "" || agreed == abandonedFCV {
		return liveFCVCommitted, agreed
	}
	return liveFCVUnknown, fmt.Sprintf("FCV %q matches neither the previous nor the abandoned version", agreed)
}

// readShardedFCV 는 cfg 와 존재하는 모든 shard StatefulSet 의 멤버 pod 하나하나에 직접 붙어
// FCV 를 읽는다(mongos 에는 이 파라미터가 없다). 한 멤버라도 못 읽으면 에러 — 못 읽은 멤버가
// primary 일 수 있다.
func (r *MongoDBShardedReconciler) readShardedFCV(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) ([]mongodb.FCVState, error) {
	adminPassword, err := r.getAdminPassword(ctx, mdbsh)
	if err != nil {
		return nil, err
	}

	list := &appsv1.StatefulSetList{}
	if err := r.liveReader().List(ctx, list,
		client.InNamespace(mdbsh.Namespace), client.MatchingLabels{labelInstance: mdbsh.Name}); err != nil {
		return nil, fmt.Errorf("list statefulsets: %w", err)
	}

	states := []mongodb.FCVState{}
	sawConfigServer := false
	for i := range list.Items {
		sts := &list.Items[i]
		port := shardPort
		switch {
		case sts.Name == mdbsh.Name+"-cfg":
			port = configServerPort
			sawConfigServer = true
		case !isShardStatefulSet(mdbsh, sts):
			continue
		}

		members, err := r.readStatefulSetFCV(ctx, sts, port, adminPassword)
		if err != nil {
			return nil, err
		}
		states = append(states, members...)
	}

	if !sawConfigServer {
		return nil, errors.New("config server statefulset not found")
	}
	return states, nil
}

func (r *MongoDBShardedReconciler) readStatefulSetFCV(ctx context.Context, sts *appsv1.StatefulSet, port int, adminPassword string) ([]mongodb.FCVState, error) {
	manager := r.newRSManager(sts.Name+headlessSuffix, port, adminPassword)
	states := []mongodb.FCVState{}

	for ordinal := int32(0); ordinal < desiredReplicas(sts.Spec.Replicas); ordinal++ {
		pod := fmt.Sprintf("%s-%d", sts.Name, ordinal)
		state, err := manager.GetFCVState(ctx, pod, sts.Namespace)
		if err != nil {
			return nil, fmt.Errorf("read FCV from %s: %w", pod, err)
		}
		states = append(states, state)
	}
	return states, nil
}

// confirmAbortFCV 는 라이브 FCV 를 읽어 판정한다. 읽기 실패는 Unknown 이다(fail-closed).
func (r *MongoDBShardedReconciler) confirmAbortFCV(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, abandonedFCV string) (liveFCV, string) {
	read := r.fcvReader
	if read == nil {
		read = r.readShardedFCV
	}

	states, err := read(ctx, mdbsh)
	if err != nil {
		return liveFCVUnknown, err.Error()
	}
	return judgeLiveFCV(states, fcvMajorMinor(mdbsh.Status.Version), abandonedFCV)
}

// holdAbortOnFCV 는 라이브 FCV 가 옛 것이 아닐 때 아무 워크로드도 옮기지 않고 이유를 남긴다.
// commit 된 것으로 읽혔으면 status.fcvVersion 도 그 값으로 고친다.
func (r *MongoDBShardedReconciler) holdAbortOnFCV(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, verdict liveFCV, detail string) (ctrl.Result, bool, error) {
	held := ctrl.Result{RequeueAfter: upgradeRequeueInterval}
	reason := abortReasonFCVUnconfirmed
	message := fmt.Sprintf("Live FCV could not be confirmed as %s, no component is moved: %s",
		fcvMajorMinor(mdbsh.Status.Version), detail)
	repairedFCV := mdbsh.Status.FCVVersion

	if verdict == liveFCVCommitted {
		reason = abortReasonFCVCommitted
		message = fmt.Sprintf("Live FCV is %s, not %s: binaries on %s cannot start on it, no component is moved back. Set spec.version to a %s release",
			detail, fcvMajorMinor(mdbsh.Status.Version), mdbsh.Status.Version, detail)
		repairedFCV = detail
	}

	// 같은 이유가 이미 적혀 있으면 다시 쓰지 않는다(읽기 에러 문구는 매번 다를 수 있다).
	recorded := meta.FindStatusCondition(mdbsh.Status.Conditions, upgradeConditionAborted)
	if recorded != nil && recorded.Reason == reason && mdbsh.Status.FCVVersion == repairedFCV {
		return held, true, nil
	}

	log.FromContext(ctx).Info("sharded upgrade abort held on live FCV", "reason", reason, "detail", detail)

	applyStatus := func() {
		mdbsh.Status.FCVVersion = repairedFCV
		setShardedUpgradeCondition(mdbsh, upgradeConditionAborted, metav1.ConditionFalse, reason, message)
	}
	applyStatus()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
		return ctrl.Result{}, false, err
	}
	return held, true, nil
}

// resumeAbortCondition 은 FCV 확인이 풀린 뒤 진행 중 condition 을 되돌려 놓는다.
func (r *MongoDBShardedReconciler) resumeAbortCondition(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	recorded := meta.FindStatusCondition(mdbsh.Status.Conditions, upgradeConditionAborted)
	if recorded != nil && recorded.Reason == abortReasonRollingBack {
		return nil
	}

	applyStatus := func() {
		setShardedUpgradeCondition(mdbsh, upgradeConditionAborted, metav1.ConditionFalse, abortReasonRollingBack,
			fmt.Sprintf("Upgrade aborted: returning components to %s in order %s", mdbsh.Status.Version, abortOrderDescription))
	}
	applyStatus()
	return commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus)
}

// rollForwardFromAbort 는 중단 도중 FCV 가 commit 된 것으로 읽혔고 spec.version 이 그 FCV 의
// 버전일 때 중단을 버리고 업그레이드로 되돌아간다. 이미 내려간 컴포넌트는 평소 순서 게이트
// (cfg → shards → mongos)로 다시 올라간다.
func (r *MongoDBShardedReconciler) rollForwardFromAbort(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, committedFCV string) (ctrl.Result, bool, error) {
	forward := mdbsh.Spec.Version.Version
	log.FromContext(ctx).Info("sharded upgrade abort abandoned: FCV already committed, rolling forward",
		"fcv", committedFCV, "version", forward)

	rollingSince := metav1.Now()
	applyStatus := func() {
		mdbsh.Status.FCVVersion = committedFCV
		mdbsh.Status.PreviousVersion = mdbsh.Status.Version
		mdbsh.Status.EffectiveVersion = forward
		mdbsh.Status.UpgradePhase = UpgradePhaseUpgrading
		mdbsh.Status.UpgradeStartTime = &rollingSince
		setShardedUpgradeCondition(mdbsh, upgradeConditionAborted, metav1.ConditionFalse, abortReasonFCVCommitted,
			fmt.Sprintf("Abort abandoned: live FCV is %s, rolling every component forward to %s", committedFCV, forward))
	}
	applyStatus()
	if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, applyStatus); err != nil {
		return ctrl.Result{}, false, err
	}
	return ctrl.Result{RequeueAfter: abortStatusRequeue}, true, nil
}
