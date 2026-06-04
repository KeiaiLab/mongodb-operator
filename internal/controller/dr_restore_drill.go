/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// Package controller — dr_restore_drill.go: Phase 5.2 disaster recovery 고도화.
//
// PITR 복원 타겟 검증 + restore drill 결과 판정 *순수 함수*. cross-cluster /
// cross-region 실 복원은 후속 (본 increment 는 검증 로직만 — webhook/reconcile
// 이 호출할 결정 함수). RestoreSpec.PointInTime 이 oplog retention window 밖이면
// mongorestore 가 실패하므로 *사전 거부* 한다 (mongodbbackup_types.go 주석의
// "rejected by the validating webhook" 의 testable core).
package controller

import (
	"fmt"
	"time"
)

// ValidatePITRTarget checks that a point-in-time recovery target is restorable
// given the oplog retention window. target must be in the past (vs now) and not
// older than the retention window (now - retentionHours). retentionHours<=0 means
// no PITR window configured → any PITR target is rejected.
func ValidatePITRTarget(target, now time.Time, retentionHours int) error {
	if retentionHours <= 0 {
		return fmt.Errorf("PITR target 지정됐으나 oplog retention window 미설정 (OplogRetentionHours<=0)")
	}
	if target.After(now) {
		return fmt.Errorf("PITR target %s 가 미래 (now=%s) — 복원 불가", target.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	oldest := now.Add(-time.Duration(retentionHours) * time.Hour)
	if target.Before(oldest) {
		return fmt.Errorf("PITR target %s 가 oplog retention window 밖 (가장 오래된 복원 가능 시점 %s, retention %dh)",
			target.UTC().Format(time.RFC3339), oldest.UTC().Format(time.RFC3339), retentionHours)
	}
	return nil
}

// DrillVerdict is the outcome of a restore drill verification.
type DrillVerdict struct {
	Passed bool
	Reason string
}

// EvaluateRestoreDrill compares the restored collection document counts against
// the source snapshot counts. A drill passes only when every expected collection
// is present with a matching (or greater, to tolerate post-snapshot oplog replay)
// document count. Missing collections or short counts fail the drill.
func EvaluateRestoreDrill(expected, actual map[string]int64) DrillVerdict {
	if len(expected) == 0 {
		return DrillVerdict{Passed: false, Reason: "기대 collection 0건 — 검증할 스냅샷 메타데이터 없음"}
	}
	for coll, want := range expected {
		got, ok := actual[coll]
		if !ok {
			return DrillVerdict{Passed: false, Reason: fmt.Sprintf("복원본에 collection %q 누락", coll)}
		}
		if got < want {
			return DrillVerdict{Passed: false, Reason: fmt.Sprintf("collection %q 문서 수 부족 (기대 %d, 실제 %d)", coll, want, got)}
		}
	}
	return DrillVerdict{Passed: true, Reason: fmt.Sprintf("%d collection 전부 검증 통과", len(expected))}
}
