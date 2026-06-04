/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package insights

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
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

// Codex re-review (RFC-0045) #4 fix 회귀 가드 — ConvertProfile 경로에서
// bson.A 가 잔존하는 진본 시나리오. NormalizeMap 은 nested bson.A 를 그대로
// 두므로 countBoolClauses 가 `[]any` 만 봤다면 miss. NormalizeSlice 경유로 fix.
func TestAnalyze_SchemaHintViaConvertProfileWithBsonA(t *testing.T) {
	// bson.M 전체를 ConvertProfile 에 통과 — Filter 의 $or 가 bson.A 잔존.
	clauses := bson.A{
		bson.M{"a": 1}, bson.M{"b": 2}, bson.M{"c": 3},
		bson.M{"d": 4}, bson.M{"e": 5}, bson.M{"f": 6},
	}
	m := bson.M{
		"op":           "query",
		"ns":           "log.events",
		"millis":       int64(100),
		"planSummary":  "IXSCAN",
		"filter":       bson.M{"$or": clauses},
		"docsExamined": int64(10),
		"nreturned":    int64(6),
	}
	doc := ConvertProfile(m)
	// Filter["$or"] 가 bson.A 그대로 잔존하는지 verify (NormalizeMap 의 shallow copy 동작).
	if _, ok := doc.Filter["$or"].(bson.A); !ok {
		t.Logf("nested $or type=%T (NormalizeMap 가 nested 변환 시 본 가정 변경)", doc.Filter["$or"])
	}
	recs := Analyze([]ProfileDoc{doc}, 100)
	var found bool
	for _, r := range recs {
		if r.Type == "SchemaHint" {
			found = true
		}
	}
	if !found {
		t.Errorf("bson.A $or 6절 → SchemaHint emit 기대, recs=%+v", recs)
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

// Codex review #3 fix 회귀 가드 — mixed sample (일부 below threshold) 도
// 그룹 평균이 threshold ≥ 면 emit.
func TestAnalyze_SlowQueryPatternUsesGroupAverage(t *testing.T) {
	// 5건 동일 (ns, filterShape). latency = {50, 600, 700, 800, 50}
	// → avg = 440 < threshold 500 → emit 금지.
	docs := []ProfileDoc{
		{Op: "query", NS: "app.users", Millis: 50, PlanSummary: "IXSCAN",
			Filter: map[string]any{"email": "a@x.com"}, DocsExamined: 1, NReturned: 1},
		{Op: "query", NS: "app.users", Millis: 600, PlanSummary: "IXSCAN",
			Filter: map[string]any{"email": "b@x.com"}, DocsExamined: 1, NReturned: 1},
		{Op: "query", NS: "app.users", Millis: 700, PlanSummary: "IXSCAN",
			Filter: map[string]any{"email": "c@x.com"}, DocsExamined: 1, NReturned: 1},
		{Op: "query", NS: "app.users", Millis: 800, PlanSummary: "IXSCAN",
			Filter: map[string]any{"email": "d@x.com"}, DocsExamined: 1, NReturned: 1},
		{Op: "query", NS: "app.users", Millis: 50, PlanSummary: "IXSCAN",
			Filter: map[string]any{"email": "e@x.com"}, DocsExamined: 1, NReturned: 1},
	}
	recs := Analyze(docs, 500)
	for _, r := range recs {
		if r.Type == "SlowQueryPattern" {
			t.Errorf("group avg 440 < 500 → emit 금지, got %+v", r)
		}
	}

	// 같은 5건이지만 threshold=400 일 때는 avg=440 ≥ 400 → emit.
	recs2 := Analyze(docs, 400)
	var found int
	for _, r := range recs2 {
		if r.Type == "SlowQueryPattern" {
			found++
			if r.AvgLatencyMs != 440 {
				t.Errorf("avg 440 기대, got %d", r.AvgLatencyMs)
			}
		}
	}
	if found != 1 {
		t.Errorf("threshold 400 시 1건 emit 기대, got %d", found)
	}
}

// Codex review #4 fix 회귀 가드 — NReturned=0 + 대량 scan → MissingIndex.
func TestAnalyze_ZeroReturnedHighScanEmitsMissingIndex(t *testing.T) {
	docs := []ProfileDoc{
		{Op: "query", NS: "shop.orders", Millis: 350, PlanSummary: "IXSCAN { status: 1 }",
			Filter:       map[string]any{"status": "deleted"},
			DocsExamined: 50000, NReturned: 0},
	}
	recs := Analyze(docs, 1000)
	var found bool
	for _, r := range recs {
		if r.Type == "MissingIndex" {
			found = true
		}
	}
	if !found {
		t.Errorf("NReturned=0 + DocsExamined=50000 → MissingIndex 기대, recs=%+v", recs)
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

func TestAnalyzeIndexUsage_DetectsUnused(t *testing.T) {
	stats := []IndexStat{
		{NS: "db.users", IndexName: "_id_", Accesses: 100},
		{NS: "db.users", IndexName: "email_1", Accesses: 50},
		{NS: "db.users", IndexName: "old_field_1", Accesses: 0},
		{NS: "db.orders", IndexName: "status_1", Accesses: 0},
	}
	recs := AnalyzeIndexUsage(stats)
	if len(recs) != 2 {
		t.Fatalf("expected 2 unused index recommendations, got %d", len(recs))
	}
	for _, r := range recs {
		if r.Type != "UnusedIndex" {
			t.Errorf("expected type UnusedIndex, got %s", r.Type)
		}
		if r.Severity != "warning" {
			t.Errorf("expected severity warning, got %s", r.Severity)
		}
	}
}

func TestAnalyzeIndexUsage_SkipsIdIndex(t *testing.T) {
	stats := []IndexStat{
		{NS: "db.x", IndexName: "_id_", Accesses: 0},
	}
	recs := AnalyzeIndexUsage(stats)
	if len(recs) != 0 {
		t.Fatalf("_id_ index should be skipped, got %d recommendations", len(recs))
	}
}

func TestAnalyzeIndexUsage_EmptyInput(t *testing.T) {
	recs := AnalyzeIndexUsage(nil)
	if recs != nil {
		t.Fatalf("expected nil for empty input, got %v", recs)
	}
}

func TestAnalyzeIndexUsage_AllUsed(t *testing.T) {
	stats := []IndexStat{
		{NS: "db.x", IndexName: "a_1", Accesses: 10},
		{NS: "db.x", IndexName: "b_1", Accesses: 1},
	}
	recs := AnalyzeIndexUsage(stats)
	if len(recs) != 0 {
		t.Fatalf("all indexes used, expected 0 recommendations, got %d", len(recs))
	}
}
