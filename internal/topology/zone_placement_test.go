/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package topology

import "testing"

func TestPlanZonePlacement_EmptyDesiredReturnsNoop(t *testing.T) {
	plan := PlanZonePlacement([]ShardZone{{ShardName: "s0", Zones: []string{"z1"}}}, nil)
	if len(plan.Ops) != 0 {
		t.Fatalf("desired 비어있으면 op 0 기대, got %d", len(plan.Ops))
	}
	if !plan.DryRun {
		t.Errorf("plan 은 advisory(DryRun=true) 기대")
	}
}

func TestPlanZonePlacement_AlreadyAlignedReturnsNoop(t *testing.T) {
	shards := []ShardZone{{ShardName: "s0", Zones: []string{"z1"}}}
	desired := map[string][]string{"s0": {"z1"}}
	plan := PlanZonePlacement(shards, desired)
	if len(plan.Ops) != 0 {
		t.Fatalf("정합 상태면 op 0 기대, got %+v", plan.Ops)
	}
}

func TestPlanZonePlacement_UnassignedShardEmitsAddToZone(t *testing.T) {
	shards := []ShardZone{{ShardName: "s0", Zones: nil}}
	desired := map[string][]string{"s0": {"z1"}}
	plan := PlanZonePlacement(shards, desired)
	if len(plan.Ops) != 1 || plan.Ops[0].Action != ActionAddToZone || plan.Ops[0].Zone != "z1" {
		t.Fatalf("AddToZone z1 기대, got %+v", plan.Ops)
	}
}

func TestPlanZonePlacement_WrongMembershipEmitsRemove(t *testing.T) {
	shards := []ShardZone{{ShardName: "s0", Zones: []string{"z1", "z2"}}}
	desired := map[string][]string{"s0": {"z1"}}
	plan := PlanZonePlacement(shards, desired)
	if len(plan.Ops) != 1 || plan.Ops[0].Action != ActionRemoveFromZone || plan.Ops[0].Zone != "z2" {
		t.Fatalf("RemoveFromZone z2 기대, got %+v", plan.Ops)
	}
}

func TestPlanZonePlacement_UnknownShardSkipped(t *testing.T) {
	shards := []ShardZone{{ShardName: "s0", Zones: []string{"z1"}}}
	// s0 은 desired 에 키 없음 → 건드리지 않음. ghost 는 shards 에 없음 → 무시.
	desired := map[string][]string{"ghost": {"z9"}}
	plan := PlanZonePlacement(shards, desired)
	if len(plan.Ops) != 0 {
		t.Fatalf("desired 키 없는 shard 미변경 + shards 없는 ghost 무시 — op 0 기대, got %+v", plan.Ops)
	}
}

func TestPlanZonePlacement_DefaultsToDryRun(t *testing.T) {
	plan := PlanZonePlacement([]ShardZone{{ShardName: "s0"}}, map[string][]string{"s0": {"z1"}})
	if !plan.DryRun {
		t.Errorf("zone placement plan 은 항상 advisory(DryRun=true) 기대")
	}
}

func TestPlanZonePlacement_DeterministicOrder(t *testing.T) {
	shards := []ShardZone{
		{ShardName: "s1", Zones: []string{"zb", "za"}},
		{ShardName: "s0", Zones: nil},
	}
	desired := map[string][]string{"s0": {"z1"}, "s1": {}}
	p1 := PlanZonePlacement(shards, desired)
	p2 := PlanZonePlacement(shards, desired)
	if len(p1.Ops) != 3 {
		t.Fatalf("op 3개 기대(s0 add z1, s1 remove za, s1 remove zb), got %+v", p1.Ops)
	}
	for i := range p1.Ops {
		if p1.Ops[i] != p2.Ops[i] {
			t.Fatalf("op 순서 결정론적이어야 함: %+v vs %+v", p1.Ops, p2.Ops)
		}
	}
	if p1.Ops[0].ShardName != "s0" || p1.Ops[0].Action != ActionAddToZone {
		t.Errorf("정렬 시 s0 AddToZone 가 먼저: %+v", p1.Ops)
	}
}
