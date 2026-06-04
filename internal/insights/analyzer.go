/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Package insights — MongoDBInsights 실 분석 엔진 (ROADMAP §3.2 cycle 9 강화).
//
// 본 패키지는 mongo 의존성 없이 *순수 함수* 만 노출 — 호출자가 system.profile
// docs 를 ProfileDoc 슬라이스로 변환한 뒤 Analyze 로 Recommendation 을 산출.
// 외부 mongo 호출은 controller layer 의 책임.
package insights

import (
	"fmt"
	"sort"
	"strings"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// Severity constants.
const (
	SevWarning  = "warning"
	SevInfo     = "info"
	SevCritical = "critical"
)

// idIndexName — MongoDB 기본 `_id` 인덱스명 (UnusedIndex 분석서 제외 대상).
const idIndexName = "_id_"

// planSummaryCollscan — mongo profile docs 의 plan summary "COLLSCAN" 문자열.
// MissingIndex heuristic 의 1차 trigger (full collection scan).
const planSummaryCollscan = "COLLSCAN"

// ProfileDoc — db.system.profile 의 분석 대상 필드 subset.
// driver 의 bson.M 그대로가 아니라 *분석 entry point* 로 normalize.
type ProfileDoc struct {
	// Op = "query" | "command" | "update" | "remove" | "getmore"
	Op string

	// NS = "<db>.<collection>"
	NS string

	// Millis = 실행 latency.
	Millis int32

	// PlanSummary = "COLLSCAN" | "IXSCAN { key: 1 }" 등 mongo plan 요약.
	PlanSummary string

	// Filter = query filter (정확한 값은 무시하고 *키 집합* 만 분석에 사용).
	Filter map[string]any

	// Sort = sort spec (키 집합만 분석).
	Sort map[string]any

	// DocsExamined / NReturned — selectivity 분석.
	DocsExamined int64
	NReturned    int64

	// KeysExamined — index hit 여부 보조 지표.
	KeysExamined int64
}

// Analyze 는 profile docs 를 분석하여 Recommendation 슬라이스를 반환한다.
// docs 가 nil/빈 슬라이스면 빈 결과 반환 (실패 아님).
//
// slowThresholdMs 는 SlowQueryPattern 의 *그룹 평균 latency* 기준. CRD 의
// Spec.SlowQueryThresholdMs 를 그대로 전달.
func Analyze(docs []ProfileDoc, slowThresholdMs int32) []mongodbv1alpha1.Recommendation {
	if len(docs) == 0 {
		return nil
	}

	recs := make([]mongodbv1alpha1.Recommendation, 0, 8)
	recs = append(recs, detectMissingIndexes(docs)...)
	recs = append(recs, detectSlowQueryPatterns(docs, slowThresholdMs)...)
	recs = append(recs, detectSchemaHints(docs)...)
	return recs
}

// IndexStat represents a single index usage statistic from $indexStats.
type IndexStat struct {
	NS        string
	IndexName string
	Accesses  int64
}

// AnalyzeIndexUsage detects unused indexes (0 accesses) and returns recommendations.
// Skips _id_ index which is always maintained by MongoDB.
func AnalyzeIndexUsage(stats []IndexStat) []mongodbv1alpha1.Recommendation {
	var recs []mongodbv1alpha1.Recommendation
	for _, s := range stats {
		if s.IndexName == idIndexName {
			continue
		}
		if s.Accesses == 0 {
			recs = append(recs, mongodbv1alpha1.Recommendation{
				Type:     RecTypeUnusedIndex,
				Severity: SevWarning,
				Detail:   fmt.Sprintf("Index %q on %s has 0 accesses — consider dropping", s.IndexName, s.NS),
			})
		}
	}
	sort.Slice(recs, func(i, j int) bool {
		return recs[i].Detail < recs[j].Detail
	})
	return recs
}

// detectMissingIndexes — COLLSCAN 또는 examined/returned 비율이 높은 query.
//
// 동일 (ns, filterShape) 는 한 번만 emit — sample query 3건까지 첨부.
func detectMissingIndexes(docs []ProfileDoc) []mongodbv1alpha1.Recommendation {
	type bucket struct {
		ns           string
		filter       map[string]any
		sort         map[string]any
		samples      []string
		count        int
		totalMillis  int64
		maxRatio     float64
		collscanSeen bool
	}
	groups := make(map[string]*bucket)

	for _, d := range docs {
		if !looksLikeMissingIndex(d) {
			continue
		}
		key := d.NS + "|" + filterShape(d.Filter) + "|" + filterShape(d.Sort)
		b, ok := groups[key]
		if !ok {
			b = &bucket{ns: d.NS, filter: d.Filter, sort: d.Sort}
			groups[key] = b
		}
		b.count++
		b.totalMillis += int64(d.Millis)
		if d.PlanSummary == planSummaryCollscan {
			b.collscanSeen = true
		}
		if d.NReturned > 0 {
			r := float64(d.DocsExamined) / float64(d.NReturned)
			if r > b.maxRatio {
				b.maxRatio = r
			}
		}
		if len(b.samples) < 3 {
			b.samples = append(b.samples, summarizeQuery(d))
		}
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]mongodbv1alpha1.Recommendation, 0, len(groups))
	for _, k := range keys {
		b := groups[k]
		avg := int32(0)
		if b.count > 0 {
			avg = int32(b.totalMillis / int64(b.count))
		}
		db, coll := splitNS(b.ns)
		reason := "DocsExamined/NReturned 비율 과다"
		if b.collscanSeen {
			reason = "COLLSCAN 감지"
		}
		out = append(out, mongodbv1alpha1.Recommendation{
			Type:            RecTypeMissingIndex,
			Severity:        severityFromLatency(avg),
			DB:              db,
			Collection:      coll,
			Detail:          fmt.Sprintf("%s — filter %s 에 대한 인덱스 권장", reason, filterShape(b.filter)),
			IndexSuggestion: suggestIndex(b.filter, b.sort),
			AvgLatencyMs:    avg,
			QuerySamples:    b.samples,
		})
	}
	return out
}

// detectSlowQueryPatterns — 동일 (ns, filterShape) 그룹의 평균 latency 가
// threshold 를 넘고 발생이 3회 이상이면 emit.
//
// Codex review (RFC-0045) #3 fix: threshold 필터링을 *그룹화 이후* 로 이동.
// 이전 구현은 threshold 미만 doc 을 먼저 버려 mixed sample 에서 평균을 잘못
// 산정 (false negative). 본 fix 는 모든 doc 을 그룹화한 뒤 *그룹 평균* 으로
// 판정.
func detectSlowQueryPatterns(docs []ProfileDoc, thresholdMs int32) []mongodbv1alpha1.Recommendation {
	type bucket struct {
		ns      string
		filter  map[string]any
		samples []string
		count   int
		total   int64
	}
	groups := make(map[string]*bucket)
	for _, d := range docs {
		key := d.NS + "|" + filterShape(d.Filter)
		b, ok := groups[key]
		if !ok {
			b = &bucket{ns: d.NS, filter: d.Filter}
			groups[key] = b
		}
		b.count++
		b.total += int64(d.Millis)
		if len(b.samples) < 3 {
			b.samples = append(b.samples, summarizeQuery(d))
		}
	}

	keys := make([]string, 0, len(groups))
	for k, b := range groups {
		if b.count < 3 {
			continue
		}
		avg := b.total / int64(b.count)
		if avg < int64(thresholdMs) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]mongodbv1alpha1.Recommendation, 0, len(keys))
	for _, k := range keys {
		b := groups[k]
		avg := int32(b.total / int64(b.count))
		db, coll := splitNS(b.ns)
		out = append(out, mongodbv1alpha1.Recommendation{
			Type:         RecTypeSlowQueryPattern,
			Severity:     severityFromLatency(avg),
			DB:           db,
			Collection:   coll,
			Detail:       fmt.Sprintf("filter %s — %d회 발생, 평균 %dms (임계 %dms)", filterShape(b.filter), b.count, avg, thresholdMs),
			AvgLatencyMs: avg,
			QuerySamples: b.samples,
		})
	}
	return out
}

// detectSchemaHints — 단일 query 의 $or/$nor 절 5+ → schema 정규화 검토.
func detectSchemaHints(docs []ProfileDoc) []mongodbv1alpha1.Recommendation {
	seen := make(map[string]bool)
	var out []mongodbv1alpha1.Recommendation
	for _, d := range docs {
		clauses := countBoolClauses(d.Filter)
		if clauses < 5 {
			continue
		}
		if seen[d.NS] {
			continue
		}
		seen[d.NS] = true
		db, coll := splitNS(d.NS)
		out = append(out, mongodbv1alpha1.Recommendation{
			Type:       RecTypeSchemaHint,
			Severity:   SevInfo,
			DB:         db,
			Collection: coll,
			Detail:     fmt.Sprintf("%d개 $or/$nor 절 감지 — 복합 인덱스 또는 schema 정규화 검토", clauses),
		})
	}
	// 결과 정렬 — NS 기준. DB 와 Collection 사이에 NUL 구분자를 삽입해
	// 경계 모호로 인한 정렬 키 충돌을 방지한다 (예: DB="ab",Coll="c" 와
	// DB="a",Coll="bc" 가 단순 연결 시 동일 "abc" 로 충돌).
	sort.Slice(out, func(i, j int) bool {
		return out[i].DB+"\x00"+out[i].Collection < out[j].DB+"\x00"+out[j].Collection
	})
	return out
}

func looksLikeMissingIndex(d ProfileDoc) bool {
	if d.PlanSummary == planSummaryCollscan {
		return true
	}
	// Codex review (RFC-0045) #4 fix: NReturned==0 + 대량 scan 도 MissingIndex.
	// max(NReturned, 1) 을 분모로 사용 — 흔한 real slow query 패턴 cover.
	if d.DocsExamined > 1000 {
		denom := d.NReturned
		if denom < 1 {
			denom = 1
		}
		ratio := float64(d.DocsExamined) / float64(denom)
		if ratio > 100 {
			return true
		}
	}
	return false
}

// filterShape — filter 의 키 집합을 알파벳 정렬 join. *값* 은 무시.
// 예: {status: "active", createdAt: {$gt: T}} → "createdAt,status".
func filterShape(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// suggestIndex — equality 우선 → range → sort 키 순서로 compound 추천.
// 본 cycle 에서는 정확한 ESR (Equality/Sort/Range) 규칙 대신 *키 정렬 join*
// + sort 키 후행 배치 의 단순 휴리스틱. cycle 10 강화 예정.
func suggestIndex(filter, sortKeys map[string]any) string {
	if len(filter) == 0 && len(sortKeys) == 0 {
		return ""
	}
	fkeys := make([]string, 0, len(filter))
	for k := range filter {
		fkeys = append(fkeys, k)
	}
	sort.Strings(fkeys)
	skeys := make([]string, 0, len(sortKeys))
	for k := range sortKeys {
		if _, dup := filter[k]; dup {
			continue
		}
		skeys = append(skeys, k)
	}
	sort.Strings(skeys)

	parts := make([]string, 0, len(fkeys)+len(skeys))
	for _, k := range fkeys {
		parts = append(parts, fmt.Sprintf("%s:1", k))
	}
	for _, k := range skeys {
		parts = append(parts, fmt.Sprintf("%s:1", k))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func summarizeQuery(d ProfileDoc) string {
	return fmt.Sprintf("%s ns=%s filter=%s millis=%d examined=%d returned=%d",
		d.Op, d.NS, filterShape(d.Filter), d.Millis, d.DocsExamined, d.NReturned)
}

func splitNS(ns string) (string, string) {
	i := strings.Index(ns, ".")
	if i < 0 {
		return ns, ""
	}
	return ns[:i], ns[i+1:]
}

func severityFromLatency(avg int32) string {
	switch {
	case avg >= 1000:
		return SevCritical
	case avg >= 500:
		return SevWarning
	default:
		return SevInfo
	}
}

// countBoolClauses — top-level $or / $nor 배열 길이의 최댓값.
//
// Codex re-review (RFC-0045) #4 fix: NormalizeMap 이 nested bson.A 를 그대로
// 두므로 `[]any` 단일 type assertion 으로는 bson.A{...} 형태의 진본 $or 절을
// 누락. NormalizeSlice 로 bson.A | []any 동시 처리.
func countBoolClauses(m map[string]any) int {
	max := 0
	for _, k := range []string{"$or", "$nor"} {
		if v, ok := m[k]; ok {
			if arr := NormalizeSlice(v); len(arr) > max {
				max = len(arr)
			}
		}
	}
	return max
}
