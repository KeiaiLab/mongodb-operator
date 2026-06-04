/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// autopilot_advisory.go — Auto Pilot A등급 advisory (ROADMAP §3.2 / Level V).
//
// MongoDBInsights 의 Recommendations 를 Auto Pilot 조치 *계획* 으로 변환하여
// Status.AutoPilotActions 에 표면화한다. 비가역 운영(인덱스 생성/쿼리 hint)의
// 자동 실행은 spec.AutoIndex/AutoQueryHint 의 DryRun(기본 true) 정책에 따르며,
// 본 advisory 단계는 *계획 표면화* 만 수행 — 실 실행은 후속(execution) 단계.

package controller

import (
	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/insights"
)

// buildAutoPilotActions — Recommendations + Auto Pilot 정책 → 조치 계획.
//
// insights.PlanMissingIndexActions (MissingIndex→createIndex 계획) +
// PlanSlowQueryHints (SlowQueryPattern→hint 계획) 를 surface. UnusedIndex 는
// drop 이 비가역이라 자동 계획에서 제외 (advisory Recommendation 으로만 노출).
// nil/disabled spec 은 insights.Plan* 내부 guard 가 nil 반환 → 안전.
func buildAutoPilotActions(
	recs []mongodbv1alpha1.Recommendation,
	autoIdx *mongodbv1alpha1.AutoIndexSpec,
	autoHint *mongodbv1alpha1.AutoQueryHintSpec,
) []mongodbv1alpha1.AutoPilotAction {
	out := make([]mongodbv1alpha1.AutoPilotAction, 0, len(recs))
	for _, a := range insights.PlanMissingIndexActions(recs, autoIdx) {
		out = append(out, mongodbv1alpha1.AutoPilotAction{
			Type:     "MissingIndex",
			NS:       a.NS,
			Severity: a.Severity,
			Detail:   a.Reason,
			DryRun:   a.DryRun,
		})
	}
	for _, a := range insights.PlanSlowQueryHints(recs, autoHint) {
		out = append(out, mongodbv1alpha1.AutoPilotAction{
			Type:     "SlowQueryHint",
			NS:       a.NS,
			Severity: a.Severity,
			Detail:   a.Reason,
			DryRun:   a.DryRun,
		})
	}
	// 빈 결과는 nil 로 정규화 — Status.AutoPilotActions omitempty 정합 + 비활성/무조치
	// 시 빈 배열 노출 방지.
	if len(out) == 0 {
		return nil
	}
	return out
}
