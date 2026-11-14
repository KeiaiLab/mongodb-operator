/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

// builder(commons labels.Set)가 워크로드에 붙이는 권장 라벨 키.
const (
	labelInstance  = "app.kubernetes.io/instance"
	labelComponent = "app.kubernetes.io/component"
)

// defaultWorkloadReplicas — spec.replicas 생략 시 API 서버가 채우는 값(apps/v1).
const defaultWorkloadReplicas = int32(1)

func desiredReplicas(replicas *int32) int32 {
	if replicas == nil {
		return defaultWorkloadReplicas
	}
	return *replicas
}

// statefulSetRolledOut 은 모든 pod 가 최신 revision 으로 ready 인지 판정한다.
// observedGeneration 이 뒤처졌으면 status 는 옛 spec 의 것이라 믿지 않는다 — apply 직후의
// "아직 전부 ready" 를 완료로 오판하지 않게 한다. 빈 revision 은 미확인 = 미완료.
func statefulSetRolledOut(sts *appsv1.StatefulSet) bool {
	want := desiredReplicas(sts.Spec.Replicas)

	return sts.Status.ObservedGeneration >= sts.Generation &&
		sts.Status.UpdateRevision != "" &&
		sts.Status.CurrentRevision == sts.Status.UpdateRevision &&
		sts.Status.UpdatedReplicas == want &&
		sts.Status.ReadyReplicas == want
}

// deploymentRolledOut 은 새 ReplicaSet 의 pod 만 남아 전부 available 인지 판정한다
// (kubectl rollout status 와 같은 조건).
func deploymentRolledOut(dep *appsv1.Deployment) bool {
	want := desiredReplicas(dep.Spec.Replicas)

	return dep.Status.ObservedGeneration >= dep.Generation &&
		dep.Status.UpdatedReplicas == want &&
		dep.Status.Replicas == want &&
		dep.Status.AvailableReplicas == want
}

// rolloutTarget 은 목표 버전에서 builder 가 렌더할 컴포넌트별 DB 컨테이너(이름 · 이미지)다.
type rolloutTarget struct {
	configServer corev1.Container
	shard        corev1.Container
	mongos       corev1.Container
}

func shardedRolloutTarget(mdbsh *mongodbv1alpha1.MongoDBSharded) rolloutTarget {
	target := mdbsh.DeepCopy()
	target.Status.EffectiveVersion = mdbsh.Spec.Version.Version

	return rolloutTarget{
		configServer: resources.BuildConfigServerStatefulSet(target).Spec.Template.Spec.Containers[0],
		shard:        resources.BuildShardStatefulSet(target, 0).Spec.Template.Spec.Containers[0],
		mongos:       resources.BuildMongosDeployment(target).Spec.Template.Spec.Containers[0],
	}
}

// templateRunsTarget 은 pod template 의 DB 컨테이너가 목표 이미지인지 판정한다. 뒤처진
// 읽기가 돌려준 apply 전 객체는 옛 이미지를 들고 있어 여기서 걸린다 — status 가 "다
// 굴렀다" 여도 그것은 옛 버전의 롤아웃이다.
func templateRunsTarget(template corev1.PodTemplateSpec, want corev1.Container) bool {
	for _, c := range template.Spec.Containers {
		if c.Name == want.Name {
			return c.Image == want.Image
		}
	}
	return false
}

// imageDeclaresVersion 은 이미지 태그가 version 이거나 version-<접미> 인지 본다
// (예: registry/mongo:9.0.8, mongo:9.0.8-ubi9). 태그 없음 · digest 고정은 알 수 없음이다.
func imageDeclaresVersion(image, version string) bool {
	ref, _, _ := strings.Cut(image, "@")
	tagAt := strings.LastIndex(ref, ":")
	if tagAt < 0 || tagAt < strings.LastIndex(ref, "/") {
		return false
	}

	tag := ref[tagAt+1:]
	return tag == version || strings.HasPrefix(tag, version+"-")
}

// shardedBinaryKnown 은 굴러간 바이너리가 목표 버전임을 오퍼레이터가 알 수 있는지 답한다.
// spec.version.image 가 없으면 이미지는 version 에서 나온다. 있으면 이미지는 version 을
// 따라가지 않으므로(version 만 올리면 template 불변) 태그가 목표 버전을 선언할 때만 안다.
func shardedBinaryKnown(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	image := mdbsh.Spec.Version.Image
	return image == "" || imageDeclaresVersion(image, mdbsh.Spec.Version.Version)
}

// liveReader 는 cache 를 거치지 않는 읽기다. cache 는 apply 직후 옛 객체(옛 generation ·
// 완료된 status)를 돌려줘 게이트를 통과시킨다.
func (r *MongoDBShardedReconciler) liveReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// statefulSetRolloutDone 은 StatefulSet 을 API 에서 읽어 판정한다. 읽기 실패 · 부재는
// 미완료다(fail-closed).
func (r *MongoDBShardedReconciler) statefulSetRolloutDone(ctx context.Context, key types.NamespacedName, want corev1.Container) bool {
	sts := &appsv1.StatefulSet{}
	if err := r.liveReader().Get(ctx, key, sts); err != nil {
		log.FromContext(ctx).Info("statefulset rollout unknown, treating as not complete",
			"statefulset", key.Name, "error", err.Error())
		return false
	}
	return templateRunsTarget(sts.Spec.Template, want) && statefulSetRolledOut(sts)
}

func (r *MongoDBShardedReconciler) configServerRolledOut(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	key := types.NamespacedName{Name: mdbsh.Name + "-cfg", Namespace: mdbsh.Namespace}
	return r.statefulSetRolloutDone(ctx, key, shardedRolloutTarget(mdbsh).configServer)
}

func shardStatefulSetName(mdbsh *mongodbv1alpha1.MongoDBSharded, index int32) string {
	return fmt.Sprintf("%s-shard-%d", mdbsh.Name, index)
}

// isShardStatefulSet 은 이 CR 의 shard StatefulSet(이름 <cr>-shard-<n>, component shard-<n>)
// 인지 본다.
func isShardStatefulSet(mdbsh *mongodbv1alpha1.MongoDBSharded, sts *appsv1.StatefulSet) bool {
	return strings.HasPrefix(sts.Name, mdbsh.Name+"-shard-") &&
		strings.HasPrefix(sts.Labels[labelComponent], "shard-")
}

// shardsRolledOut 은 이 CR 의 shard StatefulSet 중 *존재하는 것 전부*를 본다. spec 의
// count 만 돌면 업그레이드 도중 count 를 내렸을 때 아직 클러스터에 붙어 있는 옛 바이너리
// shard 가 게이트 밖으로 빠진다.
func (r *MongoDBShardedReconciler) shardsRolledOut(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	list := &appsv1.StatefulSetList{}
	if err := r.liveReader().List(ctx, list,
		client.InNamespace(mdbsh.Namespace), client.MatchingLabels{labelInstance: mdbsh.Name}); err != nil {
		log.FromContext(ctx).Info("shard rollout unknown, treating as not complete", "error", err.Error())
		return false
	}

	want := shardedRolloutTarget(mdbsh).shard
	rolled := map[string]bool{}
	for i := range list.Items {
		sts := &list.Items[i]
		if !isShardStatefulSet(mdbsh, sts) {
			continue
		}
		if !templateRunsTarget(sts.Spec.Template, want) || !statefulSetRolledOut(sts) {
			return false
		}
		rolled[sts.Name] = true
	}

	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		if !rolled[shardStatefulSetName(mdbsh, i)] {
			return false
		}
	}
	return true
}

func (r *MongoDBShardedReconciler) mongosRolledOut(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	dep := &appsv1.Deployment{}
	name := mdbsh.Name + "-mongos"
	if err := r.liveReader().Get(ctx, types.NamespacedName{Name: name, Namespace: mdbsh.Namespace}, dep); err != nil {
		log.FromContext(ctx).Info("mongos rollout unknown, treating as not complete",
			"deployment", name, "error", err.Error())
		return false
	}
	return templateRunsTarget(dep.Spec.Template, shardedRolloutTarget(mdbsh).mongos) && deploymentRolledOut(dep)
}

// shardedRolloutComplete 는 cfg · 모든 shard · mongos 가 목표 이미지의 새 revision 으로 다
// 굴렀는지 라이브로 판정한다. FCV commit(point-of-no-return)의 선결 조건 — 옛 바이너리
// 멤버가 하나라도 남은 채 FCV 를 올리면 그 멤버가 클러스터에서 끊긴다.
func (r *MongoDBShardedReconciler) shardedRolloutComplete(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return r.configServerRolledOut(ctx, mdbsh) &&
		r.shardsRolledOut(ctx, mdbsh) &&
		r.mongosRolledOut(ctx, mdbsh)
}

// shardedUpgradeHeld 는 롤백 없이 terminal 실패한 업그레이드(phase 비었고, 이 generation
// 에서 끝났고, 목표 버전에 못 닿음)인지 본다. 이 상태에서는 아직 옛 바이너리인 컴포넌트를
// 새 버전으로 옮기지 않는다 — 순서 게이트 없이 남은 것이 한꺼번에 구르는 것을 막는다.
// spec 이 바뀌면(generation) 업그레이드가 다시 감지돼 순서대로 재개한다.
func shardedUpgradeHeld(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return mdbsh.Status.UpgradePhase == "" &&
		!mdbsh.Status.RollbackActive &&
		mdbsh.Status.Version != "" &&
		mdbsh.Status.Version != mdbsh.Spec.Version.Version &&
		mdbsh.Status.ObservedUpgradeGeneration != 0 &&
		mdbsh.Status.ObservedUpgradeGeneration == mdbsh.Generation
}

// heldOnOtherBinary 는 hold 상태에서 워크로드가 목표 이미지가 아닌지(= 적용하면 새 버전으로
// 구르는지) 본다. 없는 워크로드는 잡지 않는다(생성은 롤링이 아니다). 읽기 실패는 잡는다.
func (r *MongoDBShardedReconciler) heldOnOtherBinary(ctx context.Context, obj client.Object, name, namespace string, want corev1.Container) bool {
	if err := r.liveReader().Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
		return !apierrors.IsNotFound(err)
	}

	switch workload := obj.(type) {
	case *appsv1.StatefulSet:
		return !templateRunsTarget(workload.Spec.Template, want)
	case *appsv1.Deployment:
		return !templateRunsTarget(workload.Spec.Template, want)
	}
	return true
}

func (r *MongoDBShardedReconciler) configServerHeld(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return shardedUpgradeHeld(mdbsh) &&
		r.heldOnOtherBinary(ctx, &appsv1.StatefulSet{}, mdbsh.Name+"-cfg", mdbsh.Namespace, shardedRolloutTarget(mdbsh).configServer)
}

func (r *MongoDBShardedReconciler) shardHeld(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, index int32) bool {
	return shardedUpgradeHeld(mdbsh) &&
		r.heldOnOtherBinary(ctx, &appsv1.StatefulSet{}, shardStatefulSetName(mdbsh, index), mdbsh.Namespace, shardedRolloutTarget(mdbsh).shard)
}

func (r *MongoDBShardedReconciler) mongosHeld(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return shardedUpgradeHeld(mdbsh) &&
		r.heldOnOtherBinary(ctx, &appsv1.Deployment{}, mdbsh.Name+"-mongos", mdbsh.Namespace, shardedRolloutTarget(mdbsh).mongos)
}

// shardedValidationTimeout = interval + 총멤버×budget(갭3 fix — 정상 rollout이 느려도 오판
// rollback 방지). sharded 총멤버 = config + shards×membersPerShard(컴포넌트 다수).
func shardedValidationTimeout(mdbsh *mongodbv1alpha1.MongoDBSharded) time.Duration {
	totalMembers := mdbsh.Spec.ConfigServer.Members + mdbsh.Spec.Shards.Count*mdbsh.Spec.Shards.MembersPerShard
	if totalMembers < 1 {
		totalMembers = 1
	}
	return parseValidationInterval(mdbsh.Spec.UpgradeStrategy) + time.Duration(totalMembers)*perMemberRolloutBudget
}

// awaitShardedRollout 은 Upgrading 단계에서 앞 컴포넌트가 다 구를 때까지 다음 컴포넌트
// 적용을 미룬다(cfg → shards → mongos). 예산은 검증 단계와 같은 시계 · 같은 값이고,
// 넘기면 검증 timeout 과 같은 길(롤백 또는 terminal 실패)로 간다.
func (r *MongoDBShardedReconciler) awaitShardedRollout(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded, component string) (ctrl.Result, error) {
	// 시작 시각이 없으면 예산이 흐르지 않아 영구 대기가 된다 — 지금부터 잰다.
	if mdbsh.Status.UpgradeStartTime == nil {
		startClock := func() { ensureShardedUpgradeStartTime(mdbsh) }
		startClock()
		if err := commonsstatus.UpdateWithRetry(ctx, r.Client, mdbsh, startClock); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, nil
	}

	elapsed := time.Since(mdbsh.Status.UpgradeStartTime.Time)
	timeout := shardedValidationTimeout(mdbsh)
	if elapsed < timeout {
		log.FromContext(ctx).Info("sharded upgrade waiting for rollout before next component",
			"rolling", component, "elapsed", elapsed.String(), "timeout", timeout.String())
		return ctrl.Result{RequeueAfter: upgradeRequeueInterval}, nil
	}

	result, _, err := r.shardedUpgradeTimedOut(ctx, mdbsh, mdbsh.Spec.Version.Version)
	return result, err
}
