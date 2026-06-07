/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

// 에러 reconcile 시 status 정합 회귀 테스트. 이전에는 applyErrorCondition 이
// phase=Failed + ReconcileError=True 만 설정하고, 직전 성공의 Ready=True 와
// stale top-level ObservedGeneration 을 그대로 남겼다. 결과적으로
//   - Ready=True 와 phase=Failed 가 공존하는 모순,
//   - kstatus 가 ObservedGeneration != Generation 을 보고 InProgress 로 오판해
//     Flux helm-controller wait 가 timeout (HelmRelease Stalled).
package controller

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestUpdateStatusError_DowngradesReadyAndObservedGeneration(t *testing.T) {
	s := newApplyScheme(t)
	mdbsh := &mongodbv1alpha1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns", Generation: 2},
		Status: mongodbv1alpha1.MongoDBShardedStatus{
			ObservedGeneration: 1, // 직전 성공 generation
			Conditions: []metav1.Condition{{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 1,
				Reason:             "Available",
				Message:            "all ready",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdbsh).WithStatusSubresource(mdbsh).Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	if _, err := r.updateStatusError(context.Background(), mdbsh, "ConfigServer", errors.New("boom")); err == nil {
		t.Fatalf("updateStatusError 는 reconcile 에러를 전파해야 한다")
	}

	got := &mongodbv1alpha1.MongoDBSharded{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "x", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.Status.Phase) != "Failed" {
		t.Fatalf("phase=Failed 기대, got %q", got.Status.Phase)
	}
	if got.Status.ObservedGeneration != 2 {
		t.Fatalf("에러 시 top-level ObservedGeneration=2 기대(kstatus InProgress 오판 방지), got %d", got.Status.ObservedGeneration)
	}
	ready := meta.FindStatusCondition(got.Status.Conditions, "Ready")
	if ready == nil || ready.Status != metav1.ConditionFalse {
		t.Fatalf("에러 시 Ready=False 기대(stale True 잔존 금지), got %+v", ready)
	}
	if ready.ObservedGeneration != 2 {
		t.Fatalf("Ready.ObservedGeneration=2 기대, got %d", ready.ObservedGeneration)
	}
	re := meta.FindStatusCondition(got.Status.Conditions, "ReconcileError")
	if re == nil || re.Status != metav1.ConditionTrue {
		t.Fatalf("ReconcileError=True 기대, got %+v", re)
	}
}
