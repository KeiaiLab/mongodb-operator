/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

// 본 파일은 envtest 없이 fake client interceptor만으로 updateStatusWithRetry의
// conflict 재적용(mutate-after-get) 동작을 검증한다. 첫 Status().Update 호출에
// 1회 conflict를 주입해, refetch 이후 호출자의 status mutation이 mutate 콜백으로
// 재적용되어 최종 status에 반영되는지를 확인한다 (silent 유실 회귀 가드).
package controller

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// newConflictOnceInterceptor는 SubResourceUpdate(=status update)에 대해 첫 호출에서만
// conflict 에러를 1회 주입하고, 이후 호출은 실제 fake client로 위임하는 interceptor를
// 반환한다. 반환된 포인터로 주입 발생 여부를 검사할 수 있다.
func newConflictOnceInterceptor() (interceptor.Funcs, *int) {
	calls := 0
	funcs := interceptor.Funcs{
		SubResourceUpdate: func(
			ctx context.Context,
			c client.Client,
			subResourceName string,
			obj client.Object,
			opts ...client.SubResourceUpdateOption,
		) error {
			calls++
			if calls == 1 {
				// 첫 status update에 conflict 강제 — 호출자가 보유한 RV가 stale인 상황 모사.
				gr := schema.GroupResource{Group: "mongodb.keiailab.com", Resource: "mongodbs"}
				return apierrors.NewConflict(gr, obj.GetName(), nil)
			}
			// 두 번째부터는 실제 fake client subresource client로 위임.
			return c.Status().Update(ctx, obj, opts...)
		},
	}
	return funcs, &calls
}

// TestUpdateStatusWithRetry_MutateReappliedAfterConflict는 conflict 발생 후 refetch가
// 호출자의 status 변경을 덮어쓰더라도, mutate 콜백이 재적용되어 최종 status에 호출자
// 값(Phase=Running)이 반영됨을 검증한다. 수정 전 구현은 refetch만 하고 재적용하지 않아
// 이 값이 silent하게 유실됐다(red → green).
func TestUpdateStatusWithRetry_MutateReappliedAfterConflict(t *testing.T) {
	s := newTestScheme(t)

	// 서버에 저장된 초기 상태: Phase는 비어 있음.
	stored := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        3,
			ReplicaSetName: "rs0",
		},
	}

	funcs, calls := newConflictOnceInterceptor()
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(stored).
		WithStatusSubresource(stored).
		WithInterceptorFuncs(funcs).
		Build()

	// 호출자는 obj를 fetch한 뒤 Phase를 Running으로 mutate한다.
	obj := &mongodbv1alpha1.MongoDB{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(stored), obj); err != nil {
		t.Fatalf("초기 fetch 실패: %v", err)
	}
	mutate := func() {
		obj.Status.Phase = mongodbv1alpha1.PhaseRunning
	}
	mutate()

	if err := updateStatusWithRetry(context.Background(), cl, obj, mutate); err != nil {
		t.Fatalf("updateStatusWithRetry 실패: %v", err)
	}

	// conflict 1회 주입 + 재시도 1회 = 최소 2회 status update가 발생해야 한다.
	if *calls < 2 {
		t.Fatalf("기대: status update 2회 이상(conflict 주입 검증), got=%d", *calls)
	}

	// 서버에서 다시 fetch해 호출자 값이 영속화됐는지 확인.
	got := &mongodbv1alpha1.MongoDB{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(stored), got); err != nil {
		t.Fatalf("최종 fetch 실패: %v", err)
	}
	if got.Status.Phase != mongodbv1alpha1.PhaseRunning {
		t.Fatalf("기대 phase=Running(mutate 재적용), got=%q — conflict 후 status mutation 유실", got.Status.Phase)
	}
}

// TestUpdateStatusWithRetry_NoMutateBackwardCompatible는 mutate 콜백 없이 호출하는
// 기존 호출자가 여전히 정상 동작함을 검증한다. conflict 없는 정상 경로에서 호출자가
// 설정한 status가 그대로 영속화돼야 한다(점진 마이그레이션 호환).
func TestUpdateStatusWithRetry_NoMutateBackwardCompatible(t *testing.T) {
	s := newTestScheme(t)

	stored := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs-compat", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        3,
			ReplicaSetName: "rs0",
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(stored).
		WithStatusSubresource(stored).
		Build()

	obj := &mongodbv1alpha1.MongoDB{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(stored), obj); err != nil {
		t.Fatalf("초기 fetch 실패: %v", err)
	}
	obj.Status.Phase = mongodbv1alpha1.PhaseInitializing

	// 가변 인자 미지정 — 기존 3-arg 호출과 동일.
	if err := updateStatusWithRetry(context.Background(), cl, obj); err != nil {
		t.Fatalf("updateStatusWithRetry(no mutate) 실패: %v", err)
	}

	got := &mongodbv1alpha1.MongoDB{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(stored), got); err != nil {
		t.Fatalf("최종 fetch 실패: %v", err)
	}
	if got.Status.Phase != mongodbv1alpha1.PhaseInitializing {
		t.Fatalf("기대 phase=Initializing(정상 경로 영속화), got=%q", got.Status.Phase)
	}
}
