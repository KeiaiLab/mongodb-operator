/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
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
func applyStatefulSet(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner client.Object, desired *appsv1.StatefulSet) error {
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
			target.Spec.Replicas = desired.Spec.Replicas
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
func applyDeployment(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner client.Object, desired *appsv1.Deployment) error {
	target := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, c, target, func() error {
		target.Labels = desired.Labels
		target.Annotations = desired.Annotations
		if target.CreationTimestamp.IsZero() {
			target.Spec = desired.Spec
		} else {
			target.Spec.Replicas = desired.Spec.Replicas
			target.Spec.Template = desired.Spec.Template
			target.Spec.Strategy = desired.Spec.Strategy
			target.Spec.MinReadySeconds = desired.Spec.MinReadySeconds
			target.Spec.RevisionHistoryLimit = desired.Spec.RevisionHistoryLimit
			target.Spec.ProgressDeadlineSeconds = desired.Spec.ProgressDeadlineSeconds
			target.Spec.Paused = desired.Spec.Paused
		}
		return controllerutil.SetControllerReference(owner, target, scheme)
	})
	return err
}
