/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

// sharded_upgrade_gate_unit_test.go — 롤아웃 게이트가 뒤처진 읽기 · 옛 이미지 · 잉여 shard ·
// 실패한 업그레이드에서도 닫혀 있는지 가드한다.
//
// laggingClient 는 informer cache 를 모사한다: 워크로드를 쓰면 그 직전 객체를 붙잡아 두고
// Get · List 에 그 옛 객체를 돌려준다(읽기가 쓰기 하나만큼 뒤처짐).
// StatefulSet · Deployment 컨트롤러는 여전히 없다 — 롤아웃 status 는 시험이 쓴다.
package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	rolloutCustomOldImage = "registry.example/library/mongo:8.3.4"
	rolloutCustomNewImage = "registry.example/library/mongo:9.0.8"
)

type laggingClient struct {
	client.Client
	stale map[string]client.Object
}

func newLaggingClient(inner client.Client) *laggingClient {
	return &laggingClient{Client: inner, stale: map[string]client.Object{}}
}

// serveStale 은 cache 가 obj 를 아직 들고 있는 상태를 직접 만든다.
func (c *laggingClient) serveStale(obj client.Object) {
	c.stale[obj.GetName()] = obj
}

func (c *laggingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if !isWorkload(obj) {
		return c.Client.Update(ctx, obj, opts...)
	}
	before := obj.DeepCopyObject().(client.Object)
	if err := c.Client.Get(ctx, client.ObjectKeyFromObject(obj), before); err != nil {
		return err
	}
	if err := c.Client.Update(ctx, obj, opts...); err != nil {
		return err
	}
	c.stale[obj.GetName()] = before
	return nil
}

func (c *laggingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	stale, ok := c.stale[key.Name]
	if !ok || !isWorkload(obj) {
		return c.Client.Get(ctx, key, obj, opts...)
	}
	switch out := obj.(type) {
	case *appsv1.StatefulSet:
		stale.(*appsv1.StatefulSet).DeepCopyInto(out)
	case *appsv1.Deployment:
		stale.(*appsv1.Deployment).DeepCopyInto(out)
	}
	return nil
}

func (c *laggingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if err := c.Client.List(ctx, list, opts...); err != nil {
		return err
	}
	stsList, ok := list.(*appsv1.StatefulSetList)
	if !ok {
		return nil
	}
	for i := range stsList.Items {
		if stale, ok := c.stale[stsList.Items[i].Name]; ok {
			stale.(*appsv1.StatefulSet).DeepCopyInto(&stsList.Items[i])
		}
	}
	return nil
}

func assertOnlyConfigServerMoved(t *testing.T, cl client.Client) {
	t.Helper()
	oldImage := mongodImage(rolloutOldVersion)

	if got := stsImage(t, cl, rolloutCfgName); got != mongodImage(rolloutNewVersion) {
		t.Fatalf("cfg image = %q, want %q", got, mongodImage(rolloutNewVersion))
	}
	if got := stsImage(t, cl, rolloutShard0Name); got != oldImage {
		t.Errorf("shard-0 updated before config server finished rolling: image = %q", got)
	}
	if got := mongosImage(t, cl); got != oldImage {
		t.Errorf("mongos updated before config server finished rolling: image = %q", got)
	}
	if got := getSharded(t, cl).Status.UpgradePhase; got != UpgradePhaseUpgrading {
		t.Errorf("phase = %q while config server is rolling, want %q", got, UpgradePhaseUpgrading)
	}
}

// TestShardedUpgrade_OrderHoldsBehindStaleCache — cache(Client)가 쓰기 하나만큼 뒤처져
// apply 직전 객체(옛 generation · 완료된 status)를 돌려줘도, 게이트는 APIReader 로 읽어
// cfg 가 구르는 동안 shard · mongos 를 건드리지 않는다.
func TestShardedUpgrade_OrderHoldsBehindStaleCache(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newRolloutSharded(UpgradePhaseUpgrading, time.Second)
	objs := append(rolloutWorkloads(mdbsh, rolloutOldVersion, rolloutOldRevision), mdbsh)
	live := newRolloutClient(t, s, &rolloutProbe{}, objs...)
	r := &MongoDBShardedReconciler{Client: newLaggingClient(live), APIReader: live, Scheme: s}

	reconcileRollout(t, r)

	assertOnlyConfigServerMoved(t, live)
}

// TestShardedUpgrade_StaleReaderFailsClosed — 게이트가 읽는 곳마저 뒤처져도(옛 객체 =
// 옛 이미지) 목표 이미지가 아니라서 미완료로 본다.
func TestShardedUpgrade_StaleReaderFailsClosed(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newRolloutSharded(UpgradePhaseUpgrading, time.Second)
	objs := append(rolloutWorkloads(mdbsh, rolloutOldVersion, rolloutOldRevision), mdbsh)
	live := newRolloutClient(t, s, &rolloutProbe{}, objs...)
	lagging := newLaggingClient(live)
	r := &MongoDBShardedReconciler{Client: lagging, APIReader: lagging, Scheme: s}

	reconcileRollout(t, r)

	assertOnlyConfigServerMoved(t, live)
}

// TestShardedUpgradeValidation_OldImageBlocksFCV — status 가 "다 굴렀다" 여도 pod template
// 이미지가 목표 버전의 것이 아니면 FCV commit 에 들어가지 않는다.
func TestShardedUpgradeValidation_OldImageBlocksFCV(t *testing.T) {
	for _, name := range []string{rolloutCfgName, rolloutShard0Name, rolloutShard1Name, rolloutMongosName} {
		t.Run(name, func(t *testing.T) {
			s := newTestScheme(t)
			mdbsh := newRolloutSharded(UpgradePhaseValidating, 2*time.Minute)
			objs := append(rolloutWorkloads(mdbsh, rolloutNewVersion, rolloutNewRevision), mdbsh)
			probe := &rolloutProbe{}
			live := newRolloutClient(t, s, probe, objs...)

			// 읽는 쪽은 업그레이드 전 객체(옛 이미지 · 완료 status)를 아직 들고 있다.
			lagging := newLaggingClient(live)
			for _, old := range rolloutWorkloads(mdbsh, rolloutOldVersion, rolloutOldRevision) {
				if old.GetName() == name {
					lagging.serveStale(old)
				}
			}
			r := &MongoDBShardedReconciler{Client: lagging, APIReader: lagging, Scheme: s}

			if _, _, err := r.reconcileShardedUpgradeValidation(context.Background(), mdbsh, rolloutNewVersion); err != nil {
				t.Fatalf("validation: %v", err)
			}

			if probe.secretGets != 0 {
				t.Errorf("FCV commit entered with %s on the old image: admin secret read %d time(s)", name, probe.secretGets)
			}
			if got := getSharded(t, live).Status.Version; got != rolloutOldVersion {
				t.Errorf("Status.Version = %q, want %q", got, rolloutOldVersion)
			}
		})
	}
}

// TestShardedUpgradeValidation_CustomImage — spec.version.image 가 있으면 이미지는 version
// 을 따라가지 않는다. 이미지 태그가 목표 버전을 선언할 때만 FCV 를 commit 한다.
func TestShardedUpgradeValidation_CustomImage(t *testing.T) {
	cases := []struct {
		name       string
		image      string
		wantCommit bool
	}{
		{"only version bumped, image still old", rolloutCustomOldImage, false},
		{"digest-pinned image, version unknown", "registry.example/library/mongo@sha256:0123", false},
		{"image tag is the target version", rolloutCustomNewImage, true},
		{"image tag is the target version with suffix", rolloutCustomNewImage + "-ubi9", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestScheme(t)
			mdbsh := newRolloutSharded(UpgradePhaseValidating, 2*time.Minute)
			mdbsh.Spec.Version.Image = tc.image
			objs := append(rolloutWorkloads(mdbsh, rolloutNewVersion, rolloutNewRevision), mdbsh)
			probe := &rolloutProbe{}
			cl := newRolloutClient(t, s, probe, objs...)
			r := &MongoDBShardedReconciler{Client: cl, APIReader: cl, Scheme: s}

			if _, _, err := r.reconcileShardedUpgradeValidation(context.Background(), mdbsh, rolloutNewVersion); err != nil {
				t.Fatalf("validation: %v", err)
			}

			if committed := probe.secretGets > 0; committed != tc.wantCommit {
				t.Fatalf("FCV commit entered = %v, want %v", committed, tc.wantCommit)
			}
			if tc.wantCommit {
				return
			}

			stored := getSharded(t, cl)
			if stored.Status.FCVVersion != fcvMajorMinor(rolloutOldVersion) {
				t.Errorf("FCVVersion = %q, want %q (not committed)", stored.Status.FCVVersion, fcvMajorMinor(rolloutOldVersion))
			}
			if stored.Status.UpgradePhase != "" || stored.Status.Version != rolloutNewVersion {
				t.Errorf("phase=%q version=%q, want upgrade closed at %q without FCV",
					stored.Status.UpgradePhase, stored.Status.Version, rolloutNewVersion)
			}
			if !hasConditionReason(stored.Status.Conditions, "UpgradeComplete", upgradeReasonFCVNotCommitted) {
				t.Errorf("no UpgradeComplete/%s condition: %+v", upgradeReasonFCVNotCommitted, stored.Status.Conditions)
			}
		})
	}
}

func hasConditionReason(conditions []metav1.Condition, condType, reason string) bool {
	for _, c := range conditions {
		if c.Type == condType && c.Reason == reason {
			return true
		}
	}
	return false
}

// TestShardedUpgradeBackup_RestartsRolloutClock — 롤아웃 예산은 Upgrading 이 시작할 때부터
// 잰다. 백업에 쓴 시간이 예산을 먹으면 첫 Upgrading reconcile 이 곧장 롤백으로 간다.
func TestShardedUpgradeBackup_RestartsRolloutClock(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newRolloutSharded(UpgradePhaseBackingUp, 24*time.Hour)
	mdbsh.Spec.UpgradeStrategy = &mongodbv1alpha1.UpgradeStrategySpec{PreUpgradeBackup: true, RollbackOnFailure: true}
	backup := &mongodbv1alpha1.MongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{Name: rolloutTestName + "-pre-upgrade-" + rolloutNewVersion, Namespace: rolloutTestNS},
		Status:     mongodbv1alpha1.MongoDBBackupStatus{Phase: "Completed"},
	}
	objs := append(rolloutWorkloads(mdbsh, rolloutOldVersion, rolloutOldRevision), mdbsh, backup)
	cl := newRolloutClient(t, s, &rolloutProbe{}, objs...)
	r := &MongoDBShardedReconciler{Client: cl, APIReader: cl, Scheme: s}

	reconcileRollout(t, r)
	stored := getSharded(t, cl)
	if stored.Status.UpgradePhase != UpgradePhaseUpgrading {
		t.Fatalf("phase = %q after backup completed, want %q", stored.Status.UpgradePhase, UpgradePhaseUpgrading)
	}
	if stored.Status.UpgradeStartTime == nil || time.Since(stored.Status.UpgradeStartTime.Time) > time.Minute {
		t.Errorf("UpgradeStartTime = %v, want restarted when Upgrading began", stored.Status.UpgradeStartTime)
	}

	reconcileRollout(t, r)
	if got := getSharded(t, cl).Status.UpgradePhase; got != UpgradePhaseUpgrading {
		t.Errorf("phase = %q on the first Upgrading reconcile after a 24h backup, want %q", got, UpgradePhaseUpgrading)
	}
}

// TestShardedUpgradeValidation_SurplusShardBlocksFCV — 업그레이드 도중 shards.count 를
// 내려도, 아직 남아 있는 옛 바이너리 shard StatefulSet 이 게이트 밖으로 빠지지 않는다.
func TestShardedUpgradeValidation_SurplusShardBlocksFCV(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newRolloutSharded(UpgradePhaseValidating, 2*time.Minute)
	workloads := rolloutWorkloads(mdbsh, rolloutNewVersion, rolloutNewRevision)
	for i, old := range rolloutWorkloads(mdbsh, rolloutOldVersion, rolloutOldRevision) {
		if old.GetName() == rolloutShard1Name {
			workloads[i] = old
		}
	}
	mdbsh.Spec.Shards.Count = 1
	probe := &rolloutProbe{}
	cl := newRolloutClient(t, s, probe, append(workloads, mdbsh)...)
	r := &MongoDBShardedReconciler{Client: cl, APIReader: cl, Scheme: s}

	if _, _, err := r.reconcileShardedUpgradeValidation(context.Background(), mdbsh, rolloutNewVersion); err != nil {
		t.Fatalf("validation: %v", err)
	}

	if probe.secretGets != 0 {
		t.Errorf("FCV commit entered with surplus shard-1 on the old binary: admin secret read %d time(s)", probe.secretGets)
	}
}

// TestShardedUpgrade_FailedUpgradeHoldsComponents — 롤백 없이 실패한 업그레이드는 아직 옛
// 바이너리인 컴포넌트를 그대로 둔다. spec 이 바뀌면(generation) 다시 순서대로 진행한다.
func TestShardedUpgrade_FailedUpgradeHoldsComponents(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newRolloutSharded(UpgradePhaseUpgrading, 24*time.Hour)
	mdbsh.Generation = 1
	objs := append(rolloutWorkloads(mdbsh, rolloutOldVersion, rolloutOldRevision), mdbsh)
	cl := newRolloutClient(t, s, &rolloutProbe{}, objs...)
	r := &MongoDBShardedReconciler{Client: cl, APIReader: cl, Scheme: s}
	oldImage := mongodImage(rolloutOldVersion)

	// cfg 가 예산 안에 못 굴러 terminal 실패.
	reconcileRollout(t, r)
	failed := getSharded(t, cl)
	if failed.Status.UpgradePhase != "" || !hasConditionReason(failed.Status.Conditions, "UpgradeFailed", "ValidationTimeout") {
		t.Fatalf("phase=%q conditions=%+v, want terminal UpgradeFailed", failed.Status.UpgradePhase, failed.Status.Conditions)
	}

	// 실패 뒤 reconcile 이 몇 번을 돌아도, cfg 가 뒤늦게 다 굴러도 shard · mongos 는 그대로.
	// (초기화 단계는 mongod 가 없어 에러로 끝난다 — 워크로드 적용은 그 앞에서 끝난다.)
	reconcileSteady := func() {
		_, _ = r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{Name: rolloutTestName, Namespace: rolloutTestNS},
		})
	}
	reconcileSteady()
	finishRollout(t, cl, rolloutCfgName)
	reconcileSteady()
	reconcileSteady()
	for _, name := range []string{rolloutShard0Name, rolloutShard1Name} {
		if got := stsImage(t, cl, name); got != oldImage {
			t.Errorf("%s moved to the new version after the upgrade failed: image = %q", name, got)
		}
	}
	if got := mongosImage(t, cl); got != oldImage {
		t.Errorf("mongos moved to the new version after the upgrade failed: image = %q", got)
	}

	// spec 변경 = 재개. 다시 Upgrading 으로 들어가 순서대로 shard 를 갱신한다.
	resumed := getSharded(t, cl)
	resumed.Generation = 2
	if err := cl.Update(context.Background(), resumed); err != nil {
		t.Fatalf("bump generation: %v", err)
	}
	reconcileRollout(t, r)
	if got := getSharded(t, cl).Status.UpgradePhase; got != UpgradePhaseUpgrading {
		t.Fatalf("phase = %q after spec change, want %q", got, UpgradePhaseUpgrading)
	}
	reconcileRollout(t, r)
	if got := stsImage(t, cl, rolloutShard0Name); got != mongodImage(rolloutNewVersion) {
		t.Errorf("shard-0 image = %q after resume, want %q", got, mongodImage(rolloutNewVersion))
	}
	if got := mongosImage(t, cl); got != oldImage {
		t.Errorf("mongos updated before shards finished rolling after resume: image = %q", got)
	}
}

// TestShardedUpgrade_MissingStartTimeGetsClock — Upgrading 인데 시작 시각이 없으면 영구
// 재대기가 아니라 시각을 채워 예산이 흐르게 한다.
func TestShardedUpgrade_MissingStartTimeGetsClock(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newRolloutSharded(UpgradePhaseUpgrading, time.Second)
	mdbsh.Status.UpgradeStartTime = nil
	objs := append(rolloutWorkloads(mdbsh, rolloutOldVersion, rolloutOldRevision), mdbsh)
	cl := newRolloutClient(t, s, &rolloutProbe{}, objs...)
	r := &MongoDBShardedReconciler{Client: cl, APIReader: cl, Scheme: s}

	reconcileRollout(t, r)

	if getSharded(t, cl).Status.UpgradeStartTime == nil {
		t.Error("UpgradeStartTime still nil while Upgrading waits for a rollout — budget never runs")
	}
}
