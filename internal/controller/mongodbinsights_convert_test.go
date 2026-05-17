/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0.
*/

// mongodbinsights_convert_test.go — Codex review (RFC-0045) #2 fix 회귀 가드.
// BSON variance (bson.M / bson.D / bson.A / []any) + command.{filter,q,pipeline}
// + command.sort + sort 위치 변동에 대한 unit test.

package controller

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestNormalizeMap_BSON_M_And_D(t *testing.T) {
	m := bson.M{"a": 1, "b": "x"}
	got := normalizeMap(m)
	if got["a"] != 1 || got["b"] != "x" {
		t.Errorf("bson.M 변환 실패, got %+v", got)
	}

	d := bson.D{{Key: "a", Value: 1}, {Key: "b", Value: "x"}}
	got = normalizeMap(d)
	if got["a"] != 1 || got["b"] != "x" {
		t.Errorf("bson.D 변환 실패, got %+v", got)
	}

	plain := map[string]any{"a": 1}
	got = normalizeMap(plain)
	if got["a"] != 1 {
		t.Errorf("map[string]any 변환 실패, got %+v", got)
	}

	if normalizeMap(nil) != nil {
		t.Errorf("nil → nil 기대")
	}
	if normalizeMap("not-a-map") != nil {
		t.Errorf("unknown 타입 → nil 기대")
	}
}

func TestNormalizeSlice_BSON_A(t *testing.T) {
	a := bson.A{"x", 1, true}
	got := normalizeSlice(a)
	if len(got) != 3 || got[0] != "x" {
		t.Errorf("bson.A 변환 실패, got %+v", got)
	}

	plain := []any{"y"}
	got = normalizeSlice(plain)
	if len(got) != 1 || got[0] != "y" {
		t.Errorf("[]any 변환 실패, got %+v", got)
	}

	if normalizeSlice(nil) != nil {
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
	d := convertProfile(m)
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
	// command op — filter 가 command.filter 안.
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
	d := convertProfile(m)
	if _, ok := d.Filter["status"]; !ok {
		t.Errorf("command.filter 추출 실패, got %+v", d.Filter)
	}
	if _, ok := d.Sort["id"]; !ok {
		t.Errorf("command.sort 추출 실패, got %+v", d.Sort)
	}
}

func TestConvertProfile_LegacyQueryQ(t *testing.T) {
	// legacy: command.q 위치 (predates 'filter').
	m := bson.M{
		"op": "command",
		"ns": "shop.orders",
		"command": bson.M{
			"q": bson.M{"region": "us"},
		},
	}
	d := convertProfile(m)
	if _, ok := d.Filter["region"]; !ok {
		t.Errorf("command.q legacy 추출 실패, got %+v", d.Filter)
	}
}

func TestConvertProfile_AggregationPipelineMatch(t *testing.T) {
	// aggregation: command.pipeline[0].$match.
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
	d := convertProfile(m)
	if _, ok := d.Filter["severity"]; !ok {
		t.Errorf("aggregation $match 추출 실패, got %+v", d.Filter)
	}
}

func TestConvertProfile_BSON_D_Filter(t *testing.T) {
	// driver 가 decode mode 에 따라 bson.D 로 디코딩하는 경우.
	m := bson.M{
		"op":     "query",
		"ns":     "app.users",
		"filter": bson.D{{Key: "tenant", Value: "t1"}},
	}
	d := convertProfile(m)
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
		if got := readInt64Any(c.in); got != c.want {
			t.Errorf("readInt64Any(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}
