/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package v1beta1

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

	// Restore defines optional restore configuration. When set, this
	// MongoDBBackup CR triggers a restore (not a new backup capture). The
	// referenced source backup (SourceBackupName) must already be Completed.
	// PointInTime requires the source cluster to have BackupSpec.PITREnabled
	// true and OplogRetentionHours window covering the target timestamp.
	// +optional
	Restore *RestoreSpec `json:"restore,omitempty"`
}

// RestoreSpec defines restore configuration (F-IMP-01 / F01 cycle 1).
//
// 본 spec 가 nil 이 아닐 때 MongoDBBackup CR 은 *백업 capture 가 아닌 restore
// 작업* 으로 해석된다. controller 는 다음을 보장:
//   - SourceBackupName 으로 참조한 백업이 Phase=Completed 인지 확인
//   - PointInTime 이 설정되면 source cluster 의 oplog retention window 안에
//     timestamp 가 있는지 검증 후 mongorestore --oplogReplay 실행
//   - PointInTime 미설정 시 단순 full restore (백업 시점으로 복원)
//
// 본 CR 의 Spec.ClusterRef 는 *target* (복원 대상) 을 가리킨다. SourceBackupName
// 은 같은 namespace 내 backup CR 이름.
type RestoreSpec struct {
	// SourceBackupName references the source MongoDBBackup (must be Completed)
	// in the same namespace as this restore CR.
	// +kubebuilder:validation:MinLength=1
	SourceBackupName string `json:"sourceBackupName"`

	// PointInTime is the target timestamp for point-in-time recovery. When
	// nil, restore replays the backup snapshot only. When set, the controller
	// performs `mongorestore --oplogReplay --oplogLimit <ts>` against the
	// archived oplog stream uploaded by the oplog uploader controller.
	//
	// Must fall within the source cluster's oplog retention window
	// (BackupSpec.OplogRetentionHours). Past timestamps that have been
	// trimmed will be rejected by the validating webhook.
	// +optional
	PointInTime *metav1.Time `json:"pointInTime,omitempty"`
}

// MongoDBBackupStatus defines the observed state of MongoDBBackup
type MongoDBBackupStatus struct {
	// Phase represents the current backup phase
	// +kubebuilder:validation:Enum=Pending;Running;Completed;Failed;Restoring
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
// +kubebuilder:storageversion
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
// +kubebuilder:storageversion

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
