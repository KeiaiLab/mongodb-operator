/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// preserveReplicas 분기 단위 테스트 — envtest 불필요, fake client만 사용.
// HPA 활성 또는 deliberate=false 가드 상황에서 STS/Deployment의 spec.Replicas가
// operator reconcile로 덮어씌워지지 않는지 검증한다.
package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func ptr32(v int32) *int32 { return &v }

func newApplyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatalf("add appsv1 scheme: %v", err)
	}
	if err := mongodbv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add mongodb scheme: %v", err)
	}
	return s
}

// TestApplyStatefulSet_PreserveReplicas_True는 preserveReplicas=true 시 HPA가
// 조정한 운영 중 STS replicas(4)가 desired(3)로 덮어씌워지지 않는지 검증한다.
func TestApplyStatefulSet_PreserveReplicas_True(t *testing.T) {
	s := newApplyScheme(t)
	owner := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
	}

	// 운영 중 STS: HPA가 4로 scale-out한 상태.
	existing := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rs",
			Namespace: "ns",
			// fake client는 CreationTimestamp를 자동 설정하지 않으므로 직접 지정.
			CreationTimestamp: metav1.Now(),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    ptr32(4),
			ServiceName: "rs-headless",
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{"app": "rs"}},
			Template:    podTemplateSpec("rs"),
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(owner, existing).Build()

	desired := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    ptr32(3), // spec.Members=3, but HPA override 보존해야 함
			ServiceName: "rs-headless",
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{"app": "rs"}},
			Template:    podTemplateSpec("rs"),
		},
	}

	if err := applyStatefulSet(context.Background(), cl, s, owner, desired, true); err != nil {
		t.Fatalf("applyStatefulSet: %v", err)
	}

	got := &appsv1.StatefulSet{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "rs", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get sts: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 4 {
		t.Fatalf("기대 replicas=4 (HPA 보존), got=%v", got.Spec.Replicas)
	}
}

// TestApplyStatefulSet_PreserveReplicas_False는 preserveReplicas=false 시
// desired replicas(3)가 운영 중 STS(4)에 정상 적용되는지 검증한다.
func TestApplyStatefulSet_PreserveReplicas_False(t *testing.T) {
	s := newApplyScheme(t)
	owner := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
	}
	existing := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rs", Namespace: "ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    ptr32(4),
			ServiceName: "rs-headless",
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{"app": "rs"}},
			Template:    podTemplateSpec("rs"),
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(owner, existing).Build()

	desired := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    ptr32(3),
			ServiceName: "rs-headless",
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{"app": "rs"}},
			Template:    podTemplateSpec("rs"),
		},
	}

	if err := applyStatefulSet(context.Background(), cl, s, owner, desired, false); err != nil {
		t.Fatalf("applyStatefulSet: %v", err)
	}

	got := &appsv1.StatefulSet{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "rs", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get sts: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 3 {
		t.Fatalf("기대 replicas=3 (desired 적용), got=%v", got.Spec.Replicas)
	}
}

// TestApplyDeployment_PreserveReplicas_True는 Deployment에서 preserveReplicas=true
// 시 HPA가 조정한 운영 중 replicas(5)가 보존되는지 검증한다.
func TestApplyDeployment_PreserveReplicas_True(t *testing.T) {
	s := newApplyScheme(t)
	owner := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "mongos", Namespace: "ns"},
	}
	existing := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mongos", Namespace: "ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr32(5),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "mongos"}},
			Template: podTemplateSpec("mongos"),
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(owner, existing).Build()

	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "mongos", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr32(2),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "mongos"}},
			Template: podTemplateSpec("mongos"),
		},
	}

	if err := applyDeployment(context.Background(), cl, s, owner, desired, true); err != nil {
		t.Fatalf("applyDeployment: %v", err)
	}

	got := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "mongos", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get deploy: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 5 {
		t.Fatalf("기대 replicas=5 (HPA 보존), got=%v", got.Spec.Replicas)
	}
}

// podTemplateSpec은 테스트용 최소 PodTemplateSpec을 반환한다.
func podTemplateSpec(app string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": app}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "mongo", Image: "mongo:7.0"}}},
	}
}
