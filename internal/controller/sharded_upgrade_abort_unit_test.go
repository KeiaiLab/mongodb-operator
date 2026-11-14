/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

// sharded_upgrade_abort_unit_test.go — 업그레이드 도중 spec.version 을 이전 버전으로 되돌리면
// (git revert) 그것이 진짜 중단인지 가드한다: 옛 바이너리 컴포넌트는 새 버전으로 가지 않고,
// 이미 옮겨진 컴포넌트는 mongos → shards → config server 순서로 롤아웃 게이트를 거쳐 돌아온다.
//
// 롤아웃 status 는 sharded_upgrade_rollout_unit_test.go 와 같이 시험이 직접 쓴다.
package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/mongodb"
)

const (
	abortConditionType     = "UpgradeAborted"
	abortReasonInProgress  = "RollingBack"
	abortReasonDone        = "Reverted"
	abortReasonCancelled   = "CancelledBeforeRollout"
	abortReasonFCVBlocked  = "FCVCommitted"
	abortReasonFCVUnknown  = "FCVUnconfirmed"
	abortRevertedSpecGen   = int64(2)
	abortFailedUpgradeGen  = int64(1)
	abortSteadyReconciles  = 3
	abortStartedAgo        = time.Second
	abortBackupStartedAgo  = time.Minute
	abortValidationStarted = 2 * time.Minute
)

// newRevertedSharded 는 8.3.4 → 9.0.8 업그레이드가 phase 에 있을 때 spec.version 을 8.3.4 로
// 되돌린 CR 이다.
func newRevertedSharded(phase string, started time.Duration) *mongodbv1alpha1.MongoDBSharded {
	mdbsh := newRolloutSharded(phase, started)
	mdbsh.Spec.Version.Version = rolloutOldVersion
	mdbsh.Generation = abortRevertedSpecGen
	return mdbsh
}

// newAbortingSharded 는 중단이 진행 중인 CR 이다 — 오퍼레이터가 재시작해도 CR status 만으로
// 이 상태를 다시 읽는다.
func newAbortingSharded() *mongodbv1alpha1.MongoDBSharded {
	mdbsh := newRevertedSharded(UpgradePhaseRollingBack, abortStartedAgo)
	mdbsh.Status.EffectiveVersion = rolloutOldVersion
	mdbsh.Status.UpgradeStartTime = nil
	return mdbsh
}

// workloadsOn 은 onNew 에 든 워크로드만 새 버전으로, 나머지는 옛 버전으로 롤아웃이 끝난
// cfg · shard 2개 · mongos 를 돌려준다.
func workloadsOn(mdbsh *mongodbv1alpha1.MongoDBSharded, onNew ...string) []client.Object {
	moved := map[string]bool{}
	for _, name := range onNew {
		moved[name] = true
	}

	workloads := rolloutWorkloads(mdbsh, rolloutOldVersion, rolloutOldRevision)
	for i, upgraded := range rolloutWorkloads(mdbsh, rolloutNewVersion, rolloutNewRevision) {
		if moved[upgraded.GetName()] {
			workloads[i] = upgraded
		}
	}
	return workloads
}

func newAbortReconciler(t *testing.T, probe *rolloutProbe, mdbsh *mongodbv1alpha1.MongoDBSharded, onNew ...string) (*MongoDBShardedReconciler, client.Client) {
	t.Helper()
	s := newTestScheme(t)
	cl := newRolloutClient(t, s, probe, append(workloadsOn(mdbsh, onNew...), mdbsh)...)
	live := &liveFCVStub{states: fcvOnAllMembers(fcvMajorMinor(rolloutOldVersion))}
	return &MongoDBShardedReconciler{Client: cl, APIReader: cl, Scheme: s, fcvReader: live.read}, cl
}

// liveFCVStub 은 mongod 멤버들이 보고하는 FCV 를 대신한다.
type liveFCVStub struct {
	states []mongodb.FCVState
	err    error
	reads  int
}

func (l *liveFCVStub) read(context.Context, *mongodbv1alpha1.MongoDBSharded) ([]mongodb.FCVState, error) {
	l.reads++
	return l.states, l.err
}

// fcvOnAllMembers 는 cfg · shard-0 · shard-1 primary 가 같은 확정 FCV 를 보고하는 상태다.
func fcvOnAllMembers(fcv string) []mongodb.FCVState {
	return []mongodb.FCVState{{Version: fcv}, {Version: fcv}, {Version: fcv}}
}

// finishMongosRollout 은 Deployment 컨트롤러가 롤링을 끝낸 상태를 쓴다.
func finishMongosRollout(t *testing.T, cl client.Client) {
	t.Helper()
	dep := getDeployment(t, cl, rolloutMongosName)
	dep.Status.ObservedGeneration = dep.Generation
	dep.Status.Replicas = *dep.Spec.Replicas
	dep.Status.UpdatedReplicas = *dep.Spec.Replicas
	dep.Status.AvailableReplicas = *dep.Spec.Replicas
	if err := cl.Status().Update(context.Background(), dep); err != nil {
		t.Fatalf("finish mongos rollout: %v", err)
	}
}

func assertImages(t *testing.T, cl client.Client, step string, want map[string]string) {
	t.Helper()
	for name, version := range want {
		got := mongosImageOrSts(t, cl, name)
		if got != mongodImage(version) {
			t.Errorf("%s: %s image = %q, want %q", step, name, got, mongodImage(version))
		}
	}
}

func mongosImageOrSts(t *testing.T, cl client.Client, name string) string {
	t.Helper()
	if name == rolloutMongosName {
		return mongosImage(t, cl)
	}
	return stsImage(t, cl, name)
}

func assertAbortClosed(t *testing.T, cl client.Client, reason string) {
	t.Helper()
	stored := getSharded(t, cl)

	if stored.Status.UpgradePhase != "" || stored.Status.UpgradeStartTime != nil {
		t.Errorf("phase=%q startTime=%v, want upgrade state cleared", stored.Status.UpgradePhase, stored.Status.UpgradeStartTime)
	}
	if stored.Status.EffectiveVersion != rolloutOldVersion || stored.Status.Version != rolloutOldVersion {
		t.Errorf("effectiveVersion=%q version=%q, want both %q",
			stored.Status.EffectiveVersion, stored.Status.Version, rolloutOldVersion)
	}
	if stored.Status.FCVVersion != fcvMajorMinor(rolloutOldVersion) {
		t.Errorf("FCVVersion = %q, want %q (abort must not touch FCV)", stored.Status.FCVVersion, fcvMajorMinor(rolloutOldVersion))
	}
	if !hasConditionReason(stored.Status.Conditions, abortConditionType, reason) {
		t.Errorf("no %s/%s condition: %+v", abortConditionType, reason, stored.Status.Conditions)
	}
}

// TestShardedUpgradeRevert_OldBinariesStay — 재현: cfg 만 9.0.8 로 간 뒤 spec 을 8.3.4 로
// 되돌린다. 버그: phase 만 지우고 EffectiveVersion 은 9.0.8 로 남아 다음 reconcile 이 shard ·
// mongos 를 게이트 없이 한꺼번에 9.0.8 로 올렸다. 실패로 닫힌 업그레이드(phase "")를 되돌려도 같다.
func TestShardedUpgradeRevert_OldBinariesStay(t *testing.T) {
	cases := []struct {
		name  string
		phase string
	}{
		{"reverted while Upgrading", UpgradePhaseUpgrading},
		{"reverted after terminal failure", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mdbsh := newRevertedSharded(tc.phase, abortStartedAgo)
			if tc.phase == "" {
				mdbsh.Status.UpgradeStartTime = nil
				mdbsh.Status.ObservedUpgradeGeneration = abortFailedUpgradeGen
			}
			probe := &rolloutProbe{}
			r, cl := newAbortReconciler(t, probe, mdbsh, rolloutCfgName)
			stillOld := map[string]string{
				rolloutShard0Name: rolloutOldVersion,
				rolloutShard1Name: rolloutOldVersion,
				rolloutMongosName: rolloutOldVersion,
			}

			// cfg 가 옛 버전으로 돌아가는 중(롤아웃 미완료) — 몇 번을 돌아도 나머지는 그대로.
			for i := 0; i < abortSteadyReconciles; i++ {
				reconcileRollout(t, r)
				assertImages(t, cl, "while config server returns", stillOld)
			}
			assertImages(t, cl, "config server returned", map[string]string{rolloutCfgName: rolloutOldVersion})
			if got := getSharded(t, cl).Status.UpgradePhase; got != UpgradePhaseRollingBack {
				t.Errorf("phase = %q while config server is rolling back, want %q", got, UpgradePhaseRollingBack)
			}

			finishRollout(t, cl, rolloutCfgName)
			reconcileRollout(t, r)

			assertImages(t, cl, "after abort", stillOld)
			assertAbortClosed(t, cl, abortReasonDone)
			if probe.secretGets != 0 {
				t.Errorf("abort read the admin secret %d time(s): FCV path entered", probe.secretGets)
			}
		})
	}
}

// TestShardedUpgradeRevert_ReturnsInReverseOrder — 전부 9.0.8 로 구른 뒤(Validating, FCV
// 미commit) 되돌리면 mongos → shards → cfg 순서로, 앞 컴포넌트가 옛 이미지로 다 구른 뒤에만
// 다음으로 넘어간다.
func TestShardedUpgradeRevert_ReturnsInReverseOrder(t *testing.T) {
	mdbsh := newRevertedSharded(UpgradePhaseValidating, abortValidationStarted)
	probe := &rolloutProbe{}
	r, cl := newAbortReconciler(t, probe, mdbsh,
		rolloutCfgName, rolloutShard0Name, rolloutShard1Name, rolloutMongosName)

	// 1) 중단 기록 — status 가 먼저, 워크로드는 아직 그대로.
	reconcileRollout(t, r)
	started := getSharded(t, cl)
	if started.Status.UpgradePhase != UpgradePhaseRollingBack || started.Status.EffectiveVersion != rolloutOldVersion {
		t.Fatalf("phase=%q effectiveVersion=%q, want %q and %q",
			started.Status.UpgradePhase, started.Status.EffectiveVersion, UpgradePhaseRollingBack, rolloutOldVersion)
	}
	if !hasConditionReason(started.Status.Conditions, abortConditionType, abortReasonInProgress) {
		t.Errorf("no %s/%s condition: %+v", abortConditionType, abortReasonInProgress, started.Status.Conditions)
	}

	// 2) mongos 만 옛 이미지. mongos 가 구르는 동안 shard · cfg 는 그대로.
	mongosOnly := map[string]string{
		rolloutMongosName: rolloutOldVersion,
		rolloutShard0Name: rolloutNewVersion,
		rolloutShard1Name: rolloutNewVersion,
		rolloutCfgName:    rolloutNewVersion,
	}
	for i := 0; i < abortSteadyReconciles; i++ {
		res := reconcileRollout(t, r)
		assertImages(t, cl, "while mongos returns", mongosOnly)
		if res.RequeueAfter <= 0 {
			t.Errorf("no requeue while mongos is rolling back: %+v", res)
		}
	}

	// 3) mongos 완료 → shard. cfg 는 그대로, shard 하나만 끝나도 그대로.
	finishMongosRollout(t, cl)
	reconcileRollout(t, r)
	shardsBack := map[string]string{
		rolloutShard0Name: rolloutOldVersion,
		rolloutShard1Name: rolloutOldVersion,
		rolloutCfgName:    rolloutNewVersion,
	}
	assertImages(t, cl, "after mongos returned", shardsBack)
	finishRollout(t, cl, rolloutShard0Name)
	reconcileRollout(t, r)
	assertImages(t, cl, "while shard-1 returns", shardsBack)

	// 4) 모든 shard 완료 → cfg.
	finishRollout(t, cl, rolloutShard1Name)
	reconcileRollout(t, r)
	assertImages(t, cl, "after shards returned", map[string]string{rolloutCfgName: rolloutOldVersion})
	if got := getSharded(t, cl).Status.UpgradePhase; got != UpgradePhaseRollingBack {
		t.Errorf("phase = %q while config server is rolling back, want %q", got, UpgradePhaseRollingBack)
	}

	// 5) cfg 완료 → 닫힘.
	finishRollout(t, cl, rolloutCfgName)
	reconcileRollout(t, r)
	assertAbortClosed(t, cl, abortReasonDone)
	if probe.secretGets != 0 {
		t.Errorf("abort read the admin secret %d time(s): FCV path entered", probe.secretGets)
	}
}

// TestShardedUpgradeRevert_BackingUpCancels — 백업 단계에서는 아무것도 구르지 않았다.
// 되돌리면 워크로드를 건드리지 않고 한 번에 닫는다.
func TestShardedUpgradeRevert_BackingUpCancels(t *testing.T) {
	mdbsh := newRevertedSharded(UpgradePhaseBackingUp, abortBackupStartedAgo)
	mdbsh.Spec.UpgradeStrategy = &mongodbv1alpha1.UpgradeStrategySpec{PreUpgradeBackup: true}
	r, cl := newAbortReconciler(t, &rolloutProbe{}, mdbsh)
	live := &liveFCVStub{err: errors.New("mongod unreachable")}
	r.fcvReader = live.read

	if _, handled, err := r.reconcileShardedUpgrade(context.Background(), mdbsh); err != nil || !handled {
		t.Fatalf("handled=%v err=%v, want the cancel handled", handled, err)
	}
	if live.reads != 0 {
		t.Errorf("cancel before rollout read the live FCV %d time(s), want none", live.reads)
	}

	assertAbortClosed(t, cl, abortReasonCancelled)
	for _, name := range []string{rolloutCfgName, rolloutShard0Name, rolloutShard1Name} {
		if got := getStatefulSet(t, cl, name).Generation; got != 1 {
			t.Errorf("%s generation = %d, want 1 (untouched)", name, got)
		}
	}
	if got := getDeployment(t, cl, rolloutMongosName).Generation; got != 1 {
		t.Errorf("mongos generation = %d, want 1 (untouched)", got)
	}
}

// TestShardedUpgradeRevert_FCVCommittedKeepsNewBinaries — 새 major 의 FCV 가 이미 commit 됐으면
// 바이너리를 내리지 않는다(옛 바이너리는 그 FCV 로 뜨지 못한다). 새 버전을 유지하고 이유를 남긴다.
func TestShardedUpgradeRevert_FCVCommittedKeepsNewBinaries(t *testing.T) {
	mdbsh := newRevertedSharded(UpgradePhaseValidating, abortValidationStarted)
	mdbsh.Status.FCVVersion = fcvMajorMinor(rolloutNewVersion)
	all := []string{rolloutCfgName, rolloutShard0Name, rolloutShard1Name, rolloutMongosName}
	r, cl := newAbortReconciler(t, &rolloutProbe{}, mdbsh, all...)

	for i := 0; i < abortSteadyReconciles; i++ {
		res := reconcileRollout(t, r)
		if res.RequeueAfter <= 0 {
			t.Errorf("no requeue while the revert is blocked: %+v", res)
		}
	}

	for _, name := range all {
		assertImages(t, cl, "revert blocked by FCV", map[string]string{name: rolloutNewVersion})
	}
	stored := getSharded(t, cl)
	if stored.Status.EffectiveVersion != rolloutNewVersion || stored.Status.FCVVersion != fcvMajorMinor(rolloutNewVersion) {
		t.Errorf("effectiveVersion=%q fcv=%q, want %q and %q kept",
			stored.Status.EffectiveVersion, stored.Status.FCVVersion, rolloutNewVersion, fcvMajorMinor(rolloutNewVersion))
	}
	if !hasCondition(stored.Status.Conditions, abortConditionType, metav1.ConditionFalse, abortReasonFCVBlocked) {
		t.Errorf("no %s=False/%s condition: %+v", abortConditionType, abortReasonFCVBlocked, stored.Status.Conditions)
	}
}

func hasCondition(conditions []metav1.Condition, condType string, status metav1.ConditionStatus, reason string) bool {
	for _, c := range conditions {
		if c.Type == condType && c.Status == status && c.Reason == reason {
			return true
		}
	}
	return false
}

// TestShardedUpgradeRevert_ResumesAfterRestart — 중단 도중 오퍼레이터가 재시작해도 CR status
// (phase RollingBack · EffectiveVersion = 옛 버전)와 라이브 워크로드만으로 이어 간다.
func TestShardedUpgradeRevert_ResumesAfterRestart(t *testing.T) {
	// mongos 는 돌아왔고 shard · cfg 는 아직 새 버전.
	r, cl := newAbortReconciler(t, &rolloutProbe{}, newAbortingSharded(),
		rolloutCfgName, rolloutShard0Name, rolloutShard1Name)

	reconcileRollout(t, r)

	assertImages(t, cl, "resumed abort", map[string]string{
		rolloutMongosName: rolloutOldVersion,
		rolloutShard0Name: rolloutOldVersion,
		rolloutShard1Name: rolloutOldVersion,
		rolloutCfgName:    rolloutNewVersion,
	})
	if got := getSharded(t, cl).Status.UpgradePhase; got != UpgradePhaseRollingBack {
		t.Errorf("phase = %q mid-abort, want %q", got, UpgradePhaseRollingBack)
	}
}

// TestShardedUpgradeRevert_ReappliedSpecWaitsForAbort — 중단 도중 spec 을 다시 새 버전으로
// 올려도 중단을 순서대로 끝낸 뒤에야 새 업그레이드를 감지한다. 버그: 실패로 닫히면서 shard 와
// cfg 가 한꺼번에 옛 버전으로 굴렀다.
func TestShardedUpgradeRevert_ReappliedSpecWaitsForAbort(t *testing.T) {
	mdbsh := newAbortingSharded()
	mdbsh.Spec.Version.Version = rolloutNewVersion
	r, cl := newAbortReconciler(t, &rolloutProbe{}, mdbsh,
		rolloutCfgName, rolloutShard0Name, rolloutShard1Name)

	reconcileRollout(t, r)

	assertImages(t, cl, "abort continues", map[string]string{
		rolloutShard0Name: rolloutOldVersion,
		rolloutShard1Name: rolloutOldVersion,
		rolloutCfgName:    rolloutNewVersion,
	})

	finishRollout(t, cl, rolloutShard0Name)
	finishRollout(t, cl, rolloutShard1Name)
	reconcileRollout(t, r)
	finishRollout(t, cl, rolloutCfgName)
	reconcileRollout(t, r)
	if got := getSharded(t, cl).Status.UpgradePhase; got != "" {
		t.Fatalf("phase = %q after every component returned, want abort closed", got)
	}

	// 닫힌 뒤에는 평소대로 업그레이드를 감지한다.
	reconcileRollout(t, r)
	restarted := getSharded(t, cl)
	if restarted.Status.UpgradePhase != UpgradePhaseUpgrading || restarted.Status.EffectiveVersion != rolloutNewVersion {
		t.Errorf("phase=%q effectiveVersion=%q, want a fresh upgrade to %q",
			restarted.Status.UpgradePhase, restarted.Status.EffectiveVersion, rolloutNewVersion)
	}
}

// TestShardedUpgradeRevert_LiveFCVGuardsStart — setFCV 는 성공했는데 status 기록이 실패했으면
// status 는 옛 FCV 를 말한다. 중단은 status 가 아니라 라이브 FCV 로 판정한다: 옛 FCV 로
// 확정됐을 때만 내리고, 새 FCV 면 막고 status 를 고치고, 모르면 멈춘다.
func TestShardedUpgradeRevert_LiveFCVGuardsStart(t *testing.T) {
	oldFCV := fcvMajorMinor(rolloutOldVersion)
	newFCV := fcvMajorMinor(rolloutNewVersion)
	cases := []struct {
		name       string
		live       liveFCVStub
		wantReason string
		wantFCV    string
	}{
		{"new FCV committed, status still old", liveFCVStub{states: fcvOnAllMembers(newFCV)}, abortReasonFCVBlocked, newFCV},
		{"read fails", liveFCVStub{err: errors.New("mongod unreachable")}, abortReasonFCVUnknown, oldFCV},
		{"setFCV in transition", liveFCVStub{states: []mongodb.FCVState{
			{Version: oldFCV}, {Version: oldFCV, TargetVersion: newFCV}, {Version: oldFCV},
		}}, abortReasonFCVUnknown, oldFCV},
		{"members disagree", liveFCVStub{states: []mongodb.FCVState{
			{Version: newFCV}, {Version: oldFCV}, {Version: oldFCV},
		}}, abortReasonFCVUnknown, oldFCV},
		{"no member reported", liveFCVStub{}, abortReasonFCVUnknown, oldFCV},
		{"unrelated FCV", liveFCVStub{states: fcvOnAllMembers("8.0")}, abortReasonFCVUnknown, oldFCV},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mdbsh := newRevertedSharded(UpgradePhaseValidating, abortValidationStarted)
			all := []string{rolloutCfgName, rolloutShard0Name, rolloutShard1Name, rolloutMongosName}
			r, cl := newAbortReconciler(t, &rolloutProbe{}, mdbsh, all...)
			live := tc.live
			r.fcvReader = live.read

			for i := 0; i < abortSteadyReconciles; i++ {
				if res := reconcileRollout(t, r); res.RequeueAfter <= 0 {
					t.Errorf("no requeue while the abort is held: %+v", res)
				}
			}

			for _, name := range all {
				assertImages(t, cl, "abort held on live FCV", map[string]string{name: rolloutNewVersion})
			}
			stored := getSharded(t, cl)
			if stored.Status.EffectiveVersion != rolloutNewVersion {
				t.Errorf("effectiveVersion = %q, want %q kept", stored.Status.EffectiveVersion, rolloutNewVersion)
			}
			if stored.Status.FCVVersion != tc.wantFCV {
				t.Errorf("status.fcvVersion = %q, want %q", stored.Status.FCVVersion, tc.wantFCV)
			}
			if !hasCondition(stored.Status.Conditions, abortConditionType, metav1.ConditionFalse, tc.wantReason) {
				t.Errorf("no %s=False/%s condition: %+v", abortConditionType, tc.wantReason, stored.Status.Conditions)
			}
		})
	}
}

// TestShardedUpgradeRevert_LiveFCVReadEveryStep — 중단이 끝날 때까지 reconcile 마다 라이브
// FCV 를 다시 읽는다. 도중에 확정이 깨지면 다음 컴포넌트로 가지 않고, 풀리면 이어 간다.
func TestShardedUpgradeRevert_LiveFCVReadEveryStep(t *testing.T) {
	r, cl := newAbortReconciler(t, &rolloutProbe{}, newAbortingSharded(),
		rolloutCfgName, rolloutShard0Name, rolloutShard1Name)
	live := &liveFCVStub{err: errors.New("mongod unreachable")}
	r.fcvReader = live.read
	shardsStillNew := map[string]string{
		rolloutShard0Name: rolloutNewVersion,
		rolloutShard1Name: rolloutNewVersion,
		rolloutCfgName:    rolloutNewVersion,
	}

	for i := 0; i < abortSteadyReconciles; i++ {
		reconcileRollout(t, r)
	}
	assertImages(t, cl, "FCV unreadable mid-abort", shardsStillNew)
	if live.reads != abortSteadyReconciles {
		t.Errorf("live FCV read %d time(s) over %d reconciles", live.reads, abortSteadyReconciles)
	}
	if !hasCondition(getSharded(t, cl).Status.Conditions, abortConditionType, metav1.ConditionFalse, abortReasonFCVUnknown) {
		t.Errorf("no %s=False/%s condition mid-abort", abortConditionType, abortReasonFCVUnknown)
	}

	// 누가 도중에 FCV 를 올렸다 — 더 내리지 않고 status 를 고친다.
	live.err = nil
	live.states = fcvOnAllMembers(fcvMajorMinor(rolloutNewVersion))
	reconcileRollout(t, r)
	assertImages(t, cl, "FCV committed mid-abort", shardsStillNew)
	blocked := getSharded(t, cl)
	if blocked.Status.FCVVersion != fcvMajorMinor(rolloutNewVersion) ||
		!hasCondition(blocked.Status.Conditions, abortConditionType, metav1.ConditionFalse, abortReasonFCVBlocked) {
		t.Errorf("fcvVersion=%q conditions=%+v, want repaired FCV and %s",
			blocked.Status.FCVVersion, blocked.Status.Conditions, abortReasonFCVBlocked)
	}
}

// TestShardedUpgradeRevert_LiveFCVConfirmedResumes — 읽기가 돌아와 옛 FCV 로 확정되면 멈췄던
// 중단이 이어 간다.
func TestShardedUpgradeRevert_LiveFCVConfirmedResumes(t *testing.T) {
	r, cl := newAbortReconciler(t, &rolloutProbe{}, newAbortingSharded(),
		rolloutCfgName, rolloutShard0Name, rolloutShard1Name)
	live := &liveFCVStub{err: errors.New("mongod unreachable")}
	r.fcvReader = live.read
	reconcileRollout(t, r)

	live.err = nil
	live.states = fcvOnAllMembers(fcvMajorMinor(rolloutOldVersion))
	reconcileRollout(t, r)

	assertImages(t, cl, "abort resumed", map[string]string{
		rolloutShard0Name: rolloutOldVersion,
		rolloutShard1Name: rolloutOldVersion,
		rolloutCfgName:    rolloutNewVersion,
	})
	if !hasConditionReason(getSharded(t, cl).Status.Conditions, abortConditionType, abortReasonInProgress) {
		t.Errorf("condition not back to %s after the FCV was confirmed", abortReasonInProgress)
	}
}

// TestShardedUpgradeRevert_CommittedMidAbortRollsForward — 중단 도중 FCV 가 올라간 것으로
// 읽히면 멈춘다. spec.version 을 그 FCV 의 버전으로 돌려놓으면 중단을 버리고 평소 순서 게이트로
// 다시 올라간다(cfg 먼저, 내려갔던 mongos 는 shard 뒤).
func TestShardedUpgradeRevert_CommittedMidAbortRollsForward(t *testing.T) {
	mdbsh := newAbortingSharded()
	mdbsh.Spec.Version.Version = rolloutNewVersion
	r, cl := newAbortReconciler(t, &rolloutProbe{}, mdbsh, rolloutCfgName, rolloutShard0Name, rolloutShard1Name)
	live := &liveFCVStub{states: fcvOnAllMembers(fcvMajorMinor(rolloutNewVersion))}
	r.fcvReader = live.read

	reconcileRollout(t, r)

	forward := getSharded(t, cl)
	if forward.Status.UpgradePhase != UpgradePhaseUpgrading || forward.Status.EffectiveVersion != rolloutNewVersion ||
		forward.Status.FCVVersion != fcvMajorMinor(rolloutNewVersion) || forward.Status.UpgradeStartTime == nil {
		t.Fatalf("phase=%q effectiveVersion=%q fcv=%q startTime=%v, want a forward upgrade to %q",
			forward.Status.UpgradePhase, forward.Status.EffectiveVersion, forward.Status.FCVVersion,
			forward.Status.UpgradeStartTime, rolloutNewVersion)
	}
	assertImages(t, cl, "before the forward roll", map[string]string{rolloutMongosName: rolloutOldVersion})
}

// TestShardedUpgradeRevert_DefaultReaderFailsClosed — reader 를 끼우지 않으면 mongod 에 직접
// 읽는다. 읽을 수 없으면(여기서는 admin secret 부재) 내리지 않고 멈춘다.
func TestShardedUpgradeRevert_DefaultReaderFailsClosed(t *testing.T) {
	mdbsh := newRevertedSharded(UpgradePhaseValidating, abortValidationStarted)
	all := []string{rolloutCfgName, rolloutShard0Name, rolloutShard1Name, rolloutMongosName}
	r, cl := newAbortReconciler(t, &rolloutProbe{}, mdbsh, all...)
	r.fcvReader = nil

	reconcileRollout(t, r)

	for _, name := range all {
		assertImages(t, cl, "live FCV unreadable", map[string]string{name: rolloutNewVersion})
	}
	if !hasCondition(getSharded(t, cl).Status.Conditions, abortConditionType, metav1.ConditionFalse, abortReasonFCVUnknown) {
		t.Errorf("no %s=False/%s condition", abortConditionType, abortReasonFCVUnknown)
	}
}
