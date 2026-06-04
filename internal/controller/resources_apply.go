/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const deploymentRevisionAnnotation = "deployment.kubernetes.io/revision"

// 본 파일은 controller-runtime의 controllerutil.CreateOrUpdate를 도메인 타입별
// 로 감싸는 helper다. 자체 createOrUpdate(DeepCopy → SetResourceVersion → Update)
// 헬퍼는 호출자가 전달한 desired 객체의 spec을 그대로 보존하면서 update해야 했지만,
// 호출자가 partial mutation으로 desired를 만든 경우 *기존 spec이 손실*되는
// 위험을 안고 있었다. 이 위험을 mutateFn 패턴으로 차단한다 — 호출자는 어떤
// 필드를 업데이트할지 명시적으로 선언하고, immutable 필드는 Create 시점에만
// 채운다.
//
// 적용 범위: ConfigMap, Service, StatefulSet, Deployment.
// 적용 제외: keyfile Secret(immutable, 첫 생성만 — 자체 helper 유지),
// MongoDBBackup의 Job(create-only — 자체 helper 유지).

// applyConfigMap은 desired ConfigMap을 idempotent하게 적용한다.
// Data/BinaryData/Labels/Annotations만 갱신하며 owner reference를 설정한다.
func applyConfigMap(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner client.Object, desired *corev1.ConfigMap) error {
	target := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, c, target, func() error {
		target.Labels = desired.Labels
		target.Annotations = desired.Annotations
		target.Data = desired.Data
		target.BinaryData = desired.BinaryData
		return controllerutil.SetControllerReference(owner, target, scheme)
	})
	return err
}

// applyService는 desired Service를 idempotent하게 적용한다.
// Spec.ClusterIP는 immutable이므로 Create 시점에만 설정한다.
// Ports/Selector/Type/PublishNotReadyAddresses 등은 mutable.
func applyService(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner client.Object, desired *corev1.Service) error {
	target := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, c, target, func() error {
		target.Labels = desired.Labels
		target.Annotations = desired.Annotations
		// Create 시점: ClusterIP를 desired 그대로 (headless면 "None").
		// Update 시점: K8s가 할당한 값을 그대로 둠 (immutable).
		if target.CreationTimestamp.IsZero() {
			target.Spec.ClusterIP = desired.Spec.ClusterIP
			target.Spec.IPFamilies = desired.Spec.IPFamilies
			target.Spec.IPFamilyPolicy = desired.Spec.IPFamilyPolicy
		}
		target.Spec.Ports = desired.Spec.Ports
		target.Spec.Selector = desired.Spec.Selector
		target.Spec.Type = desired.Spec.Type
		target.Spec.PublishNotReadyAddresses = desired.Spec.PublishNotReadyAddresses
		target.Spec.SessionAffinity = desired.Spec.SessionAffinity
		target.Spec.SessionAffinityConfig = desired.Spec.SessionAffinityConfig
		// LoadBalancer/NodePort 관련 필드 — Mongos LB IP 변경, externalIPs 추가,
		// PROXY protocol 헬스 체크 NodePort 등 운영 변경이 첫 Create 후에도
		// 반영되어야 함. 기존 누락 시 LoadBalancerIP 영구 고착 P0 결함.
		target.Spec.LoadBalancerIP = desired.Spec.LoadBalancerIP
		target.Spec.LoadBalancerSourceRanges = desired.Spec.LoadBalancerSourceRanges
		target.Spec.LoadBalancerClass = desired.Spec.LoadBalancerClass
		target.Spec.AllocateLoadBalancerNodePorts = desired.Spec.AllocateLoadBalancerNodePorts
		target.Spec.ExternalIPs = desired.Spec.ExternalIPs
		target.Spec.ExternalName = desired.Spec.ExternalName
		target.Spec.ExternalTrafficPolicy = desired.Spec.ExternalTrafficPolicy
		target.Spec.HealthCheckNodePort = desired.Spec.HealthCheckNodePort
		target.Spec.InternalTrafficPolicy = desired.Spec.InternalTrafficPolicy
		return controllerutil.SetControllerReference(owner, target, scheme)
	})
	return err
}

// applyStatefulSet은 desired StatefulSet을 idempotent하게 적용한다.
// Selector/ServiceName/VolumeClaimTemplates 등 immutable 필드는 Create 시점에만 설정.
// Replicas/Template/UpdateStrategy/MinReadySeconds/PersistentVolumeClaimRetentionPolicy는 mutable.
//
// preserveReplicas=true 시 운영 중인 STS의 spec.Replicas를 desired 값으로 덮어쓰지
// 않는다. 두 가지 경우에 사용:
//   - HPA가 활성화돼 HPA controller가 Replicas를 자체 patch하는 경우.
//   - ScalePolicy.Deliberate=false 가드로 spec.Members 변경이 보류된 경우.
//
// 첫 Create 시점에는 desired.Spec.Replicas를 그대로 사용해 첫 deploy를 막지 않는다.
func applyStatefulSet(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner client.Object, desired *appsv1.StatefulSet, preserveReplicas bool) error {
	target := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, c, target, func() error {
		target.Labels = desired.Labels
		target.Annotations = desired.Annotations
		if target.CreationTimestamp.IsZero() {
			target.Spec = desired.Spec
		} else {
			// 운영 중인 STS는 immutable 필드(Selector/ServiceName/
			// VolumeClaimTemplates/PodManagementPolicy)를 그대로 두고
			// 변경 가능한 필드만 동기화.
			if !preserveReplicas {
				target.Spec.Replicas = desired.Spec.Replicas
			}
			target.Spec.Template = desired.Spec.Template
			target.Spec.UpdateStrategy = desired.Spec.UpdateStrategy
			target.Spec.MinReadySeconds = desired.Spec.MinReadySeconds
			target.Spec.PersistentVolumeClaimRetentionPolicy = desired.Spec.PersistentVolumeClaimRetentionPolicy
		}
		return controllerutil.SetControllerReference(owner, target, scheme)
	})
	return err
}

// applyNetworkPolicy는 desired NetworkPolicy를 idempotent하게 적용한다.
// PodSelector/PolicyTypes/Ingress/Egress를 매번 desired 그대로 동기화 — RS
// 멤버 라벨/포트가 spec 변경에 따라 갱신되어도 추적 가능.
func applyNetworkPolicy(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner client.Object, desired *networkingv1.NetworkPolicy) error {
	target := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, c, target, func() error {
		target.Labels = desired.Labels
		target.Annotations = desired.Annotations
		target.Spec.PodSelector = desired.Spec.PodSelector
		target.Spec.PolicyTypes = desired.Spec.PolicyTypes
		target.Spec.Ingress = desired.Spec.Ingress
		target.Spec.Egress = desired.Spec.Egress
		return controllerutil.SetControllerReference(owner, target, scheme)
	})
	return err
}

// applyPDB는 desired PodDisruptionBudget을 idempotent하게 적용한다.
// MinAvailable/MaxUnavailable은 K8s 제약상 둘 중 하나만 설정 가능하므로 desired
// 그대로 mutate(둘 중 하나만 non-nil이라는 호출자 책임).
func applyPDB(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner client.Object, desired *policyv1.PodDisruptionBudget) error {
	target := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, c, target, func() error {
		target.Labels = desired.Labels
		target.Annotations = desired.Annotations
		target.Spec.Selector = desired.Spec.Selector
		target.Spec.MinAvailable = desired.Spec.MinAvailable
		target.Spec.MaxUnavailable = desired.Spec.MaxUnavailable
		return controllerutil.SetControllerReference(owner, target, scheme)
	})
	return err
}

// applyDeployment는 desired Deployment를 idempotent하게 적용한다.
// Selector는 immutable이므로 Create 시점에만 설정한다.
//
// preserveReplicas=true 시 운영 중인 Deployment의 spec.Replicas를 보존한다 — HPA
// controller의 patch와 operator의 reconcile이 충돌하지 않게 함(ADR-0007).
//
// v1.4.2 P0 fix: RevisionHistoryLimit / ProgressDeadlineSeconds 는 K8s Deployment
// 컨트롤러가 자동으로 서버 기본값(10, 600)을 채우는 *pointer 필드*다. 빌더가 nil 을
// desired 로 넘기면 매 reconcile 마다 nil 로 덮어씌워지고, K8s 가 즉시 기본값을
// 재주입 → 무한 ping-pong (mongos Deployment generation 116k+ 재현). desired 가
// 비어 있으면 운영 중인 객체의 server-defaulted 값을 그대로 두어 fight 차단.
func applyDeployment(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner client.Object, desired *appsv1.Deployment, preserveReplicas bool) error {
	target := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, c, target, func() error {
		target.Labels = desired.Labels
		target.Annotations = deploymentAnnotationsWithControllerRevision(desired.Annotations, target.Annotations)
		if target.CreationTimestamp.IsZero() {
			target.Spec = desired.Spec
		} else {
			currentTemplate := target.Spec.Template.DeepCopy()
			if !preserveReplicas {
				target.Spec.Replicas = desired.Spec.Replicas
			}
			target.Spec.Template = deploymentTemplateWithServerDefaults(desired.Spec.Template, *currentTemplate)
			target.Spec.Strategy = desired.Spec.Strategy
			target.Spec.MinReadySeconds = desired.Spec.MinReadySeconds
			// nil-guard: desired 가 비어 있으면 server-default 보존.
			if desired.Spec.RevisionHistoryLimit != nil {
				target.Spec.RevisionHistoryLimit = desired.Spec.RevisionHistoryLimit
			}
			if desired.Spec.ProgressDeadlineSeconds != nil {
				target.Spec.ProgressDeadlineSeconds = desired.Spec.ProgressDeadlineSeconds
			}
			target.Spec.Paused = desired.Spec.Paused
		}
		return controllerutil.SetControllerReference(owner, target, scheme)
	})
	return err
}

func deploymentAnnotationsWithControllerRevision(desired, current map[string]string) map[string]string {
	if len(desired) == 0 && current[deploymentRevisionAnnotation] == "" {
		return desired
	}
	out := make(map[string]string, len(desired)+1)
	for key, value := range desired {
		out[key] = value
	}
	if _, ok := out[deploymentRevisionAnnotation]; !ok {
		if revision := current[deploymentRevisionAnnotation]; revision != "" {
			out[deploymentRevisionAnnotation] = revision
		}
	}
	return out
}

func deploymentTemplateWithServerDefaults(desired, current corev1.PodTemplateSpec) corev1.PodTemplateSpec {
	desiredCopy := desired.DeepCopy()
	out := *current.DeepCopy()
	out.ObjectMeta = desiredCopy.ObjectMeta
	out.Spec.SecurityContext = desiredCopy.Spec.SecurityContext
	out.Spec.InitContainers = desiredCopy.Spec.InitContainers
	out.Spec.Containers = desiredCopy.Spec.Containers
	out.Spec.Volumes = desiredCopy.Spec.Volumes
	out.Spec.Affinity = desiredCopy.Spec.Affinity
	out.Spec.NodeSelector = desiredCopy.Spec.NodeSelector
	out.Spec.Tolerations = desiredCopy.Spec.Tolerations
	out.Spec.TopologySpreadConstraints = desiredCopy.Spec.TopologySpreadConstraints
	out.Spec.ImagePullSecrets = desiredCopy.Spec.ImagePullSecrets
	out.Spec.ServiceAccountName = desiredCopy.Spec.ServiceAccountName
	out.Spec.PriorityClassName = desiredCopy.Spec.PriorityClassName
	out.Spec.RuntimeClassName = desiredCopy.Spec.RuntimeClassName
	out.Spec.DNSConfig = desiredCopy.Spec.DNSConfig
	out.Spec.HostAliases = desiredCopy.Spec.HostAliases
	preservePodSpecServerDefaults(&out.Spec, current.Spec)
	return out
}

func preservePodSpecServerDefaults(desired *corev1.PodSpec, current corev1.PodSpec) {
	if desired.RestartPolicy == "" {
		desired.RestartPolicy = current.RestartPolicy
	}
	if desired.DNSPolicy == "" {
		desired.DNSPolicy = current.DNSPolicy
	}
	if desired.SchedulerName == "" {
		desired.SchedulerName = current.SchedulerName
	}
	if desired.TerminationGracePeriodSeconds == nil {
		desired.TerminationGracePeriodSeconds = current.TerminationGracePeriodSeconds
	}
	preserveContainerListServerDefaults(desired.Containers, current.Containers)
	preserveContainerListServerDefaults(desired.InitContainers, current.InitContainers)
}

func preserveContainerListServerDefaults(desired, current []corev1.Container) {
	currentByName := map[string]corev1.Container{}
	for _, container := range current {
		currentByName[container.Name] = container
	}
	for i := range desired {
		if currentContainer, ok := currentByName[desired[i].Name]; ok {
			preserveContainerServerDefaults(&desired[i], currentContainer)
		}
	}
}

func preserveContainerServerDefaults(desired *corev1.Container, current corev1.Container) {
	if desired.ImagePullPolicy == "" {
		desired.ImagePullPolicy = current.ImagePullPolicy
	}
	if desired.TerminationMessagePath == "" {
		desired.TerminationMessagePath = current.TerminationMessagePath
	}
	if desired.TerminationMessagePolicy == "" {
		desired.TerminationMessagePolicy = current.TerminationMessagePolicy
	}
	preserveProbeServerDefaults(desired.LivenessProbe, current.LivenessProbe)
	preserveProbeServerDefaults(desired.ReadinessProbe, current.ReadinessProbe)
	preserveProbeServerDefaults(desired.StartupProbe, current.StartupProbe)
	preserveContainerPortServerDefaults(desired.Ports, current.Ports)
}

func preserveProbeServerDefaults(desired, current *corev1.Probe) {
	if desired == nil || current == nil {
		return
	}
	if desired.TimeoutSeconds == 0 {
		desired.TimeoutSeconds = current.TimeoutSeconds
	}
	if desired.PeriodSeconds == 0 {
		desired.PeriodSeconds = current.PeriodSeconds
	}
	if desired.SuccessThreshold == 0 {
		desired.SuccessThreshold = current.SuccessThreshold
	}
	if desired.FailureThreshold == 0 {
		desired.FailureThreshold = current.FailureThreshold
	}
	if desired.TerminationGracePeriodSeconds == nil {
		desired.TerminationGracePeriodSeconds = current.TerminationGracePeriodSeconds
	}
}

func preserveContainerPortServerDefaults(desired, current []corev1.ContainerPort) {
	currentByName := map[string]corev1.ContainerPort{}
	for _, port := range current {
		currentByName[port.Name] = port
	}
	for i := range desired {
		if desired[i].Protocol != "" {
			continue
		}
		if currentPort, ok := currentByName[desired[i].Name]; ok {
			desired[i].Protocol = currentPort.Protocol
		}
	}
}
