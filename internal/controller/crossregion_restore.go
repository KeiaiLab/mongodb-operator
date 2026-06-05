/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// crossregion_restore.go — Phase 5.2 cross-region PITR: 복원 source 선택 *결정*.
//
// 여러 region 의 가용 backup 메타데이터에서 target 시점을 cover 하는 가장 신선한
// backup 을 고르는 *순수 함수*. 실 backup 메타 수집(remote cluster 조회)은 후속
// (cross-cluster I/O). ValidatePITRTarget / EvaluateRestoreDrill sister.
package controller

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BackupCandidate is one region's restorable backup metadata for source
// selection. A backup covers target T when CompletedAt <= T <= OplogWindowEnd
// (snapshot 시점부터 oplog 가 replay 가능한 마지막 시점까지).
type BackupCandidate struct {
	RegionName     string
	BackupID       string
	CompletedAt    *metav1.Time
	OplogWindowEnd *metav1.Time
}

// SelectRestoreSource picks the freshest backup covering target across regions.
// covering = CompletedAt <= target <= OplogWindowEnd. Among covering candidates
// the one with the latest CompletedAt (smallest replay distance) wins; ties break
// by RegionName for determinism. nil/timestamp-less inputs are skipped/rejected.
// Returns the selected candidate, or nil + a categorized reason when none cover.
func SelectRestoreSource(candidates []BackupCandidate, target *metav1.Time) (*BackupCandidate, string) {
	if target == nil {
		return nil, "복원 target timestamp 미지정"
	}
	if len(candidates) == 0 {
		return nil, "가용 backup 후보 없음"
	}
	t := target.Time

	var best *BackupCandidate
	var afterTarget, beforeWindow, valid int
	for i := range candidates {
		c := candidates[i]
		if c.CompletedAt == nil || c.OplogWindowEnd == nil {
			continue // 불완전 메타 — 신뢰 불가, skip
		}
		valid++
		switch {
		case c.CompletedAt.Time.After(t):
			afterTarget++ // 백업이 target 이후 생성 → 이 시점 복원 불가
		case c.OplogWindowEnd.Time.Before(t):
			beforeWindow++ // oplog window 가 target 전에 닫힘
		default:
			// covering: CompletedAt <= t <= OplogWindowEnd. 최신 CompletedAt 우선,
			// 동률 시 RegionName 사전순.
			if best == nil || c.CompletedAt.Time.After(best.CompletedAt.Time) ||
				(c.CompletedAt.Time.Equal(best.CompletedAt.Time) && c.RegionName < best.RegionName) {
				sel := c
				best = &sel
			}
		}
	}

	if best != nil {
		return best, ""
	}
	switch {
	case valid == 0:
		return nil, "후보의 backup 메타데이터(CompletedAt/OplogWindowEnd) 불완전"
	case afterTarget == valid:
		return nil, "모든 backup 이 target 이후 생성됨 — 해당 시점 복원 불가"
	case beforeWindow == valid:
		return nil, "모든 oplog window 가 target 전에 닫힘 — replay 불가"
	default:
		return nil, "target 이 모든 가용 backup window 밖"
	}
}

// withinOplogRetention reports whether target is inside [now-retention, now] —
// a convenience for callers gating cross-region restore by the source cluster's
// retention. Mirrors ValidatePITRTarget's window check without erroring.
func withinOplogRetention(target, now time.Time, retentionHours int) bool {
	if retentionHours <= 0 || target.After(now) {
		return false
	}
	return !target.Before(now.Add(-time.Duration(retentionHours) * time.Hour))
}
