/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

// sharded_upgrade_rollout_unit_test.go — sharded 업그레이드가 롤아웃 완료 전에 FCV 를
// commit 하지 않고, cfg → shards → mongos 순서를 지키는지 가드한다.
//
// fake client 에는 StatefulSet · Deployment 컨트롤러가 없다. 롤아웃 상태는 시험이 직접 쓴다:
//   - newRolloutClient 의 Update interceptor 가 spec 이 바뀐 워크로드의 metadata.generation 을
//     올린다(API 서버 모사). status.observedGeneration 은 그대로 남아 "컨트롤러가 아직
//     새 spec 을 못 봤다" 가 된다.
//   - finishRollout 이 "모든 pod 가 새 revision 으로 ready" 인 status 를 쓴다(컨트롤러 모사).
package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

const (
	rolloutTestName     = "sh"
	rolloutTestNS       = "default"
	rolloutOldVersion   = "8.3.4"
	rolloutNewVersion   = "9.0.8"
	rolloutOldRevision  = "rev-old"
	rolloutNewRevision  = "rev-new"
	rolloutAdminSecret  = "sh-admin"
	rolloutCfgName      = rolloutTestName + "-cfg"
	rolloutShard0Name   = rolloutTestName + "-shard-0"
	rolloutShard1Name   = rolloutTestName + "-shard-1"
	rolloutMongosName   = rolloutTestName + "-mongos"
	rolloutCfgMembers   = int32(3)
	rolloutShardMembers = int32(3)
	rolloutMongos       = int32(2)
)

// newRolloutSharded 는 8.3.4 → 9.0.8 업그레이드 도중의 CR 을 만든다. Status 의 Ready
// 수는 업그레이드 전 값(전부 ready)으로 굳어 있다 — Upgrading · Validating 은
// updateStatus 에 닿지 않는다.
func newRolloutSharded(phase string, started time.Duration) *mongodbv1alpha1.MongoDBSharded {
	mdbsh := newAdoptedSharded(rolloutTestName, rolloutNewVersion)
	mdbsh.Finalizers = []string{mongodbShardedFinalizer}
	mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name = rolloutAdminSecret

	start := metav1.NewTime(time.Now().Add(-started))
	mdbsh.Status = mongodbv1alpha1.MongoDBShardedStatus{
		Phase:            mongodbv1alpha1.ShardedPhaseRunning,
		Version:          rolloutOldVersion,
		PreviousVersion:  rolloutOldVersion,
		EffectiveVersion: rolloutNewVersion,
		FCVVersion:       fcvMajorMinor(rolloutOldVersion),
		UpgradePhase:     phase,
		UpgradeStartTime: &start,
		ConfigServer:     mongodbv1alpha1.ComponentStatus{Ready: rolloutCfgMembers, Total: rolloutCfgMembers},
		Mongos:           mongodbv1alpha1.ComponentStatus{Ready: rolloutMongos, Total: rolloutMongos},
		Shards: []mongodbv1alpha1.ShardStatus{
			{Name: rolloutShard0Name, Ready: rolloutShardMembers, Total: rolloutShardMembers},
			{Name: rolloutShard1Name, Ready: rolloutShardMembers, Total: rolloutShardMembers},
		},
	}
	return mdbsh
}

// rolledStatefulSet 은 주어진 버전 이미지로 롤아웃이 끝난 StatefulSet 을 만든다.
func rolledStatefulSet(sts *appsv1.StatefulSet, revision string) *appsv1.StatefulSet {
	sts.Generation = 1
	sts.Status = appsv1.StatefulSetStatus{
		ObservedGeneration: 1,
		Replicas:           *sts.Spec.Replicas,
		ReadyReplicas:      *sts.Spec.Replicas,
		UpdatedReplicas:    *sts.Spec.Replicas,
		CurrentRevision:    revision,
		UpdateRevision:     revision,
	}
	return sts
}

func rolledDeployment(dep *appsv1.Deployment) *appsv1.Deployment {
	dep.Generation = 1
	dep.Status = appsv1.DeploymentStatus{
		ObservedGeneration: 1,
		Replicas:           *dep.Spec.Replicas,
		ReadyReplicas:      *dep.Spec.Replicas,
		UpdatedReplicas:    *dep.Spec.Replicas,
		AvailableReplicas:  *dep.Spec.Replicas,
	}
	return dep
}

// rolloutWorkloads 는 version 으로 롤아웃이 끝난 cfg · shard 2개 · mongos 를 돌려준다.
func rolloutWorkloads(mdbsh *mongodbv1alpha1.MongoDBSharded, version, revision string) []client.Object {
	built := mdbsh.DeepCopy()
	built.Status.EffectiveVersion = version

	return []client.Object{
		rolledStatefulSet(resources.BuildConfigServerStatefulSet(built), revision),
		rolledStatefulSet(resources.BuildShardStatefulSet(built, 0), revision),
		rolledStatefulSet(resources.BuildShardStatefulSet(built, 1), revision),
		rolledDeployment(resources.BuildMongosDeployment(built)),
	}
}

type rolloutProbe struct {
	secretGets int
	failGet    string
}

func workloadSpecChanged(existing, updated client.Object) bool {
	switch old := existing.(type) {
	case *appsv1.StatefulSet:
		return !apiequality.Semantic.DeepEqual(old.Spec, updated.(*appsv1.StatefulSet).Spec)
	case *appsv1.Deployment:
		return !apiequality.Semantic.DeepEqual(old.Spec, updated.(*appsv1.Deployment).Spec)
	}
	return false
}

func isWorkload(obj client.Object) bool {
	switch obj.(type) {
	case *appsv1.StatefulSet, *appsv1.Deployment:
		return true
	}
	return false
}

// newRolloutClient 는 API 서버의 generation 올림을 모사하고, admin Secret 읽기(=FCV commit
// 진입의 첫 I/O)를 센다.
func newRolloutClient(t *testing.T, s *runtime.Scheme, probe *rolloutProbe, objs ...client.Object) client.Client {
	t.Helper()

	funcs := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Secret); ok && key.Name == rolloutAdminSecret {
				probe.secretGets++
			}
			if probe.failGet != "" && key.Name == probe.failGet && isWorkload(obj) {
				return errors.New("injected apiserver error")
			}
			return c.Get(ctx, key, obj, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if !isWorkload(obj) {
				return c.Update(ctx, obj, opts...)
			}
			existing := obj.DeepCopyObject().(client.Object)
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), existing); err != nil {
				return err
			}
			if workloadSpecChanged(existing, obj) {
				obj.SetGeneration(existing.GetGeneration() + 1)
			}
			return c.Update(ctx, obj, opts...)
		},
	}

	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&mongodbv1alpha1.MongoDBSharded{}).
		WithInterceptorFuncs(funcs).
		Build()
}

func getStatefulSet(t *testing.T, cl client.Client, name string) *appsv1.StatefulSet {
	t.Helper()
	sts := &appsv1.StatefulSet{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: rolloutTestNS}, sts); err != nil {
		t.Fatalf("get statefulset %s: %v", name, err)
	}
	return sts
}

func getDeployment(t *testing.T, cl client.Client, name string) *appsv1.Deployment {
	t.Helper()
	dep := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: rolloutTestNS}, dep); err != nil {
		t.Fatalf("get deployment %s: %v", name, err)
	}
	return dep
}

func getSharded(t *testing.T, cl client.Client) *mongodbv1alpha1.MongoDBSharded {
	t.Helper()
	mdbsh := &mongodbv1alpha1.MongoDBSharded{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: rolloutTestName, Namespace: rolloutTestNS}, mdbsh); err != nil {
		t.Fatalf("get sharded: %v", err)
	}
	return mdbsh
}

// finishRollout 은 StatefulSet 컨트롤러가 롤링을 끝낸 상태를 쓴다.
func finishRollout(t *testing.T, cl client.Client, name string) {
	t.Helper()
	sts := getStatefulSet(t, cl, name)
	sts.Status.ObservedGeneration = sts.Generation
	sts.Status.UpdatedReplicas = *sts.Spec.Replicas
	sts.Status.ReadyReplicas = *sts.Spec.Replicas
	sts.Status.CurrentRevision = rolloutNewRevision
	sts.Status.UpdateRevision = rolloutNewRevision
	if err := cl.Status().Update(context.Background(), sts); err != nil {
		t.Fatalf("finish rollout %s: %v", name, err)
	}
}

// writeWorkload 는 metadata(generation)와 status 를 둘 다 쓴다 — fake client 는 apps/v1 의
// status 를 subresource 로 다뤄 Update 가 status 를 버린다.
func writeWorkload(t *testing.T, cl client.Client, obj client.Object) {
	t.Helper()
	status := obj.DeepCopyObject().(client.Object)
	if err := cl.Update(context.Background(), obj); err != nil {
		t.Fatalf("update %s: %v", obj.GetName(), err)
	}
	status.SetResourceVersion(obj.GetResourceVersion())
	if err := cl.Status().Update(context.Background(), status); err != nil {
		t.Fatalf("update status %s: %v", obj.GetName(), err)
	}
}

func mongodImage(version string) string {
	return "mongo:" + version
}

func stsImage(t *testing.T, cl client.Client, name string) string {
	t.Helper()
	return getStatefulSet(t, cl, name).Spec.Template.Spec.Containers[0].Image
}

func mongosImage(t *testing.T, cl client.Client) string {
	t.Helper()
	return getDeployment(t, cl, rolloutMongosName).Spec.Template.Spec.Containers[0].Image
}

func reconcileRollout(t *testing.T, r *MongoDBShardedReconciler) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: rolloutTestName, Namespace: rolloutTestNS},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return res
}

// TestShardedUpgrade_AppliesComponentsInOrder — cfg 가 새 revision 으로 다 구르기 전에는
// shard 를, shard 가 다 구르기 전에는 mongos 를 건드리지 않는다.
// 버그: ReadyReplicas 만 봐서 apply 직후(아직 3/3 ready) 통과 → 한 reconcile 에 전부 갱신.
func TestShardedUpgrade_AppliesComponentsInOrder(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newRolloutSharded(UpgradePhaseUpgrading, time.Second)
	objs := append(rolloutWorkloads(mdbsh, rolloutOldVersion, rolloutOldRevision), mdbsh)
	cl := newRolloutClient(t, s, &rolloutProbe{}, objs...)
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	oldImage := mongodImage(rolloutOldVersion)
	newImage := mongodImage(rolloutNewVersion)
	if got := stsImage(t, cl, rolloutCfgName); got != oldImage {
		t.Fatalf("fixture: cfg image = %q, want %q", got, oldImage)
	}

	// 1) cfg 만 새 이미지. shard · mongos 는 그대로.
	res := reconcileRollout(t, r)
	if got := stsImage(t, cl, rolloutCfgName); got != newImage {
		t.Fatalf("cfg image = %q, want %q", got, newImage)
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
	if res.RequeueAfter <= 0 {
		t.Errorf("no requeue while config server is rolling: %+v", res)
	}

	// 2) cfg 완료 → shard 갱신. mongos 는 그대로.
	finishRollout(t, cl, rolloutCfgName)
	reconcileRollout(t, r)
	for _, name := range []string{rolloutShard0Name, rolloutShard1Name} {
		if got := stsImage(t, cl, name); got != newImage {
			t.Fatalf("%s image = %q after config server rolled, want %q", name, got, newImage)
		}
	}
	if got := mongosImage(t, cl); got != oldImage {
		t.Errorf("mongos updated before shards finished rolling: image = %q", got)
	}

	// 3) shard 하나만 완료 → mongos 는 여전히 그대로.
	finishRollout(t, cl, rolloutShard0Name)
	reconcileRollout(t, r)
	if got := mongosImage(t, cl); got != oldImage {
		t.Errorf("mongos updated while shard-1 is still rolling: image = %q", got)
	}

	// 4) 모든 shard 완료 → mongos 갱신 + Validating.
	finishRollout(t, cl, rolloutShard1Name)
	reconcileRollout(t, r)
	if got := mongosImage(t, cl); got != newImage {
		t.Fatalf("mongos image = %q after shards rolled, want %q", got, newImage)
	}
	if got := getSharded(t, cl).Status.UpgradePhase; got != UpgradePhaseValidating {
		t.Errorf("phase = %q after all components applied, want %q", got, UpgradePhaseValidating)
	}
}

// TestShardedUpgrade_StuckRolloutTimesOut — Upgrading 에서 cfg 롤아웃이 예산을 넘기면
// 영구 대기가 아니라 기존 검증 timeout 과 같은 길로 롤백한다.
func TestShardedUpgrade_StuckRolloutTimesOut(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newRolloutSharded(UpgradePhaseUpgrading, 24*time.Hour)
	mdbsh.Spec.UpgradeStrategy = &mongodbv1alpha1.UpgradeStrategySpec{RollbackOnFailure: true}
	objs := append(rolloutWorkloads(mdbsh, rolloutOldVersion, rolloutOldRevision), mdbsh)
	cl := newRolloutClient(t, s, &rolloutProbe{}, objs...)
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	reconcileRollout(t, r)

	if got := getSharded(t, cl).Status.UpgradePhase; got != UpgradePhaseRollingBack {
		t.Errorf("phase = %q after rollout budget elapsed, want %q", got, UpgradePhaseRollingBack)
	}
	if got := stsImage(t, cl, rolloutShard0Name); got != mongodImage(rolloutOldVersion) {
		t.Errorf("shard-0 updated although config server never rolled: image = %q", got)
	}
}

// TestShardedUpgradeValidation_WaitsForRollout — 어느 컴포넌트든 새 revision 으로 다
// 구르지 않았으면 FCV commit 에 들어가지 않는다. Status 의 Ready 수는 전부 3/3(stale).
func TestShardedUpgradeValidation_WaitsForRollout(t *testing.T) {
	midRollSts := func(name string) func(t *testing.T, cl client.Client, probe *rolloutProbe) {
		return func(t *testing.T, cl client.Client, _ *rolloutProbe) {
			sts := getStatefulSet(t, cl, name)
			sts.Status.UpdatedReplicas = 2
			sts.Status.CurrentRevision = rolloutOldRevision
			if err := cl.Status().Update(context.Background(), sts); err != nil {
				t.Fatal(err)
			}
		}
	}
	updateSts := func(name string, mutate func(*appsv1.StatefulSet)) func(t *testing.T, cl client.Client, probe *rolloutProbe) {
		return func(t *testing.T, cl client.Client, _ *rolloutProbe) {
			sts := getStatefulSet(t, cl, name)
			mutate(sts)
			writeWorkload(t, cl, sts)
		}
	}
	updateMongos := func(mutate func(*appsv1.Deployment)) func(t *testing.T, cl client.Client, probe *rolloutProbe) {
		return func(t *testing.T, cl client.Client, _ *rolloutProbe) {
			dep := getDeployment(t, cl, rolloutMongosName)
			mutate(dep)
			writeWorkload(t, cl, dep)
		}
	}

	cases := []struct {
		name   string
		break_ func(t *testing.T, cl client.Client, probe *rolloutProbe)
	}{
		{"config server mid-roll, all ready", midRollSts(rolloutCfgName)},
		{"shard-0 mid-roll, all ready", midRollSts(rolloutShard0Name)},
		{"shard-1 mid-roll, all ready", midRollSts(rolloutShard1Name)},
		{"config server spec not yet observed", updateSts(rolloutCfgName, func(sts *appsv1.StatefulSet) {
			sts.Generation = 2
		})},
		{"shard-1 updated pod not ready", updateSts(rolloutShard1Name, func(sts *appsv1.StatefulSet) {
			sts.Status.ReadyReplicas = 2
		})},
		{"shard-0 revision unknown", updateSts(rolloutShard0Name, func(sts *appsv1.StatefulSet) {
			sts.Status.CurrentRevision = ""
			sts.Status.UpdateRevision = ""
		})},
		{"mongos old pods remain", updateMongos(func(dep *appsv1.Deployment) {
			dep.Status.UpdatedReplicas = 1
		})},
		{"mongos new pod not available", updateMongos(func(dep *appsv1.Deployment) {
			dep.Status.AvailableReplicas = 1
		})},
		{"mongos surge pod not yet removed", updateMongos(func(dep *appsv1.Deployment) {
			dep.Status.Replicas = rolloutMongos + 1
		})},
		{"mongos spec not yet observed", updateMongos(func(dep *appsv1.Deployment) {
			dep.Generation = 2
		})},
		{"shard-1 statefulset missing", func(t *testing.T, cl client.Client, _ *rolloutProbe) {
			if err := cl.Delete(context.Background(), getStatefulSet(t, cl, rolloutShard1Name)); err != nil {
				t.Fatal(err)
			}
		}},
		{"config server read fails", func(_ *testing.T, _ client.Client, probe *rolloutProbe) {
			probe.failGet = rolloutCfgName
		}},
		{"mongos read fails", func(_ *testing.T, _ client.Client, probe *rolloutProbe) {
			probe.failGet = rolloutMongosName
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestScheme(t)
			mdbsh := newRolloutSharded(UpgradePhaseValidating, 2*time.Minute)
			objs := append(rolloutWorkloads(mdbsh, rolloutNewVersion, rolloutNewRevision), mdbsh)
			probe := &rolloutProbe{}
			cl := newRolloutClient(t, s, probe, objs...)
			r := &MongoDBShardedReconciler{Client: cl, Scheme: s}
			tc.break_(t, cl, probe)

			res, handled, err := r.reconcileShardedUpgradeValidation(context.Background(), mdbsh, rolloutNewVersion)
			if err != nil {
				t.Fatalf("validation: %v", err)
			}

			if probe.secretGets != 0 {
				t.Errorf("FCV commit entered during rollout: admin secret read %d time(s)", probe.secretGets)
			}
			stored := getSharded(t, cl)
			if stored.Status.UpgradePhase != UpgradePhaseValidating {
				t.Errorf("phase = %q, want %q", stored.Status.UpgradePhase, UpgradePhaseValidating)
			}
			if stored.Status.Version != rolloutOldVersion {
				t.Errorf("Status.Version = %q, want %q (upgrade must not complete)", stored.Status.Version, rolloutOldVersion)
			}
			if !handled || res.RequeueAfter <= 0 {
				t.Errorf("handled=%v result=%+v, want handled with requeue", handled, res)
			}
		})
	}
}

// TestShardedUpgradeValidation_CommitsAfterRollout — 양성 대조: 전부 굴렀으면 FCV commit 에
// 들어간다. Status 의 Ready 수가 0 이어도(stale) 라이브 판독이 이긴다.
func TestShardedUpgradeValidation_CommitsAfterRollout(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newRolloutSharded(UpgradePhaseValidating, 2*time.Minute)
	mdbsh.Status.ConfigServer.Ready = 0
	objs := append(rolloutWorkloads(mdbsh, rolloutNewVersion, rolloutNewRevision), mdbsh)
	probe := &rolloutProbe{}
	cl := newRolloutClient(t, s, probe, objs...)
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	if _, _, err := r.reconcileShardedUpgradeValidation(context.Background(), mdbsh, rolloutNewVersion); err != nil {
		t.Fatalf("validation: %v", err)
	}

	if probe.secretGets == 0 {
		t.Error("FCV commit not entered although every component finished rolling")
	}
}

// TestShardedUpgradeValidation_CompletesSameFCV — FCV 가 이미 목표와 같으면(패치 업그레이드)
// 롤아웃 완료 뒤 업그레이드를 끝낸다.
func TestShardedUpgradeValidation_CompletesSameFCV(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newRolloutSharded(UpgradePhaseValidating, 2*time.Minute)
	mdbsh.Status.FCVVersion = fcvMajorMinor(rolloutNewVersion)
	objs := append(rolloutWorkloads(mdbsh, rolloutNewVersion, rolloutNewRevision), mdbsh)
	cl := newRolloutClient(t, s, &rolloutProbe{}, objs...)
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	if _, _, err := r.reconcileShardedUpgradeValidation(context.Background(), mdbsh, rolloutNewVersion); err != nil {
		t.Fatalf("validation: %v", err)
	}

	stored := getSharded(t, cl)
	if stored.Status.Version != rolloutNewVersion || stored.Status.UpgradePhase != "" {
		t.Errorf("version=%q phase=%q, want %q and empty phase",
			stored.Status.Version, stored.Status.UpgradePhase, rolloutNewVersion)
	}
}

func TestStatefulSetRolledOut_NilReplicasDefaultsToOne(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Status: appsv1.StatefulSetStatus{
			UpdatedReplicas: 1, ReadyReplicas: 1,
			CurrentRevision: rolloutNewRevision, UpdateRevision: rolloutNewRevision,
		},
	}
	if !statefulSetRolledOut(sts) {
		t.Errorf("nil spec.replicas with 1 updated ready pod: want rolled out, status=%+v", sts.Status)
	}
}
