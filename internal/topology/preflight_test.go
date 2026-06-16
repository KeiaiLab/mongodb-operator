/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package topology

import "testing"

func i32(v int32) *int32 { return &v }

func TestPreflightTopologyChange_LastShardRemovalBlocked(t *testing.T) {
	v := PreflightTopologyChange(PreflightInput{ShardCount: 1, TargetShard: "s0", BalancerEnabled: true})
	if v.Safe {
		t.Fatalf("마지막 shard 제거는 차단 기대, got Safe=true")
	}
	if len(v.Blockers) == 0 {
		t.Errorf("Blocker 기대")
	}
}

func TestPreflightTopologyChange_BalancerOffWithChunksBlocked(t *testing.T) {
	v := PreflightTopologyChange(PreflightInput{ShardCount: 3, BalancerEnabled: false, Remaining: ShardRemaining{Chunks: 10}})
	if v.Safe {
		t.Fatalf("balancer off + 잔여 chunk 는 차단 기대")
	}
}

func TestPreflightTopologyChange_RemainingDatabasesBlocked(t *testing.T) {
	v := PreflightTopologyChange(PreflightInput{ShardCount: 3, BalancerEnabled: true, Remaining: ShardRemaining{Databases: 2}})
	if v.Safe {
		t.Fatalf("잔여 primary database 는 차단 기대 (movePrimary 필요)")
	}
}

func TestPreflightTopologyChange_DrainTimeoutZeroWarns(t *testing.T) {
	v := PreflightTopologyChange(PreflightInput{ShardCount: 3, BalancerEnabled: true, DrainTimeoutSeconds: i32(0)})
	if !v.Safe {
		t.Fatalf("drain timeout 0 은 Warning 일 뿐 Safe 유지 기대")
	}
	if len(v.Warnings) == 0 {
		t.Errorf("Warning 기대")
	}
}

func TestPreflightTopologyChange_NilDrainTimeoutNoWarn(t *testing.T) {
	v := PreflightTopologyChange(PreflightInput{ShardCount: 3, BalancerEnabled: true, DrainTimeoutSeconds: nil})
	if len(v.Warnings) != 0 {
		t.Errorf("미지정(nil) drain timeout 은 안전 기본(30s) — 무경고 기대, got %+v", v.Warnings)
	}
}

func TestPreflightTopologyChange_CleanRemovalSafe(t *testing.T) {
	v := PreflightTopologyChange(PreflightInput{
		ShardCount: 3, BalancerEnabled: true,
		Remaining: ShardRemaining{Chunks: 0, Databases: 0}, DrainTimeoutSeconds: i32(60),
	})
	if !v.Safe {
		t.Fatalf("clean 제거(잔여 0, balancer on)는 Safe 기대, got %+v", v)
	}
}

func TestPreflightTopologyChange_EmptyInputSafe(t *testing.T) {
	v := PreflightTopologyChange(PreflightInput{})
	if !v.Safe {
		t.Fatalf("빈 입력(제거 컨텍스트 없음, ShardCount=0)은 Safe 기대, got %+v", v)
	}
}

func TestPreflightTopologyChange_MultipleBlockers(t *testing.T) {
	v := PreflightTopologyChange(PreflightInput{
		ShardCount: 1, BalancerEnabled: false,
		Remaining: ShardRemaining{Chunks: 5, Databases: 1},
	})
	if v.Safe {
		t.Fatalf("복수 위험 — 차단 기대")
	}
	if len(v.Blockers) < 3 {
		t.Errorf("3 Blocker 기대(마지막shard + balancer-off-chunks + remaining-db), got %d: %+v", len(v.Blockers), v.Blockers)
	}
}
