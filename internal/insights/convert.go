/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// convert.go — bson.M (system.profile row) → ProfileDoc 변환 helper.
//
// ProfileFetcher refactor (Codex review #5 근본 fix) 일환으로 controller
// 패키지에서 이전. insights 패키지가 *수집 + 변환 + 분석* 3 단계를 자기-격리
// 단위로 보유. controller 는 fetcher.Fetch + Analyze 2-step 만.

package insights

import (
	"go.mongodb.org/mongo-driver/v2/bson"
)

// ConvertProfile — bson.M (system.profile row) → ProfileDoc.
//
// BSON 의 variance 대응 (Codex review (RFC-0045) #2 fix):
//   - filter shape 후보: top-level `filter` | `command.filter` |
//     `command.q` (legacy) | `command.pipeline[0].$match` (aggregation)
//   - sort shape 후보: top-level `sort` | `command.sort`
//   - 값 타입 후보: bson.M | bson.D | map[string]any (driver 버전·decode 모드별)
//   - 배열 타입 후보: bson.A | []any | []interface{}
func ConvertProfile(m bson.M) ProfileDoc {
	d := ProfileDoc{}
	if v, ok := m["op"].(string); ok {
		d.Op = v
	}
	if v, ok := m["ns"].(string); ok {
		d.NS = v
	}
	d.Millis = int32(ReadInt64Any(m["millis"]))
	if v, ok := m["planSummary"].(string); ok {
		d.PlanSummary = v
	}

	cmd := NormalizeMap(m["command"])
	switch {
	case m["filter"] != nil:
		d.Filter = NormalizeMap(m["filter"])
	case cmd != nil && cmd["filter"] != nil:
		d.Filter = NormalizeMap(cmd["filter"])
	case cmd != nil && cmd["q"] != nil:
		// legacy `query` op 의 filter 위치.
		d.Filter = NormalizeMap(cmd["q"])
	case cmd != nil && cmd["pipeline"] != nil:
		// aggregation: pipeline[0] 의 $match.
		if arr := NormalizeSlice(cmd["pipeline"]); len(arr) > 0 {
			if stage := NormalizeMap(arr[0]); stage != nil {
				if mt, ok := stage["$match"]; ok {
					d.Filter = NormalizeMap(mt)
				}
			}
		}
	}

	switch {
	case m["sort"] != nil:
		d.Sort = NormalizeMap(m["sort"])
	case cmd != nil && cmd["sort"] != nil:
		d.Sort = NormalizeMap(cmd["sort"])
	}

	d.DocsExamined = ReadInt64Any(m["docsExamined"])
	d.NReturned = ReadInt64Any(m["nreturned"])
	if d.NReturned == 0 {
		// command op 의 경우 nReturned 대소문자 변종.
		d.NReturned = ReadInt64Any(m["nReturned"])
	}
	d.KeysExamined = ReadInt64Any(m["keysExamined"])
	return d
}

// ConvertIndexStat — bson.M ($indexStats row) → IndexStat (UnusedIndex 분석 입력).
//
// $indexStats row 는 name + accesses.ops (인덱스 접근 카운터) 를 가지며, ops 가
// 0 이면 미사용 인덱스 후보다. accesses 는 decode 모드별 bson.M | bson.D 변종이라
// NormalizeMap 으로 흡수한다 (ConvertProfile 의 BSON variance 패턴 정합).
func ConvertIndexStat(ns string, m bson.M) IndexStat {
	st := IndexStat{NS: ns}
	if v, ok := m["name"].(string); ok {
		st.IndexName = v
	}
	if acc := NormalizeMap(m["accesses"]); acc != nil {
		st.Accesses = ReadInt64Any(acc["ops"])
	}
	return st
}

// NormalizeMap — bson.M | bson.D | map[string]any → map[string]any.
// nil 또는 인식 불가 시 nil 반환.
func NormalizeMap(v any) map[string]any {
	switch m := v.(type) {
	case nil:
		return nil
	case bson.M:
		out := make(map[string]any, len(m))
		for k, vv := range m {
			out[k] = vv
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(m))
		for k, vv := range m {
			out[k] = vv
		}
		return out
	case bson.D:
		out := make(map[string]any, len(m))
		for _, e := range m {
			out[e.Key] = e.Value
		}
		return out
	}
	return nil
}

// NormalizeSlice — bson.A | []any → []any.
func NormalizeSlice(v any) []any {
	switch s := v.(type) {
	case nil:
		return nil
	case bson.A:
		out := make([]any, len(s))
		copy(out, s)
		return out
	case []any:
		out := make([]any, len(s))
		copy(out, s)
		return out
	}
	return nil
}

// ReadInt64Any — int32 | int64 | float64 | int | nil → int64.
func ReadInt64Any(v any) int64 {
	switch x := v.(type) {
	case int32:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case int:
		return int64(x)
	}
	return 0
}
