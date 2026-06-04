/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// mongodbfederation_types.go — F33 (cycle 5) 다중 리전 federation CRD.
//
// 본 CRD 는 *여러 K8s cluster 에 걸친 단일 MongoDB ReplicaSet* 를 관리.
// 각 region 은 자체 kubeconfig + priority + zone tag 를 가지며, federation
// controller 가 cross-cluster oplog replication 을 정합.
//
// 본 cycle 의 acceptance: CRD type 정의 + deepcopy + reconciler skeleton.
// 실 cross-cluster bind 는 cycle 8+ ClusterGroup 통합 시점에 강화.

package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MongoDBFederationSpec defines a multi-cluster MongoDB ReplicaSet topology.
type MongoDBFederationSpec struct {
	// Version is the MongoDB version applied to all regions.
	Version MongoDBVersion `json:"version"`

	// Regions defines per-cluster member configuration.
	// +kubebuilder:validation:MinItems=2
	Regions []FederationRegion `json:"regions"`

	// Auth applies to the federated replica set (admin credentials must be
	// the same secret content across all cluster kubeconfigs).
	Auth AuthSpec `json:"auth"`

	// TLS applies cluster-wide. cert-manager Issuer 가 각 cluster 에 동일
	// 이름으로 존재한다고 가정.
	// +optional
	TLS *TLSSpec `json:"tls,omitempty"`
}

// FederationRegion defines one cluster slice of the federation.
type FederationRegion struct {
	// Name is the human-readable region identifier (e.g. "us-west", "eu-central").
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// ClusterKubeConfigRef references a Secret in operator namespace with
	// key `kubeconfig` containing the target cluster credentials. Federation
	// controller uses this to manage remote MongoDB CR.
	ClusterKubeConfigRef corev1.LocalObjectReference `json:"clusterKubeConfigRef"`

	// Members is the number of MongoDB ReplicaSet members in this region.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=2
	Members int32 `json:"members"`

	// Priority is the MongoDB replica priority value (0.0-1000.0). Higher
	// priority members are preferred as primary. Use 0 to make a region
	// secondary-only (analytics / DR).
	// +kubebuilder:default="1.0"
	Priority string `json:"priority,omitempty"`

	// Zone is an optional zone tag for zone-aware sharding routing.
	// +optional
	Zone string `json:"zone,omitempty"`

	// Storage applies to MongoDB pods in this region.
	// +optional
	Storage StorageSpec `json:"storage,omitempty"`
}

// MongoDBFederationStatus is the observed federation state.
type MongoDBFederationStatus struct {
	// Phase: Pending | Bootstrapping | Synced | Degraded | Failed
	// +kubebuilder:validation:Enum=Pending;Bootstrapping;Synced;Degraded;Failed
	Phase string `json:"phase,omitempty"`

	// RegionStatuses tracks per-region reconcile state.
	// +optional
	RegionStatuses []FederationRegionStatus `json:"regionStatuses,omitempty"`

	// Conditions follow standard k8s convention.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// FederationRegionStatus is per-region observed state.
type FederationRegionStatus struct {
	// Name matches Spec.Regions[].Name.
	Name string `json:"name"`

	// Phase per region: Pending | Synced | Degraded | Failed
	// +kubebuilder:validation:Enum=Pending;Synced;Degraded;Failed
	Phase string `json:"phase,omitempty"`

	// LastSyncTime is when the controller last reached the region cluster.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Error contains the last reconcile error (empty when synced).
	// +optional
	Error string `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mdbfed
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Regions",type="integer",JSONPath=".spec.regions[*].name"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// MongoDBFederation is the Schema for the mongodbfederations API.
type MongoDBFederation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MongoDBFederationSpec   `json:"spec,omitempty"`
	Status MongoDBFederationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MongoDBFederationList contains a list of MongoDBFederation.
type MongoDBFederationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDBFederation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDBFederation{}, &MongoDBFederationList{})
}
