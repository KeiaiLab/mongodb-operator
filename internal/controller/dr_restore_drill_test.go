/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// dr_restore_drill_test.go — Phase 5.2 PITR 타겟 검증 + restore drill 판정 회귀 가드.
// envtest 불요 (시각/카운트 입력 → 검증 결정 순수함수).

package controller

import (
	"testing"
	"time"
)

func TestValidatePITRTarget(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		target    time.Time
		retention int
		wantErr   bool
	}{
		{"window 내 (1h 전, retention 24h)", now.Add(-1 * time.Hour), 24, false},
		{"window 경계 직전 (23h 전, retention 24h)", now.Add(-23 * time.Hour), 24, false},
		{"미래 target 거부", now.Add(1 * time.Hour), 24, true},
		{"window 밖 (25h 전, retention 24h) 거부", now.Add(-25 * time.Hour), 24, true},
		{"retention 0 → 거부 (PITR window 미설정)", now.Add(-1 * time.Hour), 0, true},
		{"retention 음수 → 거부", now.Add(-1 * time.Hour), -5, true},
		{"now 정확히 (경계, 미래 아님)", now, 24, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePITRTarget(tc.target, now, tc.retention)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidatePITRTarget(target=%s, retention=%d) err=%v, wantErr=%v", tc.target, tc.retention, err, tc.wantErr)
			}
		})
	}
}

func TestEvaluateRestoreDrill(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		expected   map[string]int64
		actual     map[string]int64
		wantPassed bool
	}{
		{"정확히 일치 → pass", map[string]int64{"users": 100, "orders": 50}, map[string]int64{"users": 100, "orders": 50}, true},
		{"실제가 더 많음(oplog replay) → pass", map[string]int64{"users": 100}, map[string]int64{"users": 105}, true},
		{"collection 누락 → fail", map[string]int64{"users": 100, "orders": 50}, map[string]int64{"users": 100}, false},
		{"문서 수 부족 → fail", map[string]int64{"users": 100}, map[string]int64{"users": 90}, false},
		{"기대 0건 → fail (검증 불가)", map[string]int64{}, map[string]int64{"users": 100}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := EvaluateRestoreDrill(tc.expected, tc.actual)
			if v.Passed != tc.wantPassed {
				t.Errorf("EvaluateRestoreDrill = %v (%s), want passed=%v", v.Passed, v.Reason, tc.wantPassed)
			}
		})
	}
}
