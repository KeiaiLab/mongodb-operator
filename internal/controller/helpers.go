/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Package controller — generic 헬퍼들.
//
// Secret 멱등 생성 / finalizer cleanup / Statusable + 에러 condition 표준 처리는
// keiailab-commons pkg/reconcile 로 승격 (v0.11.0) — 콜사이트가 직접
// commonsreconcile.SecretIfNotExists / HandleFinalizerCleanup /
// ApplyErrorCondition 을 호출한다. 본 파일에는 mongodb 단일 repo 사용 헬퍼
// (clearReconcileErrorCondition) 만 잔류.
package controller

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	conditionTypeReady          = "Ready"
	conditionTypeReconcileError = "ReconcileError"
)

func clearReconcileErrorCondition(conds []metav1.Condition, generation int64) []metav1.Condition {
	found := false
	for _, cond := range conds {
		if cond.Type == conditionTypeReconcileError {
			found = true
			break
		}
	}
	if !found {
		return conds
	}
	// iteration 33 (ADR-0013): upstream meta.SetStatusCondition 위임. 기존
	// True → False transition 시 LastTransitionTime 정확 갱신 (K8s convention).
	// upstream 은 *Status 변경 감지 시만* 갱신하고 미변경 시 보존.
	meta.SetStatusCondition(&conds, metav1.Condition{
		Type:               conditionTypeReconcileError,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		Reason:             mongodbv1alpha1.ReasonReconcileSucceeded,
		Message:            "Last reconcile succeeded",
	})
	return conds
}
