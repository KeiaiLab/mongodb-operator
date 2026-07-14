/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// mongodbsearch_types.go — Phase 1 (Atlas 갭 클로징 로드맵): MongoDB Search +
// Vector Search 를 self-managed `mongot` 으로 제공하는 CRD.
//
// operator 가 참조된 MongoDB/MongoDBSharded 클러스터 옆에 mongot StatefulSet 을
// 배포하고, mongod 에 mongotHost/searchIndexManagementHostAndPort setParameter 를
// 주입하여 `$search`/`$vectorSearch` aggregation 을 활성화한다. mongo 8.2+ 필요
// (라이브 8.3 호환). mongot 은 gRPC 27028(쿼리+인덱스관리) + 27029(health) 사용.
//
// ⚠ MongoDB Search self-managed 는 현재 public preview — dev/staging 선행, GA 후 production.

package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MongoDBSearchSpec — mongot 배포 명세.
type MongoDBSearchSpec struct {
	// Source — mongot 가 색인할 대상 MongoDB 클러스터(sync source).
	Source SearchSource `json:"source"`

	// Version — mongot 이미지 버전/오버라이드. 미지정 시 operator 기본값.
	// +optional
	Version MongotVersion `json:"version,omitempty"`

	// Replicas — mongot pod 수. ReplicaSet=총 mongot 수, Sharded=shard 당 수.
	// replicas>1 은 L7(gRPC) load balancer 필요(차기 — v1 은 1 권장).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	Replicas int32 `json:"replicas,omitempty"`

	// Resources — mongot 컨테이너 리소스 요청/제한.
	// +optional
	Resources ResourcesSpec `json:"resources,omitempty"`

	// Storage — Deprecated(sidecar 모델 미사용). mongot 인덱스는 mongod data PVC 의
	// subPath(search-index)에 저장된다(노드 디스크 종속 제거 + commonsapply VCT immutable
	// 호환). search 활성 시 source MongoDB 의 spec.storage.size 를 인덱스 용량만큼 키울 것.
	// +optional
	Storage StorageSpec `json:"storage,omitempty"`

	// TLS — mongod↔mongot 통신 TLS. 미지정 시 source 클러스터 TLS 설정을 따른다.
	// +optional
	TLS *TLSSpec `json:"tls,omitempty"`

	// SyncUserSecretRef — mongot 가 mongod 동기에 쓸 searchCoordinator 사용자 credential.
	// 미지정 시 operator 가 search-sync 사용자(searchCoordinator role)를 자동 생성·관리한다.
	// +optional
	SyncUserSecretRef *corev1.LocalObjectReference `json:"syncUserSecretRef,omitempty"`

	// Pod — 스케줄링/보안 컨텍스트 등 pod-level 제어(기존 PodSpec 패턴 재사용).
	// +optional
	Pod *PodSpec `json:"pod,omitempty"`

	// Router — Sharded source 전용. mongos 가 연결할 mongot 을 *단일 shard 로 pin* 한다.
	// mongos 는 mongotHost 로 준 엔드포인트에 *직접* 연결하며 broadcast/fan-out 하지 않는다
	// (ADR-0039 #7 라이브 실측) → 로드밸런싱 엔드포인트를 주면 질의가 비결정적으로 임의 shard
	// mongot 에 도달해 빈 결과(조용한 오답)를 낼 수 있다. 따라서 결정적 단일 shard pin 이 필수.
	// 미지정 시 shard-0. ReplicaSet source 에서는 무시(사이드카 localhost 직결).
	// +optional
	Router *SearchRouterSpec `json:"router,omitempty"`
}

// SearchRouterSpec — Sharded 전용 mongos → mongot 라우팅 pin.
type SearchRouterSpec struct {
	// MongotShard — mongos 가 연결할 mongot 이 상주하는 shard(예 "shard-0").
	// **검색 대상 컬렉션이 상주하는 shard(=그 database 의 primary shard)로 지정해야 한다.**
	// 다른 shard 를 지정하면 인덱스 관리는 되더라도 $search/$vectorSearch 가 빈 결과를 낸다.
	// multi-shard 분산 컬렉션 검색은 Community mongot upstream 한계로 여전히 미해결(ADR-0039).
	// +kubebuilder:validation:Pattern=`^shard-[0-9]+$`
	// +kubebuilder:default="shard-0"
	// +optional
	MongotShard string `json:"mongotShard,omitempty"`
}

// SearchSource — mongot 색인 대상.
type SearchSource struct {
	// MongoDBResourceRef — 동일 namespace 의 MongoDB/MongoDBSharded CR 참조.
	// +optional
	MongoDBResourceRef *corev1.LocalObjectReference `json:"mongodbResourceRef,omitempty"`

	// Kind — 참조 CR 종류.
	// +kubebuilder:validation:Enum=MongoDB;MongoDBSharded
	// +kubebuilder:default=MongoDB
	Kind string `json:"kind,omitempty"`
}

// MongotVersion — mongot 이미지 결정.
type MongotVersion struct {
	// Version — mongot 릴리스 태그(mongo 8.2+ 와 호환되어야 함).
	// +optional
	Version string `json:"version,omitempty"`

	// Image — 전체 이미지 오버라이드(Version 보다 우선).
	// +optional
	Image string `json:"image,omitempty"`
}

// MongoDBSearchStatus — mongot 배포/동기 상태.
type MongoDBSearchStatus struct {
	// Phase: Pending | Provisioning | Syncing | Ready | Degraded | Failed
	// +kubebuilder:validation:Enum=Pending;Provisioning;Syncing;Ready;Degraded;Failed
	Phase string `json:"phase,omitempty"`

	// MongotEndpoint — mongod 가 연결할 mongot service(host:27028).
	// +optional
	MongotEndpoint string `json:"mongotEndpoint,omitempty"`

	// ReadyReplicas — Ready 상태 mongot pod 수.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// ObservedGeneration — 마지막 reconcile 한 spec generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Error — 실패 사유.
	// +optional
	Error string `json:"error,omitempty"`

	// Conditions follow standard k8s convention.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mdbsearch
// +kubebuilder:printcolumn:name="Source",type="string",JSONPath=".spec.source.mongodbResourceRef.name"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".status.mongotEndpoint"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// MongoDBSearch is the Schema for self-managed MongoDB Search / Vector Search (mongot).
type MongoDBSearch struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MongoDBSearchSpec   `json:"spec,omitempty"`
	Status MongoDBSearchStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MongoDBSearchList contains a list of MongoDBSearch.
type MongoDBSearchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDBSearch `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDBSearch{}, &MongoDBSearchList{})
}
