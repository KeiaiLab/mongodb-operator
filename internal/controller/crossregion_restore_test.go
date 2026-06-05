/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// crossregion_restore_test.go — Phase 5.2 SelectRestoreSource 회귀 가드.

package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ts(base time.Time, h int) *metav1.Time {
	t := metav1.NewTime(base.Add(time.Duration(h) * time.Hour))
	return &t
}

func TestSelectRestoreSource(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	target := ts(base, 10) // T+10h

	t.Run("nil target → 거부", func(t *testing.T) {
		got, reason := SelectRestoreSource([]BackupCandidate{{RegionName: "a"}}, nil)
		if got != nil || reason == "" {
			t.Errorf("nil target 거부 기대, got %v / %q", got, reason)
		}
	})
	t.Run("빈 후보 → 거부", func(t *testing.T) {
		got, reason := SelectRestoreSource(nil, target)
		if got != nil || reason == "" {
			t.Errorf("빈 후보 거부 기대, got %v / %q", got, reason)
		}
	})
	t.Run("단일 covering → 선택", func(t *testing.T) {
		c := BackupCandidate{RegionName: "a", BackupID: "b1", CompletedAt: ts(base, 8), OplogWindowEnd: ts(base, 12)}
		got, _ := SelectRestoreSource([]BackupCandidate{c}, target)
		if got == nil || got.BackupID != "b1" {
			t.Errorf("covering backup 선택 기대, got %v", got)
		}
	})
	t.Run("복수 covering → 최신 CompletedAt", func(t *testing.T) {
		old := BackupCandidate{RegionName: "a", BackupID: "old", CompletedAt: ts(base, 5), OplogWindowEnd: ts(base, 12)}
		fresh := BackupCandidate{RegionName: "b", BackupID: "fresh", CompletedAt: ts(base, 9), OplogWindowEnd: ts(base, 12)}
		got, _ := SelectRestoreSource([]BackupCandidate{old, fresh}, target)
		if got == nil || got.BackupID != "fresh" {
			t.Errorf("최신 CompletedAt 선택 기대, got %v", got)
		}
	})
	t.Run("동률 CompletedAt → RegionName 사전순", func(t *testing.T) {
		z := BackupCandidate{RegionName: "z", BackupID: "z1", CompletedAt: ts(base, 9), OplogWindowEnd: ts(base, 12)}
		a := BackupCandidate{RegionName: "a", BackupID: "a1", CompletedAt: ts(base, 9), OplogWindowEnd: ts(base, 12)}
		got, _ := SelectRestoreSource([]BackupCandidate{z, a}, target)
		if got == nil || got.RegionName != "a" {
			t.Errorf("동률 시 RegionName 사전순 기대, got %v", got)
		}
	})
	t.Run("모든 backup target 이후 생성 → 거부", func(t *testing.T) {
		c := BackupCandidate{RegionName: "a", CompletedAt: ts(base, 11), OplogWindowEnd: ts(base, 20)}
		got, reason := SelectRestoreSource([]BackupCandidate{c}, target)
		if got != nil || reason == "" {
			t.Errorf("target 이후 생성 거부 기대, got %v / %q", got, reason)
		}
	})
	t.Run("모든 oplog window 닫힘 → 거부", func(t *testing.T) {
		c := BackupCandidate{RegionName: "a", CompletedAt: ts(base, 2), OplogWindowEnd: ts(base, 8)}
		got, reason := SelectRestoreSource([]BackupCandidate{c}, target)
		if got != nil || reason == "" {
			t.Errorf("oplog window 닫힘 거부 기대, got %v / %q", got, reason)
		}
	})
	t.Run("불완전 메타 skip", func(t *testing.T) {
		incomplete := BackupCandidate{RegionName: "a", CompletedAt: nil, OplogWindowEnd: ts(base, 12)}
		got, reason := SelectRestoreSource([]BackupCandidate{incomplete}, target)
		if got != nil || reason == "" {
			t.Errorf("불완전 메타 skip → 거부 기대, got %v / %q", got, reason)
		}
	})
	t.Run("경계: CompletedAt == target == OplogWindowEnd → 선택", func(t *testing.T) {
		c := BackupCandidate{RegionName: "a", BackupID: "edge", CompletedAt: target, OplogWindowEnd: target}
		got, _ := SelectRestoreSource([]BackupCandidate{c}, target)
		if got == nil || got.BackupID != "edge" {
			t.Errorf("경계 동시각 선택 기대, got %v", got)
		}
	})
}

func TestWithinOplogRetention(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		target    time.Time
		retention int
		want      bool
	}{
		{"window 내", now.Add(-1 * time.Hour), 24, true},
		{"window 밖(오래됨)", now.Add(-25 * time.Hour), 24, false},
		{"미래", now.Add(1 * time.Hour), 24, false},
		{"retention 0", now.Add(-1 * time.Hour), 0, false},
		{"경계 정확히 window 시작", now.Add(-24 * time.Hour), 24, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := withinOplogRetention(tc.target, now, tc.retention); got != tc.want {
				t.Errorf("withinOplogRetention = %v, want %v", got, tc.want)
			}
		})
	}
}
