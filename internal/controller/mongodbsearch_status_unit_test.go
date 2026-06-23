/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mongodbsearch_status_unit_test.go — PR1: MongoDBSearch status conditions/readiness
// 회귀 가드. mongot 은 source mongod pod 의 sidecar 컨테이너이므로 readiness 는 STS
// selector pod 의 ContainerStatuses[mongot].Ready 로 집계된다. fake client + 순수 함수만
// 사용(envtest 불요) — phase 결정 + condition 전이 + 집계 로직을 결정론적으로 검증.
package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
)

// TestSearchPhaseFromReadiness — mongot sidecar ready/total 집계 → phase 매핑.
func TestSearchPhaseFromReadiness(t *testing.T) {
	cases := []struct {
		name         string
		ready, total int32
		want         string
	}{
		{"sidecar 미주입(롤링 전)", 0, 0, searchPhaseProvisioning},
		{"sidecar 있으나 0 ready", 0, 3, searchPhaseDegraded},
		{"일부 ready(롤링 중)", 1, 3, searchPhaseSyncing},
		{"전부 ready", 3, 3, searchPhaseReady},
		{"단일 ready", 1, 1, searchPhaseReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := searchPhaseFromReadiness(tc.ready, tc.total); got != tc.want {
				t.Errorf("searchPhaseFromReadiness(%d,%d)=%q want %q", tc.ready, tc.total, got, tc.want)
			}
		})
	}
}

// TestSetSearchConditions — phase → 표준 conditions(Available/Progressing/Degraded) 매핑.
func TestSetSearchConditions(t *testing.T) {
	cases := []struct {
		phase                             string
		wantAvail, wantProg, wantDegraded metav1.ConditionStatus
	}{
		{searchPhaseReady, metav1.ConditionTrue, metav1.ConditionFalse, metav1.ConditionFalse},
		{searchPhaseSyncing, metav1.ConditionFalse, metav1.ConditionTrue, metav1.ConditionFalse},
		{searchPhaseProvisioning, metav1.ConditionFalse, metav1.ConditionTrue, metav1.ConditionFalse},
		{searchPhasePending, metav1.ConditionFalse, metav1.ConditionTrue, metav1.ConditionFalse},
		{searchPhaseDegraded, metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionTrue},
		{searchPhaseFailed, metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionTrue},
	}
	for _, tc := range cases {
		t.Run(tc.phase, func(t *testing.T) {
			var conds []metav1.Condition
			setSearchConditions(&conds, 1, tc.phase)
			assertCond(t, conds, conditionTypeSearchAvailable, tc.wantAvail)
			assertCond(t, conds, conditionTypeSearchProgressing, tc.wantProg)
			assertCond(t, conds, conditionTypeSearchDegraded, tc.wantDegraded)
		})
	}
}

func assertCond(t *testing.T, conds []metav1.Condition, condType string, want metav1.ConditionStatus) {
	t.Helper()
	c := meta.FindStatusCondition(conds, condType)
	if c == nil {
		t.Fatalf("condition %q 부재", condType)
	}
	if c.Status != want {
		t.Errorf("condition %q=%q want %q", condType, c.Status, want)
	}
}

// podWithMongot — STS selector(app) 라벨 pod. hasMongot 시 mongot 컨테이너 + ready 상태.
func podWithMongot(podName, ns, app string, hasMongot, mongotReady bool) *corev1.Pod {
	containers := []corev1.Container{{Name: "mongod", Image: "mongo"}}
	statuses := []corev1.ContainerStatus{{Name: "mongod", Ready: true}}
	if hasMongot {
		containers = append(containers, corev1.Container{Name: mongotContainerName, Image: "mongot"})
		statuses = append(statuses, corev1.ContainerStatus{Name: mongotContainerName, Ready: mongotReady})
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: ns, Labels: map[string]string{"app": app}},
		Spec:       corev1.PodSpec{Containers: containers},
		Status:     corev1.PodStatus{ContainerStatuses: statuses},
	}
}

// TestCountReadyMongotSidecars — STS selector 로 pod 조회 → mongot 컨테이너만 집계.
func TestCountReadyMongotSidecars(t *testing.T) {
	s := newTestScheme(t)
	const name, ns = "src", "default"
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       appsv1.StatefulSetSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}}},
	}
	pod0 := podWithMongot(name+"-0", ns, name, true, true)   // mongot ready
	pod1 := podWithMongot(name+"-1", ns, name, true, false)  // mongot not ready
	pod2 := podWithMongot(name+"-2", ns, name, false, false) // no mongot(롤링 전)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(sts, pod0, pod1, pod2).
		WithStatusSubresource(pod0, pod1, pod2).
		Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}
	source := &mongodbv1alpha1.MongoDB{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}

	ready, total, err := r.countReadyMongotSidecars(context.Background(), source)
	if err != nil {
		t.Fatalf("countReadyMongotSidecars: %v", err)
	}
	if total != 2 {
		t.Errorf("total=%d want 2 (mongot 컨테이너 보유 pod)", total)
	}
	if ready != 1 {
		t.Errorf("ready=%d want 1", ready)
	}
}

// TestCountReadyMongotSidecars_NoSTS — source STS 미생성 시 (0,0) 무에러(롤링 전 graceful).
func TestCountReadyMongotSidecars_NoSTS(t *testing.T) {
	s := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}
	source := &mongodbv1alpha1.MongoDB{ObjectMeta: metav1.ObjectMeta{Name: "none", Namespace: "default"}}

	ready, total, err := r.countReadyMongotSidecars(context.Background(), source)
	if err != nil {
		t.Fatalf("STS 부재 시 에러 안 나야: %v", err)
	}
	if ready != 0 || total != 0 {
		t.Errorf("(ready,total)=(%d,%d) want (0,0)", ready, total)
	}
}

// TestSearchReconcile_ReadyWhenSidecarsReady — 전체 흐름: source Running + mongot ready pod
// → MongoDBSearch.Status.Phase=Ready + ReadyReplicas + Available condition.
func TestSearchReconcile_ReadyWhenSidecarsReady(t *testing.T) {
	s := newTestScheme(t)
	const name, ns = "search-it", "default"
	source := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        1,
			ReplicaSetName: "rs0",
			Version:        mongodbv1alpha1.MongoDBVersion{Version: "8.2.0"},
		},
	}
	source.Status.Phase = mongodbPhaseRunning
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       appsv1.StatefulSetSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}}},
	}
	pod := podWithMongot(name+"-0", ns, name, true, true)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-sync", Namespace: ns},
		Data:       map[string][]byte{"username": []byte("search-sync"), "password": []byte("x")},
	}
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: mongodbv1beta1.MongoDBSearchSpec{
			Source:            mongodbv1beta1.SearchSource{MongoDBResourceRef: &corev1.LocalObjectReference{Name: name}, Kind: "MongoDB"},
			SyncUserSecretRef: &corev1.LocalObjectReference{Name: name + "-sync"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(source, sts, pod, secret, search).
		WithStatusSubresource(source, pod, search).
		Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	got := &mongodbv1beta1.MongoDBSearch{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: ns}, got); err != nil {
		t.Fatalf("get search: %v", err)
	}
	if got.Status.Phase != searchPhaseReady {
		t.Errorf("Phase=%q want %q", got.Status.Phase, searchPhaseReady)
	}
	if got.Status.ReadyReplicas != 1 {
		t.Errorf("ReadyReplicas=%d want 1", got.Status.ReadyReplicas)
	}
	if got.Status.MongotEndpoint != mongotSidecarEndpoint {
		t.Errorf("MongotEndpoint=%q want %q", got.Status.MongotEndpoint, mongotSidecarEndpoint)
	}
	if !meta.IsStatusConditionTrue(got.Status.Conditions, conditionTypeSearchAvailable) {
		t.Error("Available condition 이 True 여야(전부 ready)")
	}
}
