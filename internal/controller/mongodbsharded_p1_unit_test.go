/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// 본 파일은 v1.4.1 P1 fix(HPA ordering + status source-of-truth divergence)에 대한
// envtest-free 회귀 가드. fake client + 직접 함수 호출만 사용한다.
//
// Issue #1 (Ordering): HPA reconcile은 ConfigServerInitialized + ShardsInitialized
// 모두 true가 되기 전에는 *생성하지 않는다*. Reconcile()의 단계 순서 재배치 이후
// 외부 호출 / 재진입 회귀를 방지하는 readiness gate가 reconcileMongosHPA /
// reconcileConfigServerHPA 시작부에 있다.
//
// Issue #2 (Status truth): Total은 HPA active 시 obj.Spec.Replicas, inactive 시
// CR.Spec을 source-of-truth로 사용한다. 24h soak 동안 HPA가 .spec.replicas를
// 흔들어도 영구 divergence가 발생하지 않아야 한다.
package controller

import (
	"context"
	"os"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// newP1TestSharded는 fake-client 단위 테스트용 baseline CR을 만든다. shards=2,
// members=3, mongos=2이며 admin secret/auth는 비워둔다(HPA / status 경로만
// 검증하므로 RS init / admin user 경로는 진입하지 않는다).
func newP1TestSharded(name string) *mongodbv1alpha1.MongoDBSharded {
	return &mongodbv1alpha1.MongoDBSharded{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "mongodb.keiailab.com/v1alpha1",
			Kind:       "MongoDBSharded",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: mongodbv1alpha1.MongoDBShardedSpec{
			Version: mongodbv1alpha1.MongoDBVersion{Version: "7.0"},
			ConfigServer: mongodbv1alpha1.ConfigServerSpec{
				Members: 3,
				Storage: mongodbv1alpha1.StorageSpec{Size: resource.MustParse("10Gi")},
			},
			Shards: mongodbv1alpha1.ShardSpec{
				Count:           2,
				MembersPerShard: 3,
				Storage:         mongodbv1alpha1.StorageSpec{Size: resource.MustParse("50Gi")},
			},
			Mongos: mongodbv1alpha1.MongosSpec{Replicas: 2},
		},
	}
}

// enableMongosHPA는 mongos HPA를 활성화한다(min=2, max=10, cpu=70%).
func enableMongosHPA(mdbsh *mongodbv1alpha1.MongoDBSharded) {
	mdbsh.Spec.Mongos.AutoScaling = &mongodbv1alpha1.AutoScalingSpec{
		Enabled:     true,
		MinReplicas: 2,
		MaxReplicas: 10,
		Metrics: []mongodbv1alpha1.AutoScalingMetric{
			{Type: "cpu", Target: 70},
		},
	}
}

// TestReconcileMongosHPA_SkipsBeforeRSInit — HPA readiness gate가 cfg/shard
// init 미완료 상태에서 HPA를 *생성하지 않음*을 검증.
func TestReconcileMongosHPA_SkipsBeforeRSInit(t *testing.T) {
	s := newTestScheme(t)
	if err := autoscalingv2.AddToScheme(s); err != nil {
		t.Fatalf("add autoscaling scheme: %v", err)
	}

	mdbsh := newP1TestSharded("test-gate-skip")
	enableMongosHPA(mdbsh)
	// gate 미충족 — ConfigServerInitialized=false, ShardsInitialized=[]
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdbsh).
		WithStatusSubresource(mdbsh).
		Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s, EnableAutoscaling: true}

	if err := r.reconcileMongosHPA(context.Background(), mdbsh); err != nil {
		t.Fatalf("reconcileMongosHPA gate-skip: %v", err)
	}

	// HPA 객체가 생성되지 않아야 한다.
	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	err := cl.Get(context.Background(),
		types.NamespacedName{Name: mdbsh.Name + "-mongos-hpa", Namespace: mdbsh.Namespace}, hpa)
	if err == nil {
		t.Fatalf("HPA created before RS init — readiness gate did not fire")
	}
}

// TestReconcileMongosHPA_CreatesAfterRSInit — gate 통과 시 HPA가 정상 생성됨.
func TestReconcileMongosHPA_CreatesAfterRSInit(t *testing.T) {
	s := newTestScheme(t)
	if err := autoscalingv2.AddToScheme(s); err != nil {
		t.Fatalf("add autoscaling scheme: %v", err)
	}

	mdbsh := newP1TestSharded("test-gate-pass")
	enableMongosHPA(mdbsh)
	mdbsh.Status.ConfigServerInitialized = true
	mdbsh.Status.ShardsInitialized = []bool{true, true}

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdbsh).
		WithStatusSubresource(mdbsh).
		Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s, EnableAutoscaling: true}

	if err := r.reconcileMongosHPA(context.Background(), mdbsh); err != nil {
		t.Fatalf("reconcileMongosHPA gate-pass: %v", err)
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	if err := cl.Get(context.Background(),
		types.NamespacedName{Name: mdbsh.Name + "-mongos-hpa", Namespace: mdbsh.Namespace}, hpa); err != nil {
		t.Fatalf("HPA not created after gate pass: %v", err)
	}
	if hpa.Spec.MinReplicas == nil || *hpa.Spec.MinReplicas != 2 {
		t.Fatalf("HPA MinReplicas mismatch: got %v want 2", hpa.Spec.MinReplicas)
	}
}

// TestUpdateStatus_HPAActiveUsesDeploymentSpec — HPA active 시 Total은 mongos
// Deployment의 .spec.replicas (HPA가 patch한 값) 를 따라가야 함.
func TestUpdateStatus_HPAActiveUsesDeploymentSpec(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newP1TestSharded("test-status-hpa-active")
	enableMongosHPA(mdbsh) // CR.Spec.Mongos.Replicas = 2 이지만 HPA가 5로 scale-up
	// cfg STS — HPA inactive, Total은 CR.Spec(=3) 을 사용
	cfgSts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: mdbsh.Name + "-cfg", Namespace: "default"},
		Spec:       appsv1.StatefulSetSpec{Replicas: ptr.To[int32](3)},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: 3},
	}
	// shard STS 2개 (HPA 미지원이므로 분기 없음)
	shardObjs := []*appsv1.StatefulSet{
		{ObjectMeta: metav1.ObjectMeta{Name: mdbsh.Name + "-shard-0", Namespace: "default"},
			Spec: appsv1.StatefulSetSpec{Replicas: ptr.To[int32](3)}, Status: appsv1.StatefulSetStatus{ReadyReplicas: 3}},
		{ObjectMeta: metav1.ObjectMeta{Name: mdbsh.Name + "-shard-1", Namespace: "default"},
			Spec: appsv1.StatefulSetSpec{Replicas: ptr.To[int32](3)}, Status: appsv1.StatefulSetStatus{ReadyReplicas: 3}},
	}
	// mongos Deployment — HPA가 .spec.replicas=5 로 patch한 상태 (CR.Spec.Mongos.Replicas=2 와 divergence)
	mongosDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: mdbsh.Name + "-mongos", Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](5)},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 5},
	}

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdbsh, cfgSts, shardObjs[0], shardObjs[1], mongosDeploy).
		WithStatusSubresource(mdbsh).
		Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	if err := r.updateStatus(context.Background(), mdbsh); err != nil {
		t.Fatalf("updateStatus: %v", err)
	}

	if mdbsh.Status.Mongos.Total != 5 {
		t.Errorf("Mongos.Total = %d, want 5 (Deployment.Spec.Replicas; HPA active)", mdbsh.Status.Mongos.Total)
	}
	if mdbsh.Status.Mongos.Ready != 5 {
		t.Errorf("Mongos.Ready = %d, want 5", mdbsh.Status.Mongos.Ready)
	}
	if mdbsh.Status.ConfigServer.Total != 3 {
		t.Errorf("ConfigServer.Total = %d, want 3 (CR.Spec; cfg HPA inactive)", mdbsh.Status.ConfigServer.Total)
	}
}

// TestUpdateStatus_HPAInactiveUsesCRSpec — HPA inactive 시 Total은 CR.Spec.
func TestUpdateStatus_HPAInactiveUsesCRSpec(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newP1TestSharded("test-status-hpa-inactive")
	// HPA 비활성 — Mongos.AutoScaling = nil 그대로
	mongosDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: mdbsh.Name + "-mongos", Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 2},
	}
	cfgSts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: mdbsh.Name + "-cfg", Namespace: "default"},
		Spec:       appsv1.StatefulSetSpec{Replicas: ptr.To[int32](3)},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: 3},
	}

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdbsh, cfgSts, mongosDeploy).
		WithStatusSubresource(mdbsh).
		Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	if err := r.updateStatus(context.Background(), mdbsh); err != nil {
		t.Fatalf("updateStatus: %v", err)
	}

	if mdbsh.Status.Mongos.Total != 2 {
		t.Errorf("Mongos.Total = %d, want 2 (CR.Spec; HPA inactive)", mdbsh.Status.Mongos.Total)
	}
}

// TestUpdateStatus_DeploymentNotFoundDoesNotError — 자식 리소스 미존재 시
// status update가 정상 진행(이전 status 보존, error 없음).
func TestUpdateStatus_DeploymentNotFoundDoesNotError(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newP1TestSharded("test-status-notfound")
	// 자식 리소스 전부 부재 — NotFound는 silent skip 되어야 함

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdbsh).
		WithStatusSubresource(mdbsh).
		Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	if err := r.updateStatus(context.Background(), mdbsh); err != nil {
		t.Fatalf("NotFound는 error를 발생시키지 않아야 함: %v", err)
	}

	// Mongos status는 zero-value 그대로 — 이전 reconcile의 정확한 값을 덮어쓰지 않는다.
	if mdbsh.Status.Mongos.Total != 0 {
		t.Errorf("Mongos.Total = %d, want 0 (deployment not found, status preserved)", mdbsh.Status.Mongos.Total)
	}
}

// TestAreShardsInitialized — helper의 partial-init 안전성을 검증.
func TestAreShardsInitialized(t *testing.T) {
	r := &MongoDBShardedReconciler{}
	cases := []struct {
		name     string
		count    int32
		init     []bool
		expected bool
	}{
		{"empty slice / count=2", 2, nil, false},
		{"partial true / count=2", 2, []bool{true, false}, false},
		{"all true / count=2", 2, []bool{true, true}, true},
		{"slice shorter than count", 3, []bool{true, true}, false},
		{"all true / count=0", 0, []bool{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mdbsh := &mongodbv1alpha1.MongoDBSharded{
				Spec:   mongodbv1alpha1.MongoDBShardedSpec{Shards: mongodbv1alpha1.ShardSpec{Count: tc.count}},
				Status: mongodbv1alpha1.MongoDBShardedStatus{ShardsInitialized: tc.init},
			}
			if got := r.areShardsInitialized(mdbsh); got != tc.expected {
				t.Errorf("areShardsInitialized = %v, want %v", got, tc.expected)
			}
		})
	}
}

// TestSetupWithManager_RegistersHPAOwns — informer cache timeout 회귀 가드.
// SetupWithManager의 Owns 목록에 HorizontalPodAutoscaler가 등록되어야 한다.
// 누락 시 controller-runtime 의 default cached reader가 HPA informer를 lazy
// 생성 시도 → cache sync wait timeout (default 2분) → r.Get(... HPA ...) 영구
// hang. RS controller (mongodb_controller.go) 와 동일 패턴.
//
// 정적 소스 grep — controller-runtime의 builder는 internal field이므로
// runtime introspection 대신 코드를 직접 검증. Sharded + RS 양쪽 모두 검증.
func TestSetupWithManager_RegistersHPAOwns(t *testing.T) {
	cases := []struct {
		path string
		hint string
	}{
		{"mongodbsharded_controller.go", "Sharded controller (P1 #3 - v1.4.1 신규)"},
		{"mongodb_controller.go", "RS controller (v1.2.0 도입)"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("read %s: %v", tc.path, err)
			}
			needle := "Owns(&autoscalingv2.HorizontalPodAutoscaler{})"
			if !strings.Contains(string(data), needle) {
				t.Fatalf("%s (%s) — SetupWithManager에 %q 가 누락. informer cache timeout 회귀 위험.",
					tc.path, tc.hint, needle)
			}
		})
	}
}

// 보호용 — corev1 import가 unused로 떨어지지 않도록 ConfigMap 참조.
// (향후 admin secret 시나리오 확장 시 사용)
var _ = corev1.ConfigMap{}
