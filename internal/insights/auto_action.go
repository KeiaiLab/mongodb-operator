/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0.
*/

// Package insights — auto_action.go: Level V Auto Pilot 자동 액션 (A3, A4).
//
// Insights Recommendation을 입력으로 받아 *실제 action plan*을 생성.
// 실 MongoDB 호출은 controller layer 책임. 본 함수는 순수 함수.
package insights

import (
	"sort"
	"strings"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// Recommendation Type 상수 (goconst — 5+ occurrences in package).
const (
	RecTypeMissingIndex     = "MissingIndex"
	RecTypeSlowQueryPattern = "SlowQueryPattern"
	RecTypeUnusedIndex      = "UnusedIndex"
	RecTypeSchemaHint       = "SchemaHint"
)

// IndexAction describes a planned createIndex() operation.
type IndexAction struct {
	NS       string // "db.collection"
	Keys     []string
	Severity string
	Reason   string
	DryRun   bool
}

// QueryHintAction describes a planned query hint application.
type QueryHintAction struct {
	NS       string
	Pattern  string
	Severity string
	Reason   string
	DryRun   bool
}

// PlanMissingIndexActions converts MissingIndex recommendations into createIndex() actions.
// Filters by minSeverity (info<warning<critical) and respects DryRun.
func PlanMissingIndexActions(recs []mongodbv1alpha1.Recommendation, spec *mongodbv1alpha1.AutoIndexSpec) []IndexAction {
	if spec == nil || !spec.Enabled {
		return nil
	}
	minSev := spec.MinSeverity
	if minSev == "" {
		minSev = SevWarning
	}
	var actions []IndexAction
	for _, r := range recs {
		if r.Type != RecTypeMissingIndex {
			continue
		}
		if !meetsSeverity(r.Severity, minSev) {
			continue
		}
		actions = append(actions, IndexAction{
			NS:       joinNS(r.DB, r.Collection),
			Keys:     extractKeys(r.Detail),
			Severity: r.Severity,
			Reason:   r.Detail,
			DryRun:   spec.DryRun,
		})
	}
	sort.Slice(actions, func(i, j int) bool {
		return actions[i].NS < actions[j].NS
	})
	return actions
}

// PlanSlowQueryHints converts SlowQueryPattern recommendations into hint actions.
func PlanSlowQueryHints(recs []mongodbv1alpha1.Recommendation, spec *mongodbv1alpha1.AutoQueryHintSpec) []QueryHintAction {
	if spec == nil || !spec.Enabled {
		return nil
	}
	var actions []QueryHintAction
	for _, r := range recs {
		if r.Type != RecTypeSlowQueryPattern {
			continue
		}
		if r.AvgLatencyMs < spec.SlowQueryThresholdMs {
			continue
		}
		actions = append(actions, QueryHintAction{
			NS:       joinNS(r.DB, r.Collection),
			Pattern:  r.Detail,
			Severity: r.Severity,
			Reason:   r.Detail,
			DryRun:   spec.DryRun,
		})
	}
	sort.Slice(actions, func(i, j int) bool {
		return actions[i].NS < actions[j].NS
	})
	return actions
}

// meetsSeverity returns true if got >= min (info<warning<critical).
func meetsSeverity(got, min string) bool {
	order := map[string]int{SevInfo: 0, SevWarning: 1, SevCritical: 2}
	gv, gok := order[got]
	mv, mok := order[min]
	if !gok || !mok {
		return false
	}
	return gv >= mv
}

// joinNS combines structured DB/Collection fields into "db.coll" namespace.
// 결함 #14: 기존 extractNS 는 Detail 문자열(NS 미포함, filter shape 만 존재)을
// 휴리스틱 파싱해 NS 를 추출하지 못했다. Recommendation 은 DB/Collection 을
// 별도 필드로 보존하므로 구조화 필드를 직접 사용한다. 둘 중 하나라도 비면
// (schema-level hint) 빈 문자열.
func joinNS(db, coll string) string {
	if db == "" || coll == "" {
		return ""
	}
	return db + "." + coll
}

// extractKeys extracts proposed index keys from recommendation Detail.
// Returns empty slice if no parseable keys found.
func extractKeys(detail string) []string {
	// Heuristic: look for "keys: [a, b]" pattern or "{a:1, b:1}"
	start := strings.Index(detail, "keys:")
	if start < 0 {
		return nil
	}
	rest := detail[start+5:]
	end := strings.IndexAny(rest, ".\n")
	if end > 0 {
		rest = rest[:end]
	}
	rest = strings.TrimSpace(strings.Trim(rest, "[]{}"))
	if rest == "" {
		return nil
	}
	parts := strings.Split(rest, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.Trim(p, "\"'"))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
