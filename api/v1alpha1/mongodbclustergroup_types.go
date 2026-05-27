/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// mongodbclustergroup_types.go — F56-F60 (cycle 8) 멀티 클러스터 관리 CRD.
//
// MongoDBFederation (cycle 5) 가 *단일 ReplicaSet 의 cross-cluster 분산* 인
// 반면, MongoDBClusterGroup 은 *여러 독립 cluster (다른 ReplicaSet / Sharded)*
// 의 *공통 정책 + 중앙 모니터링 + 전역 사용자* 를 reconcile.

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PolicyEnforcementType 은 ClusterGroup 정책 적용 방식을 정의하는 타입.
// +kubebuilder:validation:Enum=Enforce;Audit;Disabled
type PolicyEnforcementType string

// #227: ClusterGroup 정책 적용 수준 상수.
const (
	// PolicyEnforce 는 정책 위반 시 webhook reject (강제 모드).
	PolicyEnforce PolicyEnforcementType = "Enforce"

	// PolicyAudit 는 정책 위반을 condition/event 로 기록만 (감사 모드).
	PolicyAudit PolicyEnforcementType = "Audit"

	// PolicyDisabled 는 정책 검증 비활성화.
	PolicyDisabled PolicyEnforcementType = "Disabled"
)

// MongoDBClusterGroupSpec — F56 (cycle 8).
type MongoDBClusterGroupSpec struct {
	// Members lists the cluster targets controlled by this group.
	// +kubebuilder:validation:MinItems=1
	Members []ClusterGroupMember `json:"members"`

	// SharedAuth defines the *global user* policy applied to all members.
	// 전역 사용자가 각 cluster 에 동일 username / role / password secret 으로
	// 자동 등록.
	// +optional
	SharedAuth *ClusterGroupSharedAuth `json:"sharedAuth,omitempty"`

	// CentralMonitoring defines the central Grafana / Prometheus aggregation
	// destination (단일 control plane 의 통합 dashboard).
	// +optional
	CentralMonitoring *CentralMonitoringSpec `json:"centralMonitoring,omitempty"`

	// PolicyTemplate — 모든 member 에 강제 적용할 정책 (예: backup
	// frequency, retention 최소값). 각 member 가 override 가능하지만 본
	// template 보다 *약한* 정책은 webhook 에서 reject.
	// +optional
	PolicyTemplate *ClusterGroupPolicy `json:"policyTemplate,omitempty"`
}

// ClusterGroupMember — 한 cluster target.
type ClusterGroupMember struct {
	// Name human-readable identifier within the group.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Kind = MongoDB | MongoDBSharded — 어느 CRD 의 reference 인지.
	// +kubebuilder:validation:Enum=MongoDB;MongoDBSharded
	// +kubebuilder:default="MongoDB"
	Kind string `json:"kind,omitempty"`

	// NamespacedName references the target CR (same-cluster scenario) or
	// the kubeconfig (cross-cluster scenario).
	NamespacedName corev1.ObjectReference `json:"namespacedName"`

	// ClusterKubeConfigRef references the kubeconfig Secret for cross-
	// cluster members. nil → same-cluster (operator 가 직접 reconcile).
	// +optional
	ClusterKubeConfigRef *corev1.LocalObjectReference `json:"clusterKubeConfigRef,omitempty"`
}

// ClusterGroupSharedAuth — 전역 사용자 정책.
type ClusterGroupSharedAuth struct {
	// Users 가 각 member 에 reconcile 되는 user 목록.
	Users []MongoDBUser `json:"users,omitempty"`
}

// CentralMonitoringSpec — 중앙 모니터링 통합.
type CentralMonitoringSpec struct {
	// PrometheusRemoteWriteURL — 각 member 의 mongodb-exporter 가
	// remote write 로 metrics 를 보내는 endpoint.
	// +optional
	PrometheusRemoteWriteURL string `json:"prometheusRemoteWriteURL,omitempty"`

	// GrafanaURL — central Grafana instance (정합 dashboard 설치 hint).
	// +optional
	GrafanaURL string `json:"grafanaURL,omitempty"`

	// AlertmanagerURL — 통합 alert sink.
	// +optional
	AlertmanagerURL string `json:"alertmanagerURL,omitempty"`
}

// ClusterGroupPolicy — 각 member 에 강제 적용할 최소 정책.
type ClusterGroupPolicy struct {
	// MinBackupRetentionDays — member 의 BackupSpec.Retention.Days 가 이
	// 값 이상이어야 함. 작은 값으로 설정 시 webhook reject.
	// +kubebuilder:default=7
	MinBackupRetentionDays int32 `json:"minBackupRetentionDays,omitempty"`

	// RequiredTLSEnabled — 모든 member 가 TLS 활성 필수.
	// +kubebuilder:default=false
	RequiredTLSEnabled bool `json:"requiredTLSEnabled,omitempty"`

	// RequiredEncryptionAtRest — 모든 member 가 encryption-at-rest 필수.
	// +kubebuilder:default=false
	RequiredEncryptionAtRest bool `json:"requiredEncryptionAtRest,omitempty"`
}

// MongoDBClusterGroupStatus — group 단위 status.
type MongoDBClusterGroupStatus struct {
	// Phase: Pending | Reconciling | Synced | Degraded
	// +kubebuilder:validation:Enum=Pending;Reconciling;Synced;Degraded
	Phase string `json:"phase,omitempty"`

	// MemberStatuses 각 member 의 sync 결과.
	// +optional
	MemberStatuses []ClusterGroupMemberStatus `json:"memberStatuses,omitempty"`

	// Conditions follow standard k8s convention.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ClusterGroupMemberStatus — per-member 상태.
type ClusterGroupMemberStatus struct {
	Name  string `json:"name"`
	Phase string `json:"phase,omitempty"`
	// PolicyCompliant — member 가 policyTemplate 을 모두 만족하는지.
	PolicyCompliant bool   `json:"policyCompliant,omitempty"`
	Error           string `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mdbgroup
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Members",type="integer",JSONPath=".spec.members[*].name"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// MongoDBClusterGroup is the Schema for the mongodbclustergroups API.
type MongoDBClusterGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MongoDBClusterGroupSpec   `json:"spec,omitempty"`
	Status MongoDBClusterGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MongoDBClusterGroupList contains a list of MongoDBClusterGroup.
type MongoDBClusterGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDBClusterGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDBClusterGroup{}, &MongoDBClusterGroupList{})
}
