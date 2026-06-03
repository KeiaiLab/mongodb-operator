/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// mongodbinsights_types.go — F51-F55 (cycle 7) MongoDBInsights CRD.
//
// 대상 cluster 의 시스템 컬렉션 (system.profile / serverStatus) 을 주기적
// 수집하여 *slow query*, *missing index*, *schema design hint* 권장을
// 사용자에게 노출. mongodb-operator 의 *advisory* 컨트롤러.

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MongoDBInsightsSpec — F51 (cycle 7) 분석 정책.
type MongoDBInsightsSpec struct {
	// ClusterRef references the target MongoDB or MongoDBSharded.
	ClusterRef ClusterReference `json:"clusterRef"`

	// TargetKind 는 분석 대상 CRD 종류. MongoDB 또는 MongoDBSharded.
	// #226: MongoDBSharded 지원 — sharded cluster 의 각 shard + mongos 의
	// system.profile 을 수집하여 통합 분석.
	// +kubebuilder:validation:Enum=MongoDB;MongoDBSharded
	// +kubebuilder:default="MongoDB"
	// +optional
	TargetKind string `json:"targetKind,omitempty"`

	// ProfilingLevel = 0 (off) | 1 (slow only) | 2 (all). 기본 1.
	// +kubebuilder:validation:Enum=0;1;2
	// +kubebuilder:default=1
	ProfilingLevel int32 `json:"profilingLevel,omitempty"`

	// SlowQueryThresholdMs — system.profile 에 기록될 최소 latency.
	// +kubebuilder:default=100
	SlowQueryThresholdMs int32 `json:"slowQueryThresholdMs,omitempty"`

	// SampleSize — 각 reconcile cycle 당 분석할 system.profile 문서 수.
	// +kubebuilder:default=500
	SampleSize int32 `json:"sampleSize,omitempty"`

	// AnalysisInterval — reconcile 간격.
	// +kubebuilder:default="15m"
	AnalysisInterval string `json:"analysisInterval,omitempty"`

	// Enabled toggles the insights collector. 기본 true.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// AnalysisCredentialsSecretRef — system.profile 읽기 권한 user.
	// 미설정 시 cluster 의 admin credentials 재사용.
	// +optional
	AnalysisCredentialsSecretRef *corev1.LocalObjectReference `json:"analysisCredentialsSecretRef,omitempty"`

	// AutoIndex — MissingIndex 권장에 대한 Auto Pilot 인덱스 조치 정책.
	// 기본 dry-run (표면화만, 실행 없음 — DryRun 기본 true). nil 이면 비활성.
	// +optional
	AutoIndex *AutoIndexSpec `json:"autoIndex,omitempty"`

	// AutoQueryHint — SlowQueryPattern 권장에 대한 Auto Pilot hint 정책.
	// 기본 dry-run. nil 이면 비활성.
	// +optional
	AutoQueryHint *AutoQueryHintSpec `json:"autoQueryHint,omitempty"`
}

// MongoDBInsightsStatus — 분석 결과.
type MongoDBInsightsStatus struct {
	// Phase: Pending | Analyzing | Ready | Failed
	// +kubebuilder:validation:Enum=Pending;Analyzing;Ready;Failed
	Phase string `json:"phase,omitempty"`

	// LastAnalysisTime 마지막 분석 완료 시점.
	// +optional
	LastAnalysisTime *metav1.Time `json:"lastAnalysisTime,omitempty"`

	// Recommendations — 인덱스 추가 / schema 개선 / slow query 패턴.
	// +optional
	Recommendations []Recommendation `json:"recommendations,omitempty"`

	// SlowQueriesSampled — 마지막 cycle 에 분석된 slow query 수.
	// +optional
	SlowQueriesSampled int32 `json:"slowQueriesSampled,omitempty"`

	// AutoPilotActions — Auto Pilot 가 Recommendations 기반으로 계획한 조치.
	// 기본 DryRun=true (표면화만 — 비가역 운영 자동 실행 방지). Spec.AutoIndex /
	// AutoQueryHint 활성 시에만 채워짐.
	// +optional
	AutoPilotActions []AutoPilotAction `json:"autoPilotActions,omitempty"`

	// Conditions follow standard k8s convention.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Recommendation — 분석 결과 1건.
type Recommendation struct {
	// Type = MissingIndex | UnusedIndex | SlowQueryPattern | SchemaHint
	// +kubebuilder:validation:Enum=MissingIndex;UnusedIndex;SlowQueryPattern;SchemaHint
	Type string `json:"type"`

	// Severity = info | warning | critical
	// +kubebuilder:validation:Enum=info;warning;critical
	// +kubebuilder:default="info"
	Severity string `json:"severity,omitempty"`

	// DB + Collection (optional — schema-level hint 시 비어있음).
	// +optional
	DB string `json:"db,omitempty"`
	// +optional
	Collection string `json:"collection,omitempty"`

	// Detail human-readable 권장. e.g. "create compound index on (status, createdAt)"
	Detail string `json:"detail"`

	// IndexSuggestion BSON-style key spec (MissingIndex 시).
	// +optional
	IndexSuggestion string `json:"indexSuggestion,omitempty"`

	// AvgLatencyMs 권장 근거 latency.
	// +optional
	AvgLatencyMs int32 `json:"avgLatencyMs,omitempty"`

	// QuerySamples — sample query string (max 3).
	// +optional
	QuerySamples []string `json:"querySamples,omitempty"`
}

// AutoPilotAction — Auto Pilot 가 Recommendation 으로부터 계획한 자동 조치 1건.
// DryRun=true (기본) 면 표면화만 하고 실행하지 않는다 — 비가역 운영 안전 기본값
// (AutoIndexSpec/AutoQueryHintSpec 의 DryRun 기본 true 정합).
type AutoPilotAction struct {
	// Type = MissingIndex (인덱스 생성 계획) | SlowQueryHint (쿼리 hint 계획)
	// +kubebuilder:validation:Enum=MissingIndex;SlowQueryHint
	Type string `json:"type"`

	// NS = "db.collection" 대상 (파싱 가능 시).
	// +optional
	NS string `json:"ns,omitempty"`

	// Severity = info | warning | critical
	// +optional
	Severity string `json:"severity,omitempty"`

	// Detail human-readable 조치 근거.
	Detail string `json:"detail"`

	// DryRun true 면 표면화만 (실행 안 함). 기본 정책.
	DryRun bool `json:"dryRun"`
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mdbinsights
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Recommendations",type="integer",JSONPath=".status.recommendations[*].type"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// MongoDBInsights is the Schema for advisory analysis.
type MongoDBInsights struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MongoDBInsightsSpec   `json:"spec,omitempty"`
	Status MongoDBInsightsStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MongoDBInsightsList contains a list of MongoDBInsights.
type MongoDBInsightsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDBInsights `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MongoDBInsights{}, &MongoDBInsightsList{})
}
