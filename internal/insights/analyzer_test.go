/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0.
*/

package insights

import (
	"testing"
)

func TestAnalyze_EmptyInputReturnsNil(t *testing.T) {
	if got := Analyze(nil, 100); got != nil {
		t.Fatalf("nil docs → nil, got %v", got)
	}
	if got := Analyze([]ProfileDoc{}, 100); got != nil {
		t.Fatalf("empty docs → nil, got %v", got)
	}
}

func TestAnalyze_CollscanEmitsMissingIndex(t *testing.T) {
	docs := []ProfileDoc{
		{
			Op: "query", NS: "shop.orders", Millis: 250,
			PlanSummary:  "COLLSCAN",
			Filter:       map[string]any{"status": "active"},
			DocsExamined: 50000, NReturned: 12,
		},
	}
	recs := Analyze(docs, 100)
	if len(recs) == 0 {
		t.Fatalf("COLLSCAN 시 Recommendation 1+ 기대, got 0")
	}
	var foundMissing bool
	for _, r := range recs {
		if r.Type == "MissingIndex" && r.DB == "shop" && r.Collection == "orders" {
			foundMissing = true
			if r.IndexSuggestion != "{status:1}" {
				t.Errorf("IndexSuggestion {status:1} 기대, got %q", r.IndexSuggestion)
			}
			if r.AvgLatencyMs != 250 {
				t.Errorf("AvgLatencyMs 250 기대, got %d", r.AvgLatencyMs)
			}
		}
	}
	if !foundMissing {
		t.Errorf("MissingIndex on shop.orders 기대, recs=%+v", recs)
	}
}

func TestAnalyze_SlowQueryPatternGroupsByFilterShape(t *testing.T) {
	// 동일 (ns, filter shape) 3건 + 평균 latency ≥ threshold → 1건 emit.
	docs := []ProfileDoc{
		{Op: "query", NS: "app.users", Millis: 600, PlanSummary: "IXSCAN { email: 1 }",
			Filter: map[string]any{"email": "a@x.com"}, DocsExamined: 1, NReturned: 1},
		{Op: "query", NS: "app.users", Millis: 700, PlanSummary: "IXSCAN { email: 1 }",
			Filter: map[string]any{"email": "b@x.com"}, DocsExamined: 1, NReturned: 1},
		{Op: "query", NS: "app.users", Millis: 800, PlanSummary: "IXSCAN { email: 1 }",
			Filter: map[string]any{"email": "c@x.com"}, DocsExamined: 1, NReturned: 1},
	}
	recs := Analyze(docs, 500)

	var slowCount int
	for _, r := range recs {
		if r.Type == "SlowQueryPattern" {
			slowCount++
			if r.AvgLatencyMs != 700 {
				t.Errorf("avg 700 기대, got %d", r.AvgLatencyMs)
			}
			if r.Severity != "warning" {
				t.Errorf("severity warning 기대 (avg 700), got %q", r.Severity)
			}
			if len(r.QuerySamples) != 3 {
				t.Errorf("samples 3건 기대, got %d", len(r.QuerySamples))
			}
		}
	}
	if slowCount != 1 {
		t.Fatalf("SlowQueryPattern 그룹 1건 기대, got %d (recs=%+v)", slowCount, recs)
	}
}

func TestAnalyze_SlowQueryBelowThresholdNoEmit(t *testing.T) {
	docs := []ProfileDoc{
		{Op: "query", NS: "app.users", Millis: 50, PlanSummary: "IXSCAN",
			Filter: map[string]any{"id": 1}, DocsExamined: 1, NReturned: 1},
	}
	recs := Analyze(docs, 100)
	for _, r := range recs {
		if r.Type == "SlowQueryPattern" {
			t.Fatalf("threshold 미만은 emit 금지, got %+v", r)
		}
	}
}

func TestAnalyze_SchemaHintOnManyOrClauses(t *testing.T) {
	docs := []ProfileDoc{
		{
			Op: "query", NS: "log.events", Millis: 200, PlanSummary: "IXSCAN",
			Filter: map[string]any{
				"$or": []any{
					map[string]any{"a": 1}, map[string]any{"b": 2},
					map[string]any{"c": 3}, map[string]any{"d": 4},
					map[string]any{"e": 5}, map[string]any{"f": 6},
				},
			},
			DocsExamined: 10, NReturned: 6,
		},
	}
	recs := Analyze(docs, 100)
	var found bool
	for _, r := range recs {
		if r.Type == "SchemaHint" && r.DB == "log" && r.Collection == "events" {
			found = true
		}
	}
	if !found {
		t.Errorf("SchemaHint emit 기대, recs=%+v", recs)
	}
}

func TestAnalyze_IndexSuggestionEqualityThenSort(t *testing.T) {
	// equality (filter) 키 → sort 키 후행. sort 의 dup 키는 제외.
	docs := []ProfileDoc{
		{Op: "query", NS: "shop.items", Millis: 300, PlanSummary: "COLLSCAN",
			Filter:       map[string]any{"category": "X"},
			Sort:         map[string]any{"createdAt": -1},
			DocsExamined: 9000, NReturned: 30},
	}
	recs := Analyze(docs, 100)
	var sug string
	for _, r := range recs {
		if r.Type == "MissingIndex" {
			sug = r.IndexSuggestion
		}
	}
	want := "{category:1, createdAt:1}"
	if sug != want {
		t.Errorf("IndexSuggestion %q 기대, got %q", want, sug)
	}
}

func TestAnalyze_HighExaminedRatioNoCollscan(t *testing.T) {
	// COLLSCAN 은 없지만 examined/returned 비율이 100 초과 → MissingIndex.
	docs := []ProfileDoc{
		{Op: "query", NS: "shop.orders", Millis: 80, PlanSummary: "IXSCAN { status: 1 }",
			Filter:       map[string]any{"status": "active", "tenant": "t1"},
			DocsExamined: 20000, NReturned: 50},
	}
	recs := Analyze(docs, 100)
	var found bool
	for _, r := range recs {
		if r.Type == "MissingIndex" {
			found = true
		}
	}
	if !found {
		t.Errorf("ratio > 100 시 MissingIndex 기대, recs=%+v", recs)
	}
}

func TestSeverityFromLatency(t *testing.T) {
	cases := []struct {
		ms   int32
		want string
	}{
		{50, "info"}, {499, "info"},
		{500, "warning"}, {999, "warning"},
		{1000, "critical"}, {5000, "critical"},
	}
	for _, c := range cases {
		if got := severityFromLatency(c.ms); got != c.want {
			t.Errorf("severityFromLatency(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
}
