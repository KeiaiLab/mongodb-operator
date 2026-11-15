/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Pre-init gate — MemberState readiness 의 멤버는 rs.initiate 전에 Ready 가 될 수
// 없다. operator 가 initiate 하는 경로(3 · 5 단계 대기 → 7 · 8 단계 init)가
// 영영 열리지 않는 deadlock 을 막는지 잠근다. fake client 만 쓴다.

package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

type podState int

const (
	podRunning podState = iota
	podWaiting
)

const gateName = "gate"

func gateSharded(mode mongodbv1alpha1.ReadinessMode) *mongodbv1alpha1.MongoDBSharded {
	mdbsh := newAdoptedSharded(gateName, "9.0.8")
	mdbsh.Spec.Shards.Count = 1
	mdbsh.Spec.Shards.Readiness = mode
	mdbsh.Spec.ConfigServer.Readiness = mode
	return mdbsh
}

// gateMembers 는 STS 와 그 pod 들을 만든다. ReadyReplicas 는 0(멤버 상태 미달).
func gateMembers(sts *appsv1.StatefulSet, state podState) []client.Object {
	objs := []client.Object{sts}
	for i := int32(0); i < *sts.Spec.Replicas; i++ {
		cs := corev1.ContainerStatus{Name: "mongodb"}
		if state == podRunning {
			cs.State.Running = &corev1.ContainerStateRunning{}
		}
		objs = append(objs, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%d", sts.Name, i), Namespace: sts.Namespace,
				Labels: sts.Spec.Selector.MatchLabels,
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{cs}},
		})
	}
	return objs
}

func gateReconciler(t *testing.T, mdbsh *mongodbv1alpha1.MongoDBSharded, state podState) *MongoDBShardedReconciler {
	t.Helper()
	objs := gateMembers(resources.BuildConfigServerStatefulSet(mdbsh), state)
	objs = append(objs, gateMembers(resources.BuildShardStatefulSet(mdbsh, 0), state)...)
	cl := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()
	return &MongoDBShardedReconciler{Client: cl, Scheme: cl.Scheme()}
}

// TestInitGate_MemberStateUninitialized — init 전에는 mongod 가 떠 있으면 연다.
func TestInitGate_MemberStateUninitialized(t *testing.T) {
	mdbsh := gateSharded(mongodbv1alpha1.ReadinessMemberState)
	r := gateReconciler(t, mdbsh, podRunning)

	require.True(t, r.isConfigServerReady(context.Background(), mdbsh))
	require.True(t, r.areShardsReady(context.Background(), mdbsh))
}

// TestInitGate_MemberStateNotRunning — mongod 가 안 떴으면 init 전이라도 닫는다.
func TestInitGate_MemberStateNotRunning(t *testing.T) {
	mdbsh := gateSharded(mongodbv1alpha1.ReadinessMemberState)
	r := gateReconciler(t, mdbsh, podWaiting)

	require.False(t, r.isConfigServerReady(context.Background(), mdbsh))
	require.False(t, r.areShardsReady(context.Background(), mdbsh))
}

// TestInitGate_MemberStateInitialized — init 뒤에는 Ready(멤버 상태)만 센다.
func TestInitGate_MemberStateInitialized(t *testing.T) {
	mdbsh := gateSharded(mongodbv1alpha1.ReadinessMemberState)
	mdbsh.Status.ConfigServerInitialized = true
	mdbsh.Status.ShardsInitialized = []bool{true}
	r := gateReconciler(t, mdbsh, podRunning)

	require.False(t, r.isConfigServerReady(context.Background(), mdbsh))
	require.False(t, r.areShardsReady(context.Background(), mdbsh))
}

// TestInitGate_PingUnchanged — Ping 은 지금처럼 ReadyReplicas 만 센다.
func TestInitGate_PingUnchanged(t *testing.T) {
	mdbsh := gateSharded(mongodbv1alpha1.ReadinessPing)
	r := gateReconciler(t, mdbsh, podRunning)

	require.False(t, r.isConfigServerReady(context.Background(), mdbsh))
	require.False(t, r.areShardsReady(context.Background(), mdbsh))
}
