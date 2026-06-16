/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package topology

import "sort"

// PlanZonePlacement 는 현재 shard→zone 멤버십과 desired 를 비교해 zone 변경
// 계획을 산출한다. 순수 함수 — mongo 호출 0. 산출된 plan 은 항상 advisory
// (DryRun=true); 실 addShardToZone/removeShardFromZone 실행은 controller 가
// 명시 opt-in 일 때만 수행한다.
//
// desiredZones 는 shard 이름 → 원하는 zone 목록. shards 에 있으나 desiredZones
// 에 키가 없는 shard 는 건드리지 않는다(opinion 없음). desiredZones 에 있으나
// shards 에 없는 항목은 무시한다(unknown shard skip).
func PlanZonePlacement(shards []ShardZone, desiredZones map[string][]string) ZonePlacementPlan {
	if len(desiredZones) == 0 {
		return ZonePlacementPlan{DryRun: true}
	}
	var ops []ZoneOp
	for _, sh := range shards {
		desired, ok := desiredZones[sh.ShardName]
		if !ok {
			continue // desired spec 없음 → 건드리지 않음
		}
		cur := toSet(sh.Zones)
		des := toSet(desired)
		// desired 에 있으나 current 에 없음 → AddToZone.
		for z := range des {
			if _, in := cur[z]; !in {
				ops = append(ops, ZoneOp{
					Action:    ActionAddToZone,
					ShardName: sh.ShardName,
					Zone:      z,
					Reason:    "shard 가 desired zone 에 미소속",
				})
			}
		}
		// current 에 있으나 desired 에 없음 → RemoveFromZone.
		for z := range cur {
			if _, in := des[z]; !in {
				ops = append(ops, ZoneOp{
					Action:    ActionRemoveFromZone,
					ShardName: sh.ShardName,
					Zone:      z,
					Reason:    "shard 가 desired 에 없는 zone 에 소속",
				})
			}
		}
	}
	// map range 는 비결정적 — emit 후 정렬로 결정론 출력 보장
	// (table-test/fake-client 안정성, internal/insights analyzer 정렬 패턴 정합).
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].ShardName != ops[j].ShardName {
			return ops[i].ShardName < ops[j].ShardName
		}
		if ops[i].Action != ops[j].Action {
			return ops[i].Action < ops[j].Action
		}
		return ops[i].Zone < ops[j].Zone
	})
	return ZonePlacementPlan{Ops: ops, DryRun: true}
}

// toSet 은 문자열 슬라이스를 멤버십 집합으로 변환한다(중복 제거).
func toSet(xs []string) map[string]struct{} {
	m := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		m[x] = struct{}{}
	}
	return m
}
