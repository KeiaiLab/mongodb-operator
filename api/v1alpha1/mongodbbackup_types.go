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

// MongoDBBackupSpec defines the desired state of MongoDBBackup
type MongoDBBackupSpec struct {
	// ClusterRef references the MongoDB or MongoDBSharded cluster
	ClusterRef ClusterReference `json:"clusterRef"`

	// Storage defines backup storage location
	Storage BackupStorageSpec `json:"storage"`

	// Type is the backup type
	// +kubebuilder:validation:Enum=full;incremental
	// +kubebuilder:default="full"
	Type string `json:"type,omitempty"`

	// Compression enables backup compression
	// +kubebuilder:default=true
	Compression bool `json:"compression,omitempty"`

	// CompressionType defines compression algorithm
	// +kubebuilder:validation:Enum=gzip;zstd;snappy
	// +kubebuilder:default="zstd"
	CompressionType string `json:"compressionType,omitempty"`
}

// MongoDBBackupStatus defines the observed state of MongoDBBackup
type MongoDBBackupStatus struct {
	// Phase represents the current backup phase
	// +kubebuilder:validation:Enum=Pending;Running;Completed;Failed
	Phase string `json:"phase,omitempty"`

	// StartTime is when the backup started
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the backup completed
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// Size is the backup size
	// +optional
	Size string `json:"size,omitempty"`

	// Location is the backup location
	// +optional
	Location string `json:"location,omitempty"`

	// Error contains error message if failed
	// +optional
	Error string `json:"error,omitempty"`

	// Conditions represents the latest available observations
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mdbbackup
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".spec.clusterRef.name"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Size",type="string",JSONPath=".status.size"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// MongoDBBackup is the Schema for the mongodbbackups API
type MongoDBBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MongoDBBackupSpec   `json:"spec,omitempty"`
	Status MongoDBBackupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MongoDBBackupList contains a list of MongoDBBackup
type MongoDBBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDBBackup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDBBackup{}, &MongoDBBackupList{})
}

// Statusable interface 구현.
func (m *MongoDBBackup) GetConditions() *[]metav1.Condition { return &m.Status.Conditions }
func (m *MongoDBBackup) SetPhase(phase string)              { m.Status.Phase = phase }
