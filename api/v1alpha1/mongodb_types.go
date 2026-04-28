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

// MongoDBSpec defines the desired state of MongoDB ReplicaSet
type MongoDBSpec struct {
	// Members is the number of replica set members
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

	// AutoScaling defines auto-scaling configuration
	// +optional
	AutoScaling *AutoScalingSpec `json:"autoScaling,omitempty"`

	// Pod defines pod-level configuration
	// +optional
	Pod *PodSpec `json:"pod,omitempty"`

	// Arbiter defines arbiter configuration
	// +optional
	Arbiter *ArbiterSpec `json:"arbiter,omitempty"`

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

// MongoDBList contains a list of MongoDB
type MongoDBList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDB `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDB{}, &MongoDBList{})
}
