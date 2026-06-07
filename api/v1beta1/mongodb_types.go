/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MongoDBPhase represents the lifecycle phase of a MongoDB resource.
// +kubebuilder:validation:Enum=Pending;Initializing;Running;Failed;Upgrading
type MongoDBPhase string

const (
	// PhasePending indicates the resource is awaiting reconciliation.
	PhasePending MongoDBPhase = "Pending"
	// PhaseInitializing indicates the replica set is being initialized.
	PhaseInitializing MongoDBPhase = "Initializing"
	// PhaseRunning indicates the replica set is fully operational.
	PhaseRunning MongoDBPhase = "Running"
	// PhaseFailed indicates the resource entered a failed state.
	PhaseFailed MongoDBPhase = "Failed"
	// PhaseUpgrading indicates the replica set is undergoing a version upgrade.
	PhaseUpgrading MongoDBPhase = "Upgrading"
)

// IsStandaloneMode — G-11 (cycle 19) explicit standalone detection helper.
// CloudPirates parity #1 (architecture: standalone) — operator-side semantic.
// Members=1 시 *replica set 모드이지만 single-member* → autoscaling/PDB
// 기본값이 부적절. caller (controller/webhook) 가 본 helper 로 분기.
func (s *MongoDBSpec) IsStandaloneMode() bool {
	return s.Members == 1
}

// MongoDBSpec defines the desired state of MongoDB ReplicaSet
type MongoDBSpec struct {
	// Members is the number of replica set members.
	// G-11 (cycle 19): Members=1 = standalone mode (CloudPirates parity #1).
	// CRD allows 1 (Minimum=1), controller treats 1 as standalone-aware:
	//   - autoScaling.enabled = true 시 webhook reject (single-member rs scale 의미 없음)
	//   - pdb.minAvailable 자동 조정 (members=1 시 pdb 무의미하므로 0)
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=50
	// +kubebuilder:default=3
	Members int32 `json:"members"`

	// Version defines MongoDB version configuration
	Version MongoDBVersion `json:"version"`

	// Storage defines storage configuration
	// +optional
	Storage StorageSpec `json:"storage,omitempty"`

	// Resources defines resource requirements
	// +optional
	Resources ResourcesSpec `json:"resources,omitempty"`

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

	// AutoScaling defines auto-scaling configuration. 멤버 수 자동 조정 — RS
	// reconfig + initial sync 부작용 때문에 ScalePolicy.Deliberate=true 가드와
	// 함께 opt-in 해야 HPA가 reconcile된다(ADR-0008).
	// +optional
	AutoScaling *AutoScalingSpec `json:"autoScaling,omitempty"`

	// ScalePolicy는 멤버 수 변경 가드. deliberate=true 없이는 spec.Members 변경
	// 또는 HPA의 scale 결정이 *적용되지 않고* Status.PendingScale에 보류된다.
	// +optional
	ScalePolicy *ScalePolicy `json:"scalePolicy,omitempty"`

	// Pod defines pod-level configuration
	// +optional
	Pod *PodSpec `json:"pod,omitempty"`

	// Arbiter defines arbiter configuration
	// +optional
	Arbiter *ArbiterSpec `json:"arbiter,omitempty"`

	// PodDisruptionBudget defines PDB configuration for the replica set workload.
	// 미지정 시 controller는 PDB를 생성하지 않는다(opt-in).
	// +optional
	PodDisruptionBudget *PodDisruptionBudgetSpec `json:"podDisruptionBudget,omitempty"`

	// NetworkPolicy defines NetworkPolicy configuration. 기본 비활성(opt-in).
	// Enabled=true면 같은 RS의 pods간 27017 ingress만 허용하는 deny-by-default
	// 정책이 적용되며 AdditionalIngressFrom으로 운영 namespace/exporter 등을 추가.
	// +optional
	NetworkPolicy *NetworkPolicySpec `json:"networkPolicy,omitempty"`

	// ReplicaSetName is the name of the replica set
	// +kubebuilder:default="rs0"
	ReplicaSetName string `json:"replicaSetName,omitempty"`

	// AdditionalConfig allows passing additional MongoDB configuration
	// +optional
	AdditionalConfig map[string]string `json:"additionalConfig,omitempty"`
}

// ArbiterSpec defines arbiter configuration
type ArbiterSpec struct {
	// Enabled enables an arbiter member
	Enabled bool `json:"enabled"`

	// Resources defines arbiter resource requirements
	// +optional
	Resources ResourcesSpec `json:"resources,omitempty"`
}

// MongoDBStatus defines the observed state of MongoDB
type MongoDBStatus struct {
	// Phase represents the current phase
	Phase MongoDBPhase `json:"phase,omitempty"`

	// ReadyMembers is the number of ready replica set members
	ReadyMembers int32 `json:"readyMembers,omitempty"`

	// CurrentPrimary is the current primary member
	CurrentPrimary string `json:"currentPrimary,omitempty"`

	// Members contains status of each member
	// +optional
	Members []MemberStatus `json:"members,omitempty"`

	// Conditions represents the latest available observations
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ConnectionString is the MongoDB connection URI
	ConnectionString string `json:"connectionString,omitempty"`

	// TLSSecretName is the name of the TLS secret
	// +optional
	TLSSecretName string `json:"tlsSecretName,omitempty"`

	// LastBackup contains information about the last backup
	// +optional
	LastBackup *BackupStatus `json:"lastBackup,omitempty"`

	// Version is the current MongoDB version
	Version string `json:"version,omitempty"`

	// ObservedGeneration is the most recent generation observed
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ReplicaSetInitialized indicates if the replica set has been initialized
	ReplicaSetInitialized bool `json:"replicaSetInitialized,omitempty"`

	// AdminUserCreated indicates if the admin user has been created
	AdminUserCreated bool `json:"adminUserCreated,omitempty"`

	// HPA는 RS HorizontalPodAutoscaler 상태(opt-in 시 노출).
	// +optional
	HPA *HPAStatus `json:"hpa,omitempty"`

	// PendingScale은 ScalePolicy.Deliberate=false 가드 때문에 보류된 멤버 수
	// 변경 요청. 보류 중이 아니면 nil.
	// +optional
	PendingScale *PendingScale `json:"pendingScale,omitempty"`

	// PreviousVersion is the version before an in-progress upgrade (rollback target).
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

// MemberStatus represents the status of a replica set member
type MemberStatus struct {
	// Name is the pod name
	Name string `json:"name"`

	// State is the replica set member state (PRIMARY, SECONDARY, ARBITER, etc.)
	State string `json:"state"`

	// Health indicates if the member is healthy
	Health bool `json:"health"`

	// Uptime is the member uptime in seconds
	// +optional
	Uptime int64 `json:"uptime,omitempty"`
}

// BackupStatus represents the status of a backup
type BackupStatus struct {
	// Time is when the backup was taken
	Time metav1.Time `json:"time,omitempty"`

	// Successful indicates if the backup was successful
	Successful bool `json:"successful"`

	// Location is the backup location
	// +optional
	Location string `json:"location,omitempty"`

	// Size is the backup size
	// +optional
	Size string `json:"size,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mdb
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Members",type="integer",JSONPath=".status.readyMembers"
// +kubebuilder:printcolumn:name="Primary",type="string",JSONPath=".status.currentPrimary"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".spec.version.version"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// MongoDB is the Schema for the mongodbs API
type MongoDB struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MongoDBSpec   `json:"spec,omitempty"`
	Status MongoDBStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion

// MongoDBList contains a list of MongoDB
type MongoDBList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDB `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDB{}, &MongoDBList{})
}

// Statusable interface 구현 — internal/controller/helpers.go의 generic helper에서 사용.
func (m *MongoDB) GetConditions() *[]metav1.Condition { return &m.Status.Conditions }
func (m *MongoDB) SetPhase(phase string)              { m.Status.Phase = MongoDBPhase(phase) }
func (m *MongoDB) SetObservedGeneration(g int64)      { m.Status.ObservedGeneration = g }
