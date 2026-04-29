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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MongoDBShardedPhase represents the lifecycle phase of a MongoDBSharded resource.
// +kubebuilder:validation:Enum=Pending;Initializing;Running;Failed;Upgrading
type MongoDBShardedPhase string

const (
	// ShardedPhasePending indicates the resource is awaiting reconciliation.
	ShardedPhasePending MongoDBShardedPhase = "Pending"
	// ShardedPhaseInitializing indicates the cluster is being initialized.
	ShardedPhaseInitializing MongoDBShardedPhase = "Initializing"
	// ShardedPhaseRunning indicates the sharded cluster is fully operational.
	ShardedPhaseRunning MongoDBShardedPhase = "Running"
	// ShardedPhaseFailed indicates the resource entered a failed state.
	ShardedPhaseFailed MongoDBShardedPhase = "Failed"
	// ShardedPhaseUpgrading indicates the cluster is undergoing a version upgrade.
	ShardedPhaseUpgrading MongoDBShardedPhase = "Upgrading"
)

// MongoDBShardedSpec defines the desired state of MongoDBSharded
type MongoDBShardedSpec struct {
	// Version defines MongoDB version configuration
	Version MongoDBVersion `json:"version"`

	// ConfigServer defines config server configuration
	ConfigServer ConfigServerSpec `json:"configServer"`

	// Shards defines shard configuration
	Shards ShardSpec `json:"shards"`

	// Mongos defines mongos router configuration
	Mongos MongosSpec `json:"mongos"`

	// TLS defines TLS configuration
	// +optional
	TLS *TLSSpec `json:"tls,omitempty"`

	// Auth defines authentication configuration
	Auth AuthSpec `json:"auth"`

	// Monitoring defines monitoring configuration
	// +optional
	Monitoring *MonitoringSpec `json:"monitoring,omitempty"`

	// Backup defines backup configuration
	// +optional
	Backup *BackupSpec `json:"backup,omitempty"`

	// AdditionalConfig allows passing additional MongoDB configuration
	// +optional
	AdditionalConfig map[string]string `json:"additionalConfig,omitempty"`

	// PodDisruptionBudget defines PDB configuration for cfg/shard/mongos
	// workloads. Enabled=true 시 모든 컴포넌트에 동일 정책 적용 (기본
	// minAvailable = replicas-1).
	// +optional
	PodDisruptionBudget *PodDisruptionBudgetSpec `json:"podDisruptionBudget,omitempty"`

	// NetworkPolicy defines NetworkPolicy configuration. 기본 비활성(opt-in).
	// 활성화 시 cfg(27019)/shard(27018)/mongos(27017) 컴포넌트별 deny-by-default.
	// +optional
	NetworkPolicy *NetworkPolicySpec `json:"networkPolicy,omitempty"`
}

// ConfigServerSpec defines config server configuration
type ConfigServerSpec struct {
	// Members is the number of config server replica set members
	// +kubebuilder:validation:Enum=1;3
	// +kubebuilder:default=3
	Members int32 `json:"members"`

	// Storage defines storage configuration
	// +optional
	Storage StorageSpec `json:"storage,omitempty"`

	// Resources defines resource requirements
	// +optional
	Resources ResourcesSpec `json:"resources,omitempty"`

	// Pod defines pod-level configuration
	// +optional
	Pod *PodSpec `json:"pod,omitempty"`

	// AutoScaling defines config server auto-scaling configuration. cfg는 보통
	// 작은 멤버 수(3-7)이고 변동이 거의 없으므로 기본 비활성. opt-in 시 멤버
	// 수 조정에 ScalePolicy.Deliberate 가드 의무.
	// +optional
	AutoScaling *AutoScalingSpec `json:"autoScaling,omitempty"`

	// ScalePolicy는 멤버 수 변경 가드. deliberate=true 없이는 spec.Members 변경
	// 또는 HPA의 scale 결정이 *적용되지 않고* Status.PendingScale에 보류된다.
	// +optional
	ScalePolicy *ScalePolicy `json:"scalePolicy,omitempty"`
}

// ShardSpec defines shard configuration
type ShardSpec struct {
	// Count is the number of shards
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=2
	Count int32 `json:"count"`

	// MembersPerShard is the number of replica set members per shard
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	MembersPerShard int32 `json:"membersPerShard"`

	// Storage defines storage configuration for each shard
	// +optional
	Storage StorageSpec `json:"storage,omitempty"`

	// Resources defines resource requirements for each shard member
	// +optional
	Resources ResourcesSpec `json:"resources,omitempty"`

	// Pod defines pod-level configuration
	// +optional
	Pod *PodSpec `json:"pod,omitempty"`

	// AutoScaling defines shard *count* auto-scaling configuration.
	//
	// 주의(ADR-0009): shard 갯수 변경(addShard/removeShard)은 chunk migration +
	// rebalancing을 동반하므로 표준 HPA로는 안전하게 다룰 수 없다. 본 필드는
	// 호환성을 위해 spec에 보존되지만 *operator가 reconcile하지 않는다*. 별도
	// deliberate sharding API 사이클이 도입될 때까지 운영자가 수동으로 shardCount
	// 를 조정해야 한다.
	// +optional
	AutoScaling *ShardAutoScalingSpec `json:"autoScaling,omitempty"`

	// ScalePolicy는 멤버 수 변경 가드(membersPerShard 또는 향후 멤버 단위 HPA).
	// deliberate=true 없이는 spec.MembersPerShard 변경이 즉시 적용되지 않는다.
	// +optional
	ScalePolicy *ScalePolicy `json:"scalePolicy,omitempty"`
}

// ShardAutoScalingSpec defines shard auto-scaling
type ShardAutoScalingSpec struct {
	// Enabled enables shard auto-scaling
	Enabled bool `json:"enabled"`

	// MinShards is the minimum number of shards
	MinShards int32 `json:"minShards,omitempty"`

	// MaxShards is the maximum number of shards
	MaxShards int32 `json:"maxShards"`

	// Metrics defines scaling metrics
	// +optional
	Metrics []AutoScalingMetric `json:"metrics,omitempty"`
}

// MongosSpec defines mongos router configuration
type MongosSpec struct {
	// Replicas is the number of mongos instances
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=2
	Replicas int32 `json:"replicas"`

	// Resources defines resource requirements
	// +optional
	Resources ResourcesSpec `json:"resources,omitempty"`

	// Pod defines pod-level configuration
	// +optional
	Pod *PodSpec `json:"pod,omitempty"`

	// Service defines mongos service configuration
	// +optional
	Service *MongosServiceSpec `json:"service,omitempty"`

	// AutoScaling defines mongos auto-scaling configuration
	// +optional
	AutoScaling *AutoScalingSpec `json:"autoScaling,omitempty"`
}

// MongosServiceSpec defines mongos service configuration
type MongosServiceSpec struct {
	// Type is the service type
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +kubebuilder:default="ClusterIP"
	Type string `json:"type,omitempty"`

	// Port is the service port
	// +kubebuilder:default=27017
	Port int32 `json:"port,omitempty"`

	// Annotations are additional service annotations
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// LoadBalancerIP is the load balancer IP (for LoadBalancer type)
	// +optional
	LoadBalancerIP string `json:"loadBalancerIP,omitempty"`
}

// MongoDBShardedStatus defines the observed state of MongoDBSharded
type MongoDBShardedStatus struct {
	// Phase represents the current phase
	Phase MongoDBShardedPhase `json:"phase,omitempty"`

	// ConfigServerStatus contains config server status
	ConfigServer ComponentStatus `json:"configServer,omitempty"`

	// ShardsStatus contains status of each shard
	// +optional
	Shards []ShardStatus `json:"shards,omitempty"`

	// MongosStatus contains mongos status
	Mongos ComponentStatus `json:"mongos,omitempty"`

	// Conditions represents the latest available observations
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ConnectionString is the MongoDB connection URI (via mongos)
	ConnectionString string `json:"connectionString,omitempty"`

	// LastBackup contains information about the last backup
	// +optional
	LastBackup *BackupStatus `json:"lastBackup,omitempty"`

	// ShardedCollections lists sharded collections
	// +optional
	ShardedCollections []string `json:"shardedCollections,omitempty"`

	// ObservedGeneration is the most recent generation observed
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ConfigServerInitialized indicates if the config server replica set has been initialized
	ConfigServerInitialized bool `json:"configServerInitialized,omitempty"`

	// ShardsInitialized indicates which shards have been initialized (indexed by shard number)
	// +optional
	ShardsInitialized []bool `json:"shardsInitialized,omitempty"`

	// ShardsAdded indicates which shards have been added to the cluster (indexed by shard number)
	// +optional
	ShardsAdded []bool `json:"shardsAdded,omitempty"`

	// AdminUserCreated indicates if the admin user has been created
	AdminUserCreated bool `json:"adminUserCreated,omitempty"`
}

// ComponentStatus represents the status of a cluster component
type ComponentStatus struct {
	// Ready is the number of ready replicas
	Ready int32 `json:"ready,omitempty"`

	// Total is the total number of replicas
	Total int32 `json:"total,omitempty"`

	// Phase is the component phase
	Phase string `json:"phase,omitempty"`

	// HPA는 컴포넌트의 HorizontalPodAutoscaler 상태(opt-in 시 노출).
	// +optional
	HPA *HPAStatus `json:"hpa,omitempty"`

	// PendingScale은 ScalePolicy.Deliberate=false 가드 때문에 보류된 멤버 수
	// 변경 요청. 보류 중이 아니면 nil.
	// +optional
	PendingScale *PendingScale `json:"pendingScale,omitempty"`
}

// ShardStatus represents the status of a shard
type ShardStatus struct {
	// Name is the shard name
	Name string `json:"name"`

	// Ready is the number of ready members
	Ready int32 `json:"ready"`

	// Total is the total number of members
	Total int32 `json:"total"`

	// Primary is the primary member name
	// +optional
	Primary string `json:"primary,omitempty"`

	// Phase is the shard phase
	Phase string `json:"phase,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mdbsh
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Shards",type="integer",JSONPath=".spec.shards.count"
// +kubebuilder:printcolumn:name="Mongos",type="integer",JSONPath=".status.mongos.ready"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".spec.version.version"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// MongoDBSharded is the Schema for the mongodbshardeds API
type MongoDBSharded struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MongoDBShardedSpec   `json:"spec,omitempty"`
	Status MongoDBShardedStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MongoDBShardedList contains a list of MongoDBSharded
type MongoDBShardedList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDBSharded `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDBSharded{}, &MongoDBShardedList{})
}
