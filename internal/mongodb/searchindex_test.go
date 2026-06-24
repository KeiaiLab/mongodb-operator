/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// searchindex_test.go — ClassifyMongotStatus 순수 함수 테이블 테스트(실 mongo 불요).
// mongot index status → CRD Phase 매핑이 결정론적임을 가드(replicaset.go classifyHelloForRSInit 패턴).
package mongodb

import "testing"

func TestClassifyMongotStatus(t *testing.T) {
	cases := []struct {
		name      string
		status    string
		queryable bool
		want      SearchIndexPhase
	}{
		{"READY+queryable → Ready", "READY", true, SearchIndexPhaseReady},
		{"READY 미queryable → Building", "READY", false, SearchIndexPhaseBuilding},
		{"PENDING → Building", "PENDING", false, SearchIndexPhaseBuilding},
		{"BUILDING → Building", "BUILDING", false, SearchIndexPhaseBuilding},
		{"STALE → Building", "STALE", true, SearchIndexPhaseBuilding},
		{"FAILED → Failed", "FAILED", false, SearchIndexPhaseFailed},
		{"DOES_NOT_EXIST → Pending", "DOES_NOT_EXIST", false, SearchIndexPhasePending},
		{"미지 status → Pending", "WEIRD", false, SearchIndexPhasePending},
		{"소문자 ready → Ready(대소문자 무관)", "ready", true, SearchIndexPhaseReady},
		{"빈 status → Pending", "", false, SearchIndexPhasePending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyMongotStatus(tc.status, tc.queryable); got != tc.want {
				t.Errorf("ClassifyMongotStatus(%q,%v)=%q want %q", tc.status, tc.queryable, got, tc.want)
			}
		})
	}
}

// TestIsIndexNotFoundErr — drop 멱등 처리: nil / 메시지 fallback.
func TestIsIndexNotFoundErr(t *testing.T) {
	if isIndexNotFoundErr(nil) {
		t.Error("nil 은 not-found 아님")
	}
	for _, msg := range []string{"index not found", "ns not found for collection", "search index does not exist"} {
		if !isIndexNotFoundErr(errString(msg)) {
			t.Errorf("%q 는 not-found 로 분류돼야(멱등 drop)", msg)
		}
	}
	if isIndexNotFoundErr(errString("connection refused")) {
		t.Error("connection refused 는 not-found 아님(진짜 에러)")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
