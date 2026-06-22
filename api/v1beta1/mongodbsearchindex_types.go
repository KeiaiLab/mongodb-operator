/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// mongodbsearchindex_types.go — Phase 1: search/vector 인덱스 선언적 관리 CRD.
//
// MongoDBSearch 가 mongot 을 *배포*한다면, MongoDBSearchIndex 는 그 위에 생성할
// `$search`(full-text) / `$vectorSearch`(AI 임베딩) 인덱스를 *선언* 한다. controller 가
// source 클러스터 mongod 에 `db.collection.createSearchIndex({name,type,definition})` 를
// reconcile 하여 명령형 createSearchIndex 호출을 GitOps 선언으로 대체한다.

package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MongoDBSearchIndexSpec — 한 컬렉션의 search/vector 인덱스 1개.
type MongoDBSearchIndexSpec struct {
	// SearchRef — 이 인덱스를 호스팅할 MongoDBSearch(mongot) 참조.
	SearchRef corev1.LocalObjectReference `json:"searchRef"`

	// Database 대상 database.
	// +kubebuilder:validation:MinLength=1
	Database string `json:"database"`

	// Collection 대상 컬렉션.
	// +kubebuilder:validation:MinLength=1
	Collection string `json:"collection"`

	// IndexName — search index 이름($search/$vectorSearch 의 index 인자).
	// +kubebuilder:validation:MinLength=1
	IndexName string `json:"indexName"`

	// Type — search(full-text/Lucene) 또는 vectorSearch(AI 임베딩).
	// +kubebuilder:validation:Enum=search;vectorSearch
	// +kubebuilder:default=search
	Type string `json:"type,omitempty"`

	// DefinitionJSON — 인덱스 정의를 JSON 으로(createSearchIndex 의 definition).
	// search: {"mappings":{...}}. vectorSearch: {"fields":[{"type":"vector","path":..,"similarity":"cosine","numDimensions":1536}]}.
	// 정의 유효성은 mongot 가 createSearchIndex 시 검증(VerificationQuery.FilterJSON 동일 raw-JSON 컨벤션).
	// +kubebuilder:validation:MinLength=2
	DefinitionJSON string `json:"definitionJSON"`
}

// MongoDBSearchIndexStatus — 인덱스 빌드 상태.
type MongoDBSearchIndexStatus struct {
	// Phase: Pending | Building | Ready | Failed | Deleting
	// +kubebuilder:validation:Enum=Pending;Building;Ready;Failed;Deleting
	Phase string `json:"phase,omitempty"`

	// IndexID — mongot 가 할당한 search index id(존재 시).
	// +optional
	IndexID string `json:"indexID,omitempty"`

	// Queryable — 인덱스가 쿼리 가능 상태($search/$vectorSearch 응답 가능).
	// +optional
	Queryable bool `json:"queryable,omitempty"`

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
// +kubebuilder:resource:shortName=mdbsearchidx
// +kubebuilder:printcolumn:name="Search",type="string",JSONPath=".spec.searchRef.name"
// +kubebuilder:printcolumn:name="Collection",type="string",JSONPath=".spec.collection"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// MongoDBSearchIndex is the Schema for declarative $search / $vectorSearch indexes.
type MongoDBSearchIndex struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MongoDBSearchIndexSpec   `json:"spec,omitempty"`
	Status MongoDBSearchIndexStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MongoDBSearchIndexList contains a list of MongoDBSearchIndex.
type MongoDBSearchIndexList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDBSearchIndex `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDBSearchIndex{}, &MongoDBSearchIndexList{})
}
