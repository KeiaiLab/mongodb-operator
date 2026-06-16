/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package topology

import "fmt"

// PreflightTopologyChange 는 shard 제거(removeShard) 직전 안전검증을 수행하는
// 순수 함수다. 본 함수는 *판정만* 반환하며 reconcile 흐름을 직접 차단하지
// 않는다 — controller 가 Verdict 를 condition 으로 surface 하거나 명시
// opt-in 시 진입 차단에 사용한다.
//
// 규칙 근거 (ADR-0009 docs/kb/adr/0009 + MongoDB removeShard 시맨틱):
//   - 마지막 shard(현재 1개) 제거 → 0 shard 는 불가 → Blocker.
//   - balancer 비활성 + 잔여 chunk → chunk 가 drain 되지 않아 removeShard 가
//     무기한 차단(hang) → Blocker (ADR-0009 "removeShard 는 모든 chunk 이동
//     전까지 차단").
//   - 잔여 primary database → movePrimary 선행 없이는 removeShard 미완료 →
//     Blocker.
//   - DrainTimeoutSeconds 명시 0 → drain 대기 없이 즉시 실패 위험 → Warning.
//     (nil = 미지정 = 기본 30s, 안전 → 무경고.)
func PreflightTopologyChange(in PreflightInput) PreflightVerdict {
	var v PreflightVerdict
	if in.ShardCount == 1 {
		v.Blockers = append(v.Blockers,
			fmt.Sprintf("마지막 shard 제거 불가 (현재 shard 수=%d) — sharded cluster 는 최소 1 shard 필요", in.ShardCount))
	}
	if !in.BalancerEnabled && in.Remaining.Chunks > 0 {
		v.Blockers = append(v.Blockers,
			fmt.Sprintf("balancer 비활성 + 잔여 chunk=%d — chunk drain 불가로 removeShard 무기한 차단 (ADR-0009)", in.Remaining.Chunks))
	}
	if in.Remaining.Databases > 0 {
		v.Blockers = append(v.Blockers,
			fmt.Sprintf("잔여 primary database=%d — movePrimary 선행 필요 (removeShard 미완료)", in.Remaining.Databases))
	}
	if in.DrainTimeoutSeconds != nil && *in.DrainTimeoutSeconds == 0 {
		v.Warnings = append(v.Warnings,
			"DrainTimeoutSeconds=0 — drain 대기 없이 즉시 실패 처리 위험")
	}
	v.Safe = len(v.Blockers) == 0
	return v
}
