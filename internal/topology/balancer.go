/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package topology

import "fmt"

// PlanBalancerControl 은 관측된 balancer 상태와 원하는 정책을 비교해 제어
// 계획을 산출하는 순수 함수다. 신규 I/O 0 — 호출자가 mongos 에서 BalancerState
// 를 fetch 해 전달하고, 실 startBalancer/stopBalancer 실행은 controller 가
// plan.DryRun=false (명시 opt-in) 일 때만 수행한다.
//
// 안전 규칙: desired=disabled 라도 진행 중 migration 이 있으면 balancer 를
// 멈추지 않는다(Noop) — 진행 중 chunk migration 중단 방지.
func PlanBalancerControl(state BalancerState, policy BalancerPolicy) BalancerPlan {
	plan := BalancerPlan{DryRun: policy.DryRun}
	switch {
	case policy.DesiredEnabled && !state.Enabled:
		plan.Action = ActionEnable
		plan.Reason = "desired=enabled 이나 balancer 비활성 — 활성화 필요"
	case !policy.DesiredEnabled && state.InflightMigrations > 0:
		plan.Action = ActionNoop
		plan.Reason = fmt.Sprintf("desired=disabled 이나 진행 중 migration=%d — 중단 방지로 보류", state.InflightMigrations)
	case !policy.DesiredEnabled && state.Enabled:
		plan.Action = ActionDisable
		plan.Reason = "desired=disabled 이고 진행 중 migration 없음 — 비활성화"
	default:
		plan.Action = ActionNoop
		plan.Reason = "balancer 상태가 desired 와 정합"
	}
	return plan
}
