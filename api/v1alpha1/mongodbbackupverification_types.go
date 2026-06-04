/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// mongodbbackupverification_types.go — F48-F50 (cycle 9) 자동 백업 검증 CRD.

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MongoDBBackupVerificationSpec — controller 가 schedule 마다 생성하여
// 백업 1건의 restore drill 실행.
type MongoDBBackupVerificationSpec struct {
	// BackupRef references the MongoDBBackup to verify (Completed phase 요구).
	BackupRef corev1.LocalObjectReference `json:"backupRef"`

	// SampleQueries — 복원 후 sanity check 으로 실행할 query 목록.
	// 결과 행 수가 0 또는 spec.MinExpectedDocs 미만이면 검증 실패.
	// +optional
	SampleQueries []VerificationQuery `json:"sampleQueries,omitempty"`

	// CleanupOnSuccess — 검증 PASS 시 임시 mongod instance 자동 폐기.
	// false 시 사용자가 수동 inspect 가능.
	// +kubebuilder:default=true
	CleanupOnSuccess bool `json:"cleanupOnSuccess,omitempty"`

	// Queryable — 활성 시 검증과 함께 *장기 read-only mongod instance* 를 생성하여
	// 백업을 ad-hoc 조회 가능하게 한다 (ROADMAP §3.1.1). Enabled 기본 false (opt-in)
	// 이므로 명시 활성 시에만 instance 가 생성된다. instance 데이터 복원 drill 은
	// 후속 운영 강화 (ROADMAP §3.1.1 deferred) — 본 단계는 구조적 instance 생성.
	// +optional
	Queryable *QueryableBackupSpec `json:"queryable,omitempty"`
}

// VerificationQuery — 한 sanity check.
type VerificationQuery struct {
	// DB 대상 database.
	// +kubebuilder:validation:MinLength=1
	DB string `json:"db"`

	// Collection 대상 컬렉션.
	// +kubebuilder:validation:MinLength=1
	Collection string `json:"collection"`

	// FilterJSON MongoDB query filter ({} = all).
	// +kubebuilder:default="{}"
	FilterJSON string `json:"filterJSON,omitempty"`

	// MinExpectedDocs — 결과 행 수가 이 값 이상이어야 검증 통과.
	// +kubebuilder:default=1
	MinExpectedDocs int64 `json:"minExpectedDocs,omitempty"`
}

// MongoDBBackupVerificationStatus — verification 결과.
type MongoDBBackupVerificationStatus struct {
	// Phase: Pending | Restoring | Verifying | Passed | Failed
	// +kubebuilder:validation:Enum=Pending;Restoring;Verifying;Passed;Failed
	Phase string `json:"phase,omitempty"`

	// StartTime when verification began.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime when verification finished (Passed or Failed).
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// QueryResults 각 SampleQueries 의 row count + pass/fail.
	// +optional
	QueryResults []VerificationQueryResult `json:"queryResults,omitempty"`

	// QueryableInstance — Spec.Queryable 활성 시 생성된 read-only mongod
	// StatefulSet 이름. 데이터 복원 drill 은 후속 (ROADMAP §3.1.1 deferred).
	// +optional
	QueryableInstance string `json:"queryableInstance,omitempty"`

	// Error 실패 사유.
	// +optional
	Error string `json:"error,omitempty"`

	// Conditions follow standard k8s convention.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// VerificationQueryResult — 단일 query 결과.
type VerificationQueryResult struct {
	DB         string `json:"db"`
	Collection string `json:"collection"`
	DocCount   int64  `json:"docCount"`
	Passed     bool   `json:"passed"`
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mdbverify
// +kubebuilder:printcolumn:name="Backup",type="string",JSONPath=".spec.backupRef.name"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// MongoDBBackupVerification is the Schema for backup restore drills.
type MongoDBBackupVerification struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MongoDBBackupVerificationSpec   `json:"spec,omitempty"`
	Status MongoDBBackupVerificationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MongoDBBackupVerificationList contains a list of MongoDBBackupVerification.
type MongoDBBackupVerificationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDBBackupVerification `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDBBackupVerification{}, &MongoDBBackupVerificationList{})
}
