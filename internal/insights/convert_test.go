/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0.
*/

// convert_test.go — BSON variance 회귀 가드 (Codex review (RFC-0045) #2 fix).
// ProfileFetcher refactor 시 controller 패키지에서 본 패키지로 이전 — insights
// 가 *수집 + 변환 + 분석* 3 단계를 자기-격리 단위로 보유.

package insights

import (
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestNormalizeMap_BSON_M_And_D(t *testing.T) {
	m := bson.M{"a": 1, "b": "x"}
	got := NormalizeMap(m)
	if got["a"] != 1 || got["b"] != "x" {
		t.Errorf("bson.M 변환 실패, got %+v", got)
	}

	d := bson.D{{Key: "a", Value: 1}, {Key: "b", Value: "x"}}
	got = NormalizeMap(d)
	if got["a"] != 1 || got["b"] != "x" {
		t.Errorf("bson.D 변환 실패, got %+v", got)
	}

	plain := map[string]any{"a": 1}
	got = NormalizeMap(plain)
	if got["a"] != 1 {
		t.Errorf("map[string]any 변환 실패, got %+v", got)
	}

	if NormalizeMap(nil) != nil {
		t.Errorf("nil → nil 기대")
	}
	if NormalizeMap("not-a-map") != nil {
		t.Errorf("unknown 타입 → nil 기대")
	}
}

func TestNormalizeSlice_BSON_A(t *testing.T) {
	a := bson.A{"x", 1, true}
	got := NormalizeSlice(a)
	if len(got) != 3 || got[0] != "x" {
		t.Errorf("bson.A 변환 실패, got %+v", got)
	}

	plain := []any{"y"}
	got = NormalizeSlice(plain)
	if len(got) != 1 || got[0] != "y" {
		t.Errorf("[]any 변환 실패, got %+v", got)
	}

	if NormalizeSlice(nil) != nil {
		t.Errorf("nil → nil 기대")
	}
}

func TestConvertProfile_TopLevelFilter(t *testing.T) {
	m := bson.M{
		"op":           "query",
		"ns":           "app.users",
		"millis":       int64(120),
		"planSummary":  "COLLSCAN",
		"filter":       bson.M{"email": "x@y.com"},
		"sort":         bson.M{"createdAt": -1},
		"docsExamined": int64(9999),
		"nreturned":    int64(1),
	}
	d := ConvertProfile(m)
	if d.Op != "query" || d.NS != "app.users" || d.Millis != 120 || d.PlanSummary != "COLLSCAN" {
		t.Errorf("기본 필드 변환 실패, got %+v", d)
	}
	if _, ok := d.Filter["email"]; !ok {
		t.Errorf("Filter.email 누락, got %+v", d.Filter)
	}
	if _, ok := d.Sort["createdAt"]; !ok {
		t.Errorf("Sort.createdAt 누락, got %+v", d.Sort)
	}
	if d.DocsExamined != 9999 || d.NReturned != 1 {
		t.Errorf("examined/returned 실패, got %+v", d)
	}
}

func TestConvertProfile_CommandFilter(t *testing.T) {
	m := bson.M{
		"op": "command",
		"ns": "app.users",
		"command": bson.M{
			"find":   "users",
			"filter": bson.M{"status": "active"},
			"sort":   bson.M{"id": 1},
		},
		"millis": int32(50),
	}
	d := ConvertProfile(m)
	if _, ok := d.Filter["status"]; !ok {
		t.Errorf("command.filter 추출 실패, got %+v", d.Filter)
	}
	if _, ok := d.Sort["id"]; !ok {
		t.Errorf("command.sort 추출 실패, got %+v", d.Sort)
	}
}

func TestConvertProfile_LegacyQueryQ(t *testing.T) {
	m := bson.M{
		"op": "command",
		"ns": "shop.orders",
		"command": bson.M{
			"q": bson.M{"region": "us"},
		},
	}
	d := ConvertProfile(m)
	if _, ok := d.Filter["region"]; !ok {
		t.Errorf("command.q legacy 추출 실패, got %+v", d.Filter)
	}
}

func TestConvertProfile_AggregationPipelineMatch(t *testing.T) {
	m := bson.M{
		"op": "command",
		"ns": "log.events",
		"command": bson.M{
			"aggregate": "events",
			"pipeline": bson.A{
				bson.M{"$match": bson.M{"severity": "error"}},
				bson.M{"$group": bson.M{"_id": "$tag"}},
			},
		},
	}
	d := ConvertProfile(m)
	if _, ok := d.Filter["severity"]; !ok {
		t.Errorf("aggregation $match 추출 실패, got %+v", d.Filter)
	}
}

func TestConvertProfile_BSON_D_Filter(t *testing.T) {
	m := bson.M{
		"op":     "query",
		"ns":     "app.users",
		"filter": bson.D{{Key: "tenant", Value: "t1"}},
	}
	d := ConvertProfile(m)
	if _, ok := d.Filter["tenant"]; !ok {
		t.Errorf("bson.D filter 추출 실패, got %+v", d.Filter)
	}
}

func TestReadInt64Any(t *testing.T) {
	cases := []struct {
		in   any
		want int64
	}{
		{int32(7), 7}, {int64(99), 99}, {float64(42.9), 42}, {int(5), 5},
		{nil, 0}, {"x", 0},
	}
	for _, c := range cases {
		if got := ReadInt64Any(c.in); got != c.want {
			t.Errorf("ReadInt64Any(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// --- $indexStats 변환 (UnusedIndex 통합, ROADMAP §3.2) ---

func TestConvertIndexStat_NameAndAccesses(t *testing.T) {
	m := bson.M{"name": "email_1", "accesses": bson.M{"ops": int64(42)}}
	got := ConvertIndexStat("shop.users", m)
	if got.NS != "shop.users" || got.IndexName != "email_1" || got.Accesses != 42 {
		t.Errorf("ConvertIndexStat 기본 추출 실패, got %+v", got)
	}
}

func TestConvertIndexStat_ZeroAccessesWhenMissing(t *testing.T) {
	// accesses 필드 부재 → 0 (UnusedIndex 후보).
	m := bson.M{"name": "stale_idx"}
	got := ConvertIndexStat("shop.users", m)
	if got.IndexName != "stale_idx" || got.Accesses != 0 {
		t.Errorf("accesses 부재 시 0 기대, got %+v", got)
	}
}

func TestConvertIndexStat_BSON_D_Accesses(t *testing.T) {
	// decode 모드별 accesses 가 bson.D 변종 → NormalizeMap 흡수.
	m := bson.M{"name": "compound_1", "accesses": bson.D{{Key: "ops", Value: int32(7)}}}
	got := ConvertIndexStat("db.coll", m)
	if got.Accesses != 7 {
		t.Errorf("bson.D accesses 추출 실패, got %+v", got)
	}
}

func TestConvertIndexStat_ComposesWithAnalyzeIndexUsage(t *testing.T) {
	// $indexStats row → ConvertIndexStat → AnalyzeIndexUsage end-to-end (순수).
	// _id_ skip + 0-access unused 검출 검증.
	stats := []IndexStat{
		ConvertIndexStat("app.users", bson.M{"name": "_id_", "accesses": bson.M{"ops": int64(0)}}),
		ConvertIndexStat("app.users", bson.M{"name": "unused_1", "accesses": bson.M{"ops": int64(0)}}),
		ConvertIndexStat("app.users", bson.M{"name": "hot_1", "accesses": bson.M{"ops": int64(999)}}),
	}
	recs := AnalyzeIndexUsage(stats)
	if len(recs) != 1 {
		t.Fatalf("UnusedIndex 1건 기대 (_id_ + hot 제외), got %d: %+v", len(recs), recs)
	}
	if recs[0].Type != RecTypeUnusedIndex {
		t.Errorf("Type RecTypeUnusedIndex 기대, got %q", recs[0].Type)
	}
	if !strings.Contains(recs[0].Detail, "unused_1") {
		t.Errorf("Detail 에 unused_1 기대, got %q", recs[0].Detail)
	}
}
