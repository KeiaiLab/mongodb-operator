/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

// 낙관적 동시성 충돌(optimistic concurrency, "the object has been modified")
// 재시도 회귀 테스트. 운영 중 StatefulSet 의 status/메타가 churn 하는 동안
// reconcile 의 apply 가 conflict 로 실패해 ReconcileError 가 반복 기록되던
// P0 결함(샤드/cfg StatefulSet generation fight)을 고정한다.
package controller

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// TestApplyStatefulSet_RetriesOnConflict 는 첫 Update 가 Conflict 로 실패해도
// apply 헬퍼가 fresh GET 으로 재시도해 결국 성공해야 함을 검증한다.
// 재시도가 없으면(현행) 단일 conflict 가 그대로 reconcile 에러로 전파된다.
func TestApplyStatefulSet_RetriesOnConflict(t *testing.T) {
	s := newApplyScheme(t)
	owner := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "shard", Namespace: "ns"},
	}
	existing := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "shard",
			Namespace:         "ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    ptr32(3),
			ServiceName: "shard-headless",
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{"app": "shard"}},
			Template:    podTemplateSpec("shard"),
		},
	}

	var updateCalls int
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(owner, existing).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				updateCalls++
				if updateCalls == 1 {
					// 실제 API server 가 반환하는 메시지를 모사.
					return apierrors.NewConflict(
						schema.GroupResource{Group: "apps", Resource: "statefulsets"},
						obj.GetName(),
						errors.New("the object has been modified; please apply your changes to the latest version and try again"),
					)
				}
				return c.Update(ctx, obj, opts...)
			},
		}).Build()

	desired := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shard",
			Namespace: "ns",
			Labels:    map[string]string{"sync": "v2"}, // mutate 가 Update 를 유발하도록 변경 포함
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    ptr32(3),
			ServiceName: "shard-headless",
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{"app": "shard"}},
			Template:    podTemplateSpec("shard"),
		},
	}

	if err := applyStatefulSet(context.Background(), cl, s, owner, desired, false); err != nil {
		t.Fatalf("applyStatefulSet 는 단일 conflict 를 재시도로 통과해야 한다, got: %v", err)
	}
	if updateCalls < 2 {
		t.Fatalf("conflict 후 재시도(Update >=2회) 기대, got %d", updateCalls)
	}

	got := &appsv1.StatefulSet{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "shard", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get sts: %v", err)
	}
	if got.Labels["sync"] != "v2" {
		t.Fatalf("재시도 후 desired 라벨(sync=v2)이 적용돼야 한다, got %v", got.Labels)
	}
}
