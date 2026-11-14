/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
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

// statefulSetRolloutDone 은 StatefulSet 을 API 에서 읽어 판정한다. 읽기 실패 · 부재는
// 미완료다(fail-closed).
func (r *MongoDBShardedReconciler) statefulSetRolloutDone(ctx context.Context, name, namespace string) bool {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sts); err != nil {
		log.FromContext(ctx).Info("statefulset rollout unknown, treating as not complete",
			"statefulset", name, "error", err.Error())
		return false
	}
	return statefulSetRolledOut(sts)
}

func (r *MongoDBShardedReconciler) configServerRolledOut(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return r.statefulSetRolloutDone(ctx, mdbsh.Name+"-cfg", mdbsh.Namespace)
}

func (r *MongoDBShardedReconciler) shardsRolledOut(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		if !r.statefulSetRolloutDone(ctx, fmt.Sprintf("%s-shard-%d", mdbsh.Name, i), mdbsh.Namespace) {
			return false
		}
	}
	return true
}

func (r *MongoDBShardedReconciler) mongosRolledOut(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	dep := &appsv1.Deployment{}
	name := mdbsh.Name + "-mongos"
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: mdbsh.Namespace}, dep); err != nil {
		log.FromContext(ctx).Info("mongos rollout unknown, treating as not complete",
			"deployment", name, "error", err.Error())
		return false
	}
	return deploymentRolledOut(dep)
}

// shardedRolloutComplete 는 cfg · 모든 shard · mongos 가 새 revision 으로 다 굴렀는지
// 라이브로 판정한다. FCV commit(point-of-no-return)의 선결 조건 — 옛 바이너리 멤버가
// 하나라도 남은 채 FCV 를 올리면 그 멤버가 클러스터에서 끊긴다.
func (r *MongoDBShardedReconciler) shardedRolloutComplete(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return r.configServerRolledOut(ctx, mdbsh) &&
		r.shardsRolledOut(ctx, mdbsh) &&
		r.mongosRolledOut(ctx, mdbsh)
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
	if mdbsh.Status.UpgradeStartTime == nil {
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
