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
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func ptr32(v int32) *int32 { return &v }

func ptr64(v int64) *int64 { return &v }

func ptrBool(v bool) *bool { return &v }

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

// TestApplyDeployment_IdempotentWithServerDefaults 는 v1.4.2 P0 회귀 테스트다.
// 빌더가 RevisionHistoryLimit / ProgressDeadlineSeconds 를 nil 로 두고 K8s 가
// 서버 기본값(10/600)을 재주입한 운영 중 Deployment 에 대해 apply 를 2회 호출했을
// 때 server-defaulted 값이 그대로 보존되는지 검증한다. 보존 안 되면 controller
// 와 K8s defaulter 사이 무한 fight 발생 (mongos Deployment generation 116k+).
func TestApplyDeployment_IdempotentWithServerDefaults(t *testing.T) {
	s := newApplyScheme(t)
	owner := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "mongos", Namespace: "ns"},
	}
	// 운영 중 Deployment: K8s 가 server default 를 채워둔 상태.
	existing := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mongos", Namespace: "ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas:                ptr32(3),
			Selector:                &metav1.LabelSelector{MatchLabels: map[string]string{"app": "mongos"}},
			Template:                podTemplateSpec("mongos"),
			RevisionHistoryLimit:    ptr32(10),  // K8s server default
			ProgressDeadlineSeconds: ptr32(600), // K8s server default
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(owner, existing).Build()

	// 빌더 출력: server-defaulted pointer 필드는 nil.
	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "mongos", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr32(3),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "mongos"}},
			Template: podTemplateSpec("mongos"),
			// RevisionHistoryLimit / ProgressDeadlineSeconds 의도적 nil
		},
	}

	// 1차 apply: 서버 기본값 보존되어야 함.
	if err := applyDeployment(context.Background(), cl, s, owner, desired, false); err != nil {
		t.Fatalf("applyDeployment 1: %v", err)
	}
	got := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "mongos", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get deploy: %v", err)
	}
	if got.Spec.RevisionHistoryLimit == nil || *got.Spec.RevisionHistoryLimit != 10 {
		t.Fatalf("기대 RevisionHistoryLimit=10 (server default 보존), got=%v", got.Spec.RevisionHistoryLimit)
	}
	if got.Spec.ProgressDeadlineSeconds == nil || *got.Spec.ProgressDeadlineSeconds != 600 {
		t.Fatalf("기대 ProgressDeadlineSeconds=600 (server default 보존), got=%v", got.Spec.ProgressDeadlineSeconds)
	}

	// 2차 apply: 멱등 — spec 변동 없어야 함 (generation-bump 시뮬레이션).
	rv1 := got.ResourceVersion
	if err := applyDeployment(context.Background(), cl, s, owner, desired, false); err != nil {
		t.Fatalf("applyDeployment 2: %v", err)
	}
	got2 := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "mongos", Namespace: "ns"}, got2); err != nil {
		t.Fatalf("get deploy 2: %v", err)
	}
	if got2.Spec.RevisionHistoryLimit == nil || *got2.Spec.RevisionHistoryLimit != 10 {
		t.Fatalf("2차: 기대 RevisionHistoryLimit=10, got=%v", got2.Spec.RevisionHistoryLimit)
	}
	// fake client 는 spec 변경 없으면 ResourceVersion 을 bump 하지 않음 → 멱등 증거.
	if got2.ResourceVersion != rv1 {
		t.Fatalf("기대 멱등 (ResourceVersion 불변), 1차=%s 2차=%s — fight 재현",
			rv1, got2.ResourceVersion)
	}
}

// TestApplyDeployment_IdempotentWithPodTemplateServerDefaults 는 v1.4.8 회귀 테스트다.
// K8s가 PodTemplate 내부 기본값(imagePullPolicy/probe thresholds/DNS/restart 등)을
// 채운 운영 중 Deployment에 대해 operator가 빈 desired 값으로 되돌리지 않아야 한다.
func TestApplyDeployment_IdempotentWithPodTemplateServerDefaults(t *testing.T) {
	s := newApplyScheme(t)
	owner := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "mongos", Namespace: "ns"},
	}

	existingTemplate := podTemplateSpec("mongos")
	existingTemplate.Spec.RestartPolicy = corev1.RestartPolicyAlways
	existingTemplate.Spec.DNSPolicy = corev1.DNSClusterFirst
	existingTemplate.Spec.SchedulerName = corev1.DefaultSchedulerName
	existingTemplate.Spec.TerminationGracePeriodSeconds = ptr64(30)
	existingTemplate.Spec.EnableServiceLinks = ptrBool(true)
	existingTemplate.Spec.Containers[0].ImagePullPolicy = corev1.PullIfNotPresent
	existingTemplate.Spec.Containers[0].TerminationMessagePath = corev1.TerminationMessagePathDefault
	existingTemplate.Spec.Containers[0].TerminationMessagePolicy = corev1.TerminationMessageReadFile
	existingTemplate.Spec.Containers[0].Ports = []corev1.ContainerPort{
		{Name: "mongodb", ContainerPort: 27017, Protocol: corev1.ProtocolTCP},
	}
	existingTemplate.Spec.Containers[0].LivenessProbe = &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(27017)}},
		InitialDelaySeconds: 30,
		PeriodSeconds:       10,
		TimeoutSeconds:      1,
		SuccessThreshold:    1,
		FailureThreshold:    3,
	}
	existingTemplate.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"mongosh", "--eval", "db.adminCommand('ping')"}}},
		InitialDelaySeconds: 10,
		PeriodSeconds:       10,
		TimeoutSeconds:      5,
		SuccessThreshold:    1,
		FailureThreshold:    3,
	}

	existing := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mongos", Namespace: "ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr32(3),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "mongos"}},
			Template: existingTemplate,
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(owner, existing).Build()

	desiredTemplate := podTemplateSpec("mongos")
	desiredTemplate.Spec.Containers[0].Ports = []corev1.ContainerPort{
		{Name: "mongodb", ContainerPort: 27017},
	}
	desiredTemplate.Spec.Containers[0].LivenessProbe = &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(27017)}},
		InitialDelaySeconds: 30,
		PeriodSeconds:       10,
	}
	desiredTemplate.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"mongosh", "--eval", "db.adminCommand('ping')"}}},
		InitialDelaySeconds: 10,
		PeriodSeconds:       10,
		TimeoutSeconds:      5,
	}
	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "mongos", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr32(3),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "mongos"}},
			Template: desiredTemplate,
		},
	}

	if err := applyDeployment(context.Background(), cl, s, owner, desired, false); err != nil {
		t.Fatalf("applyDeployment 1: %v", err)
	}
	got := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "mongos", Namespace: "ns"}, got); err != nil {
		t.Fatalf("get deploy: %v", err)
	}
	if got.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyAlways {
		t.Fatalf("RestartPolicy default 보존 실패: %q", got.Spec.Template.Spec.RestartPolicy)
	}
	if got.Spec.Template.Spec.Containers[0].ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("ImagePullPolicy default 보존 실패: %q", got.Spec.Template.Spec.Containers[0].ImagePullPolicy)
	}
	if got.Spec.Template.Spec.Containers[0].Ports[0].Protocol != corev1.ProtocolTCP {
		t.Fatalf("port protocol default 보존 실패: %q", got.Spec.Template.Spec.Containers[0].Ports[0].Protocol)
	}
	if got.Spec.Template.Spec.Containers[0].LivenessProbe.FailureThreshold != 3 {
		t.Fatalf("liveness FailureThreshold default 보존 실패: %d", got.Spec.Template.Spec.Containers[0].LivenessProbe.FailureThreshold)
	}
	if got.Spec.Template.Spec.EnableServiceLinks == nil || !*got.Spec.Template.Spec.EnableServiceLinks {
		t.Fatalf("EnableServiceLinks default 보존 실패: %v", got.Spec.Template.Spec.EnableServiceLinks)
	}

	rv1 := got.ResourceVersion
	if err := applyDeployment(context.Background(), cl, s, owner, desired, false); err != nil {
		t.Fatalf("applyDeployment 2: %v", err)
	}
	got2 := &appsv1.Deployment{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: "mongos", Namespace: "ns"}, got2); err != nil {
		t.Fatalf("get deploy 2: %v", err)
	}
	if got2.ResourceVersion != rv1 {
		t.Fatalf("기대 PodTemplate server-default 멱등, 1차=%s 2차=%s", rv1, got2.ResourceVersion)
	}
}

// podTemplateSpec은 테스트용 최소 PodTemplateSpec을 반환한다.
func podTemplateSpec(app string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": app}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "mongo", Image: "mongo:7.0"}}},
	}
}
