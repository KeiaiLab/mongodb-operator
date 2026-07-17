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
	// PITR (PointInTime / PointInTimeTimestamp) additionally requires the
	// source cluster to have BackupSpec.PITREnabled true, the source backup to
	// carry Status.OplogStart, and the target timestamp to fall inside that
	// backup's restorable window (Status.EarliestRestore ~ Status.LatestRestore).
	// PITR is ReplicaSet-only — see RestoreSpec 의 "PITR 제약" 절.
	// +optional
	Restore *RestoreSpec `json:"restore,omitempty"`
}

// RestoreSpec defines restore configuration (F-IMP-01 / F01 cycle 1).
//
// 본 spec 가 nil 이 아닐 때 MongoDBBackup CR 은 *백업 capture 가 아닌 restore
// 작업* 으로 해석된다. controller 는 다음을 수행:
//   - SourceBackupName 으로 참조한 백업이 Phase=Completed 인지 확인
//   - 해당 백업의 base 스냅샷을 storage 에서 가져와 mongorestore 로 복원
//   - PointInTime / PointInTimeTimestamp 가 모두 nil 이면 base 스냅샷 시점에서
//     종료 (= 그 백업의 Status.OplogStart 시점)
//   - 설정되면 base 복원 후 archived oplog segment 를 목표 시점까지 이어서
//     replay (`mongorestore --oplogReplay --oplogLimit <ts>`)
//
// 본 CR 의 Spec.ClusterRef 는 *target* (복원 대상) 을 가리킨다. SourceBackupName
// 은 같은 namespace 내 backup CR 이름.
//
// # PITR 제약 (ReplicaSet 전용)
//
// PointInTime / PointInTimeTimestamp 는 **ClusterRef.Kind=MongoDB (ReplicaSet)
// 에서만 정합이 보장된다**. MongoDBSharded 는 shard 마다 oplog timestamp 가
// 독립이라 단일 PIT 로 cluster-wide 일관 시점을 정의할 수 없다 — 거부하지 않고
// shard 별 best-effort 로 복원하되, admission webhook 이 Warning 을 반환한다.
// cluster-wide sharded PITR 은 미지원 (백로그).
type RestoreSpec struct {
	// SourceBackupName references the source MongoDBBackup (must be Completed)
	// in the same namespace as this restore CR.
	// +kubebuilder:validation:MinLength=1
	SourceBackupName string `json:"sourceBackupName"`

	// PointInTime 은 복구 목표 시점 (RFC3339, **초 단위**). PointInTime 과
	// PointInTimeTimestamp 가 모두 nil 이면 base 스냅샷 시점으로만 복원한다.
	// 설정 시 controller 는 base 복원 후 source 백업의 Status.OplogStart 이후
	// archived oplog segment 를 이 시점까지 replay 한다.
	//
	// 같은 초 안에 여러 write 가 있으면 초 단위로는 어디서 끊을지 결정할 수
	// 없다 — 그 경계까지 지정해야 하면 PointInTimeTimestamp 를 쓴다 (설정 시
	// 그쪽이 우선).
	//
	// 실제로 적용되는 검증은 2 지점이다:
	//  1. admission webhook — 요청 ts 가 source 백업의 Status.EarliestRestore ~
	//     Status.LatestRestore 밖이면 거부. window 가 아직 기록되지 않았으면
	//     통과시킨다 (**fail-open**) — 이 window 는 S3 segment 관측 기반이라
	//     권고 수준 게이트이지 진본이 아니다.
	//  2. restore job — 실제 S3 oplog segment 로 replay 를 시도하고, 목표 ts 에
	//     닿는 segment chain 이 없으면 Phase=Failed + Status.Error 로 끝난다.
	//     **이쪽이 authoritative** 하다.
	// +optional
	PointInTime *metav1.Time `json:"pointInTime,omitempty"`

	// PointInTimeTimestamp 은 복구 목표를 BSON timestamp 원형인
	// "<sec>:<ordinal>" 형식으로 직접 지정한다 (예: "1752710400:7").
	// sec = unix epoch 초, ordinal = 그 초 안의 연산 순번.
	//
	// 설정되면 **PointInTime 보다 우선**한다. 초 내 순서까지 정확히 끊어야 할
	// 때 (예: 특정 오작동 write 직전으로 복원) 쓴다. controller 는 값을 그대로
	// `mongorestore --oplogLimit` 에 전달하므로 ordinal 이 보존된다.
	//
	// 검증·제약은 PointInTime 과 동일 (webhook fail-open + restore job
	// authoritative, ReplicaSet 전용).
	// +optional
	// +kubebuilder:validation:Pattern=`^[0-9]+:[0-9]+$`
	PointInTimeTimestamp *string `json:"pointInTimeTimestamp,omitempty"`
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

	// OplogStart 는 base 스냅샷의 **oplog 일관 시점** — `mongodump --oplog` 가
	// dump 종료 시점에 함께 기록한 oplog 위치다. PITR replay 의 *하한 앵커*:
	// 복원은 이 시점의 스냅샷에서 출발해 이후 archived segment 를 이어 붙인다.
	//
	// 비어 있으면 그 백업은 `--oplog` 없이 떠졌다는 뜻이라 PITR 기점으로 쓸 수
	// 없다 (base 스냅샷 시점 복원 전용).
	//
	// 초 단위 (RFC3339) — window 계산·표시용이다. replay 경계로 쓰이는 정밀
	// BSON ts 는 status 가 아니라 S3 oplog segment 키
	// (`<prefix>/<cluster>/oplog/<startTs>_<endTs>.bson.gz`) 가 진본이므로,
	// 본 필드를 초 정밀도 그대로 replay 경계로 삼지 말 것.
	//
	// 채우는 주체: MongoDBBackupReconciler (base 백업 Job 완료 감지 시).
	// +optional
	OplogStart *metav1.Time `json:"oplogStart,omitempty"`

	// EarliestRestore 는 이 백업이 복원할 수 있는 **가장 이른 시점** = 복원 가능
	// window 의 하한. base 스냅샷의 일관 시점이 곧 하한이므로 값은 OplogStart 와
	// 동치이며, webhook·controller 가 window 를 [Earliest, Latest] 한 쌍으로만
	// 읽으면 되도록 별도 필드로 둔다 (OplogStart 는 base 아카이브의 불변 사실,
	// 본 필드는 uploader 가 재계산하는 window 경계).
	//
	// OplogStart 가 없거나 PITR 미사용 백업이면 nil — webhook 은 nil window 를
	// fail-open 처리한다.
	//
	// 채우는 주체: OplogUploaderReconciler (S3 segment 키 스캔 + retention prune
	// 후 재계산).
	// +optional
	EarliestRestore *metav1.Time `json:"earliestRestore,omitempty"`

	// LatestRestore 는 이 백업이 복원할 수 있는 **가장 늦은 시점** = 복원 가능
	// window 의 상한. OplogStart 에서 출발해 **끊김 없이(gap-free) 이어지는**
	// oplog segment chain 의 마지막 endTs 다. 중간에 gap 이 있으면 그 앞에서
	// 잘린다 — retention prune 이 base 직후 segment 를 지운 경우
	// EarliestRestore == LatestRestore == OplogStart (base 시점 복원만 가능).
	//
	// 채우는 주체: OplogUploaderReconciler (EarliestRestore 와 동시 갱신).
	// +optional
	LatestRestore *metav1.Time `json:"latestRestore,omitempty"`

	// RestorableWindow 는 [EarliestRestore, LatestRestore] 를 사람이 읽는 한 줄로
	// 요약한다 — 예: "2026-07-17T01:00:00Z ~ 2026-07-17T09:30:00Z".
	// `kubectl get mdbbackup -o wide` 의 WINDOW 열이 이 값을 그대로 보여준다.
	//
	// 표시 전용 — 기계 판정(webhook / controller)은 본 문자열을 파싱하지 말고
	// EarliestRestore / LatestRestore 를 직접 읽어야 한다.
	//
	// 채우는 주체: OplogUploaderReconciler (window 재계산 시 동시 갱신).
	// +optional
	RestorableWindow string `json:"restorableWindow,omitempty"`

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
// +kubebuilder:printcolumn:name="Window",type="string",JSONPath=".status.restorableWindow",priority=1
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
