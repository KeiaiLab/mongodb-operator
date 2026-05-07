/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package controller — generic 헬퍼들. 3 reconciler가 공유하는 패턴을 1곳에 통합.
//
// reconcileSecretIfNotExists  : Secret 멱등 생성 (RS/Sharded keyfile 공통)
// handleFinalizerCleanup       : deletionTimestamp + finalizer 패턴 공통
// Statusable + applyErrorCondition: ReconcileError condition + EventRecorder 발행 공통
package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// reconcileSecretIfNotExists는 Secret이 존재하지 않으면 build()로 생성한다.
// 존재 시 no-op (멱등). owner reference + scheme은 GC를 위해 필수.
//
// keyfile Secret처럼 *immutable* + *create-once* 자원에 사용.
func reconcileSecretIfNotExists(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	owner client.Object,
	secretName string,
	build func() *corev1.Secret,
) error {
	existing := &corev1.Secret{}
	err := c.Get(ctx, client.ObjectKey{Name: secretName, Namespace: owner.GetNamespace()}, existing)
	if err == nil {
		return nil // already exists
	}
	if !errors.IsNotFound(err) {
		return err
	}

	secret := build()
	if err := controllerutil.SetControllerReference(owner, secret, scheme); err != nil {
		return fmt.Errorf("set owner ref- %w", err)
	}
	return c.Create(ctx, secret)
}

// handleFinalizerCleanup는 deletionTimestamp가 설정된 객체에 대해
// (1) cleanup() 콜백 실행 → (2) finalizer 제거 → (3) Update 패턴을 처리한다.
//
// cleanup이 nil이면 finalizer 제거만. cleanup 에러 시 finalizer 유지(재시도 가능).
func handleFinalizerCleanup(
	ctx context.Context,
	c client.Client,
	obj client.Object,
	finalizer string,
	cleanup func(context.Context) error,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(obj, finalizer) {
		return ctrl.Result{}, nil
	}

	if cleanup != nil {
		if err := cleanup(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("finalizer cleanup- %w", err)
		}
	}

	controllerutil.RemoveFinalizer(obj, finalizer)
	if err := c.Update(ctx, obj); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// Statusable는 controller가 status conditions/phase를 추상화하기 위한 interface.
// 각 CR type(MongoDB, MongoDBSharded, MongoDBBackup)이 api/v1alpha1에 구현.
type Statusable interface {
	client.Object
	GetConditions() *[]metav1.Condition
	SetPhase(phase string)
}

// applyErrorCondition는 reconcile 에러 처리의 표준 패턴-
// (1) logger.Error 출력
// (2) EventRecorder Warning 이벤트 발행 (rec이 nil이면 skip)
// (3) Status.Phase = "Failed" + ReconcileError condition append (filter 후 1건 유지)
// (4) updateStatusWithRetry로 status 갱신
// (5) RequeueAfter 30s + err 반환
//
// 3 reconciler의 updateStatusError 본문 통합 — drift 위험 제거.
func applyErrorCondition(
	ctx context.Context,
	c client.Client,
	obj Statusable,
	component string,
	reconcileErr error,
	rec record.EventRecorder,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Error(reconcileErr, "Failed to reconcile component", "component", component)
	if rec != nil {
		rec.Eventf(obj, corev1.EventTypeWarning, conditionTypeReconcileError,
			"Failed to reconcile %s- %v", component, reconcileErr)
	}

	obj.SetPhase("Failed")
	conds := obj.GetConditions()
	*conds = filterConditionsByType(*conds, conditionTypeReconcileError)
	*conds = append(*conds, metav1.Condition{
		Type:               conditionTypeReconcileError,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "ReconcileFailed",
		Message:            fmt.Sprintf("Failed to reconcile %s- %v", component, reconcileErr),
	})

	if statusErr := updateStatusWithRetry(ctx, c, obj); statusErr != nil {
		logger.Error(statusErr, "Failed to update status")
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, reconcileErr
}

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
	conds = filterConditionsByType(conds, conditionTypeReconcileError)
	return append(conds, metav1.Condition{
		Type:               conditionTypeReconcileError,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "ReconcileSucceeded",
		Message:            "Last reconcile succeeded",
	})
}
