/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// 본 파일은 envtest 없이 fake client만으로 silent failure 패턴 정정의 회귀
// 가드를 제공한다. 진짜 mongo driver는 호출되지 않으며 controller helper
// (updateStatusError / setPrimaryUnreachableCondition / clear...)가 status에
// 정확한 condition을 기록하는지만 검증한다.
package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := mongodbv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add mongodb scheme: %v", err)
	}
	return s
}

// TestUpdateStatusError_SetsReconcileErrorCondition은 controller helper가
// status.Conditions에 ReconcileError=True를 기록하고 phase를 Failed로
// 전환하는지 검증한다. 이전 silent 패턴(logger.Info; return nil)을 이
// 헬퍼 호출로 교체한 모든 위치는 동일한 condition을 만들어야 한다.
func TestUpdateStatusError_SetsReconcileErrorCondition(t *testing.T) {
	s := newTestScheme(t)
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        3,
			ReplicaSetName: "rs0",
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdb).
		WithStatusSubresource(mdb).
		Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	injected := errors.New("connection refused")
	res, err := r.updateStatusError(context.Background(), mdb, "ReplicaSetInit", injected)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("기대: error propagate, got: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Fatalf("기대: RequeueAfter 설정, got 0")
	}

	// 서버에서 다시 fetch해 condition이 영속화됐는지 확인.
	got := &mongodbv1alpha1.MongoDB{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(mdb), got); err != nil {
		t.Fatalf("get mdb: %v", err)
	}
	if got.Status.Phase != mongodbv1alpha1.PhaseFailed {
		t.Fatalf("기대 phase=Failed, got=%s", got.Status.Phase)
	}
	found := false
	for _, c := range got.Status.Conditions {
		if c.Type == "ReconcileError" && c.Status == metav1.ConditionTrue {
			if !strings.Contains(c.Message, "ReplicaSetInit") || !strings.Contains(c.Message, "connection refused") {
				t.Fatalf("condition message에 component/error 누락: %q", c.Message)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("ReconcileError condition 미발견: %+v", got.Status.Conditions)
	}
}

// TestSetPrimaryUnreachableCondition은 hasPrimary가 connect 실패할 때
// PrimaryUnreachable condition이 status에 기록되는지 검증한다.
// 이전 silent 패턴: logger.Info만 찍고 condition 없음.
func TestSetPrimaryUnreachableCondition(t *testing.T) {
	s := newTestScheme(t)
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec:       mongodbv1alpha1.MongoDBSpec{Members: 3, ReplicaSetName: "rs0"},
	}
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdb).
		WithStatusSubresource(mdb).
		Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	connErr := errors.New("dial tcp 10.0.0.1:27017: i/o timeout\nstack: ...")
	r.setPrimaryUnreachableCondition(context.Background(), mdb, connErr)

	got := &mongodbv1alpha1.MongoDB{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(mdb), got); err != nil {
		t.Fatalf("get mdb: %v", err)
	}
	var puCond *metav1.Condition
	for i := range got.Status.Conditions {
		if got.Status.Conditions[i].Type == "PrimaryUnreachable" {
			puCond = &got.Status.Conditions[i]
		}
	}
	if puCond == nil {
		t.Fatalf("PrimaryUnreachable condition이 기록되지 않음: %+v", got.Status.Conditions)
	}
	if puCond.Status != metav1.ConditionTrue {
		t.Fatalf("기대 True, got=%s", puCond.Status)
	}
	if !strings.Contains(puCond.Message, "i/o timeout") {
		t.Fatalf("error 첫 줄이 message에 없음: %q", puCond.Message)
	}
	// firstLine이 multi-line err를 잘랐는지 검증.
	if strings.Contains(puCond.Message, "stack:") {
		t.Fatalf("multi-line err가 잘리지 않음: %q", puCond.Message)
	}

	// clear 호출 시 False로 갱신.
	r.clearPrimaryUnreachableCondition(context.Background(), mdb)
	got2 := &mongodbv1alpha1.MongoDB{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(mdb), got2); err != nil {
		t.Fatalf("get mdb: %v", err)
	}
	for _, c := range got2.Status.Conditions {
		if c.Type == "PrimaryUnreachable" && c.Status != metav1.ConditionFalse {
			t.Fatalf("clear 후 condition False 아님: %+v", c)
		}
	}
}

// TestUpdateStatusError_DoesNotAccumulateConditions은 updateStatusError가
// 반복 호출돼도 ReconcileError condition이 항상 1건만 유지되는지 검증한다.
// 이전 패턴(append만)은 매 호출마다 누적 → status.Conditions 폭주의 P2 버그.
func TestUpdateStatusError_DoesNotAccumulateConditions(t *testing.T) {
	s := newTestScheme(t)
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec:       mongodbv1alpha1.MongoDBSpec{Members: 3, ReplicaSetName: "rs0"},
	}
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdb).
		WithStatusSubresource(mdb).
		Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	for i := 0; i < 3; i++ {
		_, _ = r.updateStatusError(context.Background(), mdb, "Component", errors.New("transient"))
	}

	got := &mongodbv1alpha1.MongoDB{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(mdb), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	count := 0
	for _, c := range got.Status.Conditions {
		if c.Type == "ReconcileError" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("기대 ReconcileError 1건, got %d (conditions: %+v)", count, got.Status.Conditions)
	}
}

// TestIsClusterReady_RejectsNilShardsAndZeroSpec은 sharded controller의
// isClusterReady가 status.Shards가 nil(부분 누락)이거나 spec value=0(잘못된
// 설정)일 때 절대 ready로 판정하지 않는지 검증한다.
func TestIsClusterReady_RejectsNilShardsAndZeroSpec(t *testing.T) {
	r := &MongoDBShardedReconciler{}

	cases := []struct {
		name string
		mdb  mongodbv1alpha1.MongoDBSharded
		want bool
	}{
		{
			name: "shards_status_nil_but_spec_count_2",
			mdb: mongodbv1alpha1.MongoDBSharded{
				Spec: mongodbv1alpha1.MongoDBShardedSpec{
					ConfigServer: mongodbv1alpha1.ConfigServerSpec{Members: 3},
					Shards:       mongodbv1alpha1.ShardSpec{Count: 2, MembersPerShard: 3},
					Mongos:       mongodbv1alpha1.MongosSpec{Replicas: 2},
				},
				Status: mongodbv1alpha1.MongoDBShardedStatus{
					ConfigServer: mongodbv1alpha1.ComponentStatus{Ready: 3},
					Mongos:       mongodbv1alpha1.ComponentStatus{Ready: 2},
				},
			},
			want: false,
		},
		{
			name: "spec_zero_members",
			mdb: mongodbv1alpha1.MongoDBSharded{
				Spec: mongodbv1alpha1.MongoDBShardedSpec{
					Shards: mongodbv1alpha1.ShardSpec{Count: 0, MembersPerShard: 0},
					Mongos: mongodbv1alpha1.MongosSpec{Replicas: 0},
				},
			},
			want: false,
		},
		{
			name: "all_ready",
			mdb: mongodbv1alpha1.MongoDBSharded{
				Spec: mongodbv1alpha1.MongoDBShardedSpec{
					ConfigServer: mongodbv1alpha1.ConfigServerSpec{Members: 3},
					Shards:       mongodbv1alpha1.ShardSpec{Count: 2, MembersPerShard: 3},
					Mongos:       mongodbv1alpha1.MongosSpec{Replicas: 2},
				},
				Status: mongodbv1alpha1.MongoDBShardedStatus{
					ConfigServer: mongodbv1alpha1.ComponentStatus{Ready: 3},
					Mongos:       mongodbv1alpha1.ComponentStatus{Ready: 2},
					Shards: []mongodbv1alpha1.ShardStatus{
						{Name: "shard-0", Ready: 3},
						{Name: "shard-1", Ready: 3},
					},
				},
			},
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.isClusterReady(&c.mdb)
			if got != c.want {
				t.Fatalf("기대 %v, got %v (mdb: %+v)", c.want, got, c.mdb)
			}
		})
	}
}

// TestBuildConditions_PreservesExternalConditions은 buildConditions가
// 외부에서 set한 PrimaryUnreachable condition을 보존하는지 검증.
// 이전: 매 호출마다 conditions=[]로 초기화 → 외부 condition silent 손실.
func TestBuildConditions_PreservesExternalConditions(t *testing.T) {
	s := newTestScheme(t)
	r := &MongoDBReconciler{Scheme: s}
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec:       mongodbv1alpha1.MongoDBSpec{Members: 3},
		Status: mongodbv1alpha1.MongoDBStatus{
			Conditions: []metav1.Condition{
				{Type: "PrimaryUnreachable", Status: metav1.ConditionTrue, Reason: "ConnectError", LastTransitionTime: metav1.Now()},
				{Type: "Ready", Status: metav1.ConditionFalse, Reason: "Stale", LastTransitionTime: metav1.Now()},
			},
		},
	}

	out := r.buildConditions(mdb)

	hasPrimary, hasReady := false, false
	readyCount := 0
	for _, c := range out {
		if c.Type == "PrimaryUnreachable" {
			hasPrimary = true
		}
		if c.Type == "Ready" {
			hasReady = true
			readyCount++
		}
	}
	if !hasPrimary {
		t.Fatalf("PrimaryUnreachable 보존 실패: %+v", out)
	}
	if !hasReady || readyCount != 1 {
		t.Fatalf("Ready condition 1건 재생성 실패 (hasReady=%v count=%d): %+v", hasReady, readyCount, out)
	}
}

func TestClearReconcileErrorCondition(t *testing.T) {
	conds := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now()},
		{Type: conditionTypeReconcileError, Status: metav1.ConditionTrue, Reason: "ReconcileFailed", LastTransitionTime: metav1.Now()},
	}

	out := clearReconcileErrorCondition(conds, 7)
	foundFalse := false
	for _, cond := range out {
		if cond.Type == conditionTypeReconcileError {
			if cond.Status != metav1.ConditionFalse {
				t.Fatalf("기대 ReconcileError=False, got %+v", cond)
			}
			if cond.ObservedGeneration != 7 {
				t.Fatalf("기대 observedGeneration=7, got %d", cond.ObservedGeneration)
			}
			foundFalse = true
		}
	}
	if !foundFalse {
		t.Fatalf("ReconcileError=False condition 미발견: %+v", out)
	}

	withoutError := []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now()}}
	unchanged := clearReconcileErrorCondition(withoutError, 7)
	if len(unchanged) != 1 || unchanged[0].Type != "Ready" {
		t.Fatalf("ReconcileError 미존재 시 condition 추가 금지: %+v", unchanged)
	}
}

// TestShardedUpdateStatusError은 sharded controller의 동일 헬퍼가 cfg/shard
// init 실패에 대해 ReconcileError condition을 남기는지 검증.
func TestShardedUpdateStatusError(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := &mongodbv1alpha1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{Name: "sh", Namespace: "ns"},
	}
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdbsh).
		WithStatusSubresource(mdbsh).
		Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	injected := errors.New("check config server init: connect for IsInitialized: auth failed")
	if _, err := r.updateStatusError(context.Background(), mdbsh, "ConfigServerInit", injected); err == nil {
		t.Fatalf("기대 error propagate")
	}

	got := &mongodbv1alpha1.MongoDBSharded{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(mdbsh), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != mongodbv1alpha1.ShardedPhaseFailed {
		t.Fatalf("기대 phase Failed, got %s", got.Status.Phase)
	}
	found := false
	for _, c := range got.Status.Conditions {
		if c.Type == "ReconcileError" && strings.Contains(c.Message, "ConfigServerInit") {
			found = true
		}
	}
	if !found {
		t.Fatalf("ReconcileError(ConfigServerInit) 미발견: %+v", got.Status.Conditions)
	}
}
