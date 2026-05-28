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
		minSev = "warning"
	}
	var actions []IndexAction
	for _, r := range recs {
		if r.Type != "MissingIndex" {
			continue
		}
		if !meetsSeverity(r.Severity, minSev) {
			continue
		}
		actions = append(actions, IndexAction{
			NS:       extractNS(r.Detail),
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
		if r.Type != "SlowQueryPattern" {
			continue
		}
		if r.AvgLatencyMs < spec.SlowQueryThresholdMs {
			continue
		}
		actions = append(actions, QueryHintAction{
			NS:       extractNS(r.Detail),
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
	order := map[string]int{"info": 0, "warning": 1, "critical": 2}
	gv, gok := order[got]
	mv, mok := order[min]
	if !gok || !mok {
		return false
	}
	return gv >= mv
}

// extractNS extracts "db.coll" from recommendation Detail string.
// Best-effort heuristic — controller validates before action.
func extractNS(detail string) string {
	for _, token := range strings.Fields(detail) {
		token = strings.Trim(token, ":,.\"'")
		if strings.Count(token, ".") == 1 && !strings.HasPrefix(token, ".") {
			return token
		}
	}
	return ""
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
