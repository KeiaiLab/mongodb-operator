/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
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

	// UpgradeStrategy defines version upgrade automation policy (cycle 7).
	// +optional
	UpgradeStrategy *UpgradeStrategySpec `json:"upgradeStrategy,omitempty"`

	// AuditLog defines MongoDB Enterprise audit log policy (cycle 8).
	// +optional
	AuditLog *AuditLogSpec `json:"auditLog,omitempty"`

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

	// External configures an external (pre-existing) config server replica set.
	// When set, the operator skips creating ConfigServer StatefulSet/Service
	// and uses the provided connection string for mongos --configdb.
	// Mutually exclusive with Members/Storage/Resources.
	// +optional
	External *ExternalConfigServerSpec `json:"external,omitempty"`
}

// ExternalConfigServerSpec references a pre-existing config server replica set.
type ExternalConfigServerSpec struct {
	// ReplicaSetName is the replica set name of the external config server.
	ReplicaSetName string `json:"replicaSetName"`

	// Hosts is a list of host:port addresses (e.g., ["cfg-0:27019", "cfg-1:27019", "cfg-2:27019"]).
	// +kubebuilder:validation:MinItems=1
	Hosts []string `json:"hosts"`
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

	// HiddenMembers — F67/F75 (cycle 10) sharded hidden replica.
	//
	// 각 shard 의 RS 에 hidden=true / priority=0 / votes=0 인 멤버 추가.
	// analytics / backup 격리 시나리오. data-bearing 이지만 client read
	// preference 에 직접 노출되지 않음. CloudPirates parity #19.
	// +optional
	HiddenMembers *ShardHiddenMembersSpec `json:"hiddenMembers,omitempty"`

	// Arbiter는 각 shard replica set 에 배치할 arbiter 구성.
	//
	// Why: sharded cluster 의 각 shard 는 독립된 replica set 이다. 비용 효율을
	// 위해 일부 환경에서 data-bearing member 대신 arbiter (vote 만 갖는 lightweight
	// member) 를 추가해 PSA (Primary-Secondary-Arbiter) 토폴로지를 구성한다. 본
	// 필드는 ROADMAP 4.2 Sharded Arbiter / Hidden member (P0) 의 일부.
	//
	// 주의: MongoDB 컨벤션상 RS 당 arbiter 는 최대 1개. Replicas 는 0 또는 1.
	// 본 webhook 검증에서 강제.
	// +optional
	Arbiter *ShardArbiterSpec `json:"arbiter,omitempty"`

	// DrainTimeoutSeconds 는 scale-in 시 shard 의 chunk drain 완료 대기 시간(초).
	// 지정하지 않으면 기본 30초. removeShard 이후 chunk migration 이 이 시간 내
	// 완료되지 않으면 controller 가 drain 실패로 판단한다.
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=0
	// +optional
	DrainTimeoutSeconds *int32 `json:"drainTimeoutSeconds,omitempty"`
}

// ShardHiddenMembersSpec — F67/F75 (cycle 10).
type ShardHiddenMembersSpec struct {
	// Count = hidden member 수 per shard. 0 = 비활성.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=3
	// +kubebuilder:default=0
	Count int32 `json:"count"`

	// Priority — MongoDB priority. 기본 0 (선출 불가).
	// +kubebuilder:default="0.0"
	Priority string `json:"priority,omitempty"`

	// Votes — MongoDB votes. 기본 0 (투표권 없음, RS quorum 영향 0).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	// +kubebuilder:default=0
	Votes int32 `json:"votes,omitempty"`

	// SlaveDelaySeconds — F76 (cycle 10) CloudPirates parity #20.
	// 0 이상이면 delayed replica (DR 시나리오, 빠른 데이터 복구).
	// +kubebuilder:default=0
	SlaveDelaySeconds int32 `json:"slaveDelaySeconds,omitempty"`

	// Tags — read preference 라우팅용 tag 셋. e.g. {"role": "analytics"}
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// Resources — hidden member 별 리소스 (data-bearing 이지만 별도 hardware 권장).
	// +optional
	Resources ResourcesSpec `json:"resources,omitempty"`
}

// ShardArbiterSpec defines per-shard arbiter configuration.
//
// 단일 RS 의 ArbiterSpec 과 별도 타입인 이유: sharded 토폴로지는 shard 별로
// arbiter 정책을 일관 적용해야 하고, 향후 hiddenMembers 와 같은 sharded 한정
// 옵션이 추가될 여지가 있다. 또한 Replicas 필드 (0/1 선택) 가 single-RS 의
// ArbiterSpec 에는 의미가 없다 (단일 RS 는 Enabled bool 만으로 충분).
type ShardArbiterSpec struct {
	// Enabled enables arbiter on each shard's replica set.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// Replicas is the number of arbiter members per shard RS.
	//
	// MongoDB 권장사항: arbiter 는 RS 당 1개를 초과하지 않는다. 0 또는 1.
	// Enabled=false 일 때는 무시.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	// +kubebuilder:default=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// Resources defines per-arbiter resource requirements. Arbiter 는 데이터
	// 를 보유하지 않으므로 data member 대비 매우 낮은 자원으로 운영 가능.
	// +optional
	Resources ResourcesSpec `json:"resources,omitempty"`
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

	// UseStatefulSet — G-12 upstream chart parity. true 시 mongos 를 StatefulSet 으로
	// 배포 (각 mongos pod 가 stable network identity 보유). 외부 client 가
	// 특정 mongos 를 직접 지정해야 하는 시나리오 (e.g. service-per-replica).
	// 기본 false: Deployment (rolling update + horizontal scale 우수).
	// +kubebuilder:default=false
	// +optional
	UseStatefulSet bool `json:"useStatefulSet,omitempty"`

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

	// SessionAffinity — F72 (cycle 10) upstream chart parity.
	// +kubebuilder:validation:Enum=None;ClientIP
	// +kubebuilder:default="None"
	// +optional
	SessionAffinity string `json:"sessionAffinity,omitempty"`

	// SessionAffinityConfig — ClientIP affinity 시 timeoutSeconds.
	// +optional
	SessionAffinityConfig *corev1.SessionAffinityConfig `json:"sessionAffinityConfig,omitempty"`

	// ExternalIPs — F72 (cycle 10). spec.externalIPs 그대로 매핑.
	// +optional
	ExternalIPs []string `json:"externalIPs,omitempty"`

	// NodePort — F72 (cycle 10). Type=NodePort 또는 LoadBalancer 시 명시 포트.
	// +optional
	NodePort int32 `json:"nodePort,omitempty"`

	// Headless — F72 (cycle 10). ClusterIP=None 헤드리스 서비스.
	// +kubebuilder:default=false
	// +optional
	Headless bool `json:"headless,omitempty"`

	// ServicePerReplica creates individual services for each mongos pod.
	// Requires UseStatefulSet=true on MongosSpec.
	// +optional
	ServicePerReplica *ServicePerReplicaSpec `json:"servicePerReplica,omitempty"`
}

// ServicePerReplicaSpec configures per-pod services for mongos.
type ServicePerReplicaSpec struct {
	// Enabled creates one Service per mongos pod (e.g., my-sharded-mongos-0, -1, ...).
	Enabled bool `json:"enabled"`

	// Type is the per-replica service type.
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +kubebuilder:default="ClusterIP"
	// +optional
	Type string `json:"type,omitempty"`

	// Annotations are additional annotations for per-replica services.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
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

	// Version is the current running MongoDB version.
	// +optional
	Version string `json:"version,omitempty"`

	// PreviousVersion is the version before an in-progress upgrade.
	// +optional
	PreviousVersion string `json:"previousVersion,omitempty"`

	// UpgradePhase tracks the current upgrade orchestration phase.
	// +kubebuilder:validation:Enum="";BackingUp;Upgrading;Validating;RollingBack
	// +optional
	UpgradePhase string `json:"upgradePhase,omitempty"`

	// UpgradeStartTime is when the current upgrade was initiated.
	// +optional
	UpgradeStartTime *metav1.Time `json:"upgradeStartTime,omitempty"`
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
// +kubebuilder:storageversion
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
// +kubebuilder:storageversion

// MongoDBShardedList contains a list of MongoDBSharded
type MongoDBShardedList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDBSharded `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDBSharded{}, &MongoDBShardedList{})
}

// Statusable interface 구현.
func (m *MongoDBSharded) GetConditions() *[]metav1.Condition { return &m.Status.Conditions }
func (m *MongoDBSharded) SetPhase(phase string)              { m.Status.Phase = MongoDBShardedPhase(phase) }
