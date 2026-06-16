/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package topology

import "testing"

func TestPlanBalancerControl_DefaultsToDryRun(t *testing.T) {
	plan := PlanBalancerControl(BalancerState{Enabled: true}, BalancerPolicy{DesiredEnabled: true, DryRun: true})
	if !plan.DryRun {
		t.Errorf("policy.DryRun=true 전파 기대")
	}
}

func TestPlanBalancerControl_DesiredOnWhenOffEmitsEnable(t *testing.T) {
	plan := PlanBalancerControl(BalancerState{Enabled: false}, BalancerPolicy{DesiredEnabled: true, DryRun: true})
	if plan.Action != ActionEnable {
		t.Fatalf("Enable 기대, got %s", plan.Action)
	}
}

func TestPlanBalancerControl_InflightMigrationsBlockDisable(t *testing.T) {
	plan := PlanBalancerControl(
		BalancerState{Enabled: true, InflightMigrations: 3},
		BalancerPolicy{DesiredEnabled: false, DryRun: true},
	)
	if plan.Action != ActionNoop {
		t.Fatalf("진행 중 migration 보호로 Noop 기대, got %s (%s)", plan.Action, plan.Reason)
	}
}

func TestPlanBalancerControl_DisableWhenNoInflight(t *testing.T) {
	plan := PlanBalancerControl(
		BalancerState{Enabled: true, InflightMigrations: 0},
		BalancerPolicy{DesiredEnabled: false, DryRun: true},
	)
	if plan.Action != ActionDisable {
		t.Fatalf("inflight 0 이면 Disable 기대, got %s", plan.Action)
	}
}

func TestPlanBalancerControl_AlreadyAlignedReturnsNoop(t *testing.T) {
	plan := PlanBalancerControl(BalancerState{Enabled: true}, BalancerPolicy{DesiredEnabled: true, DryRun: true})
	if plan.Action != ActionNoop {
		t.Fatalf("정합 시 Noop 기대, got %s", plan.Action)
	}
}

func TestPlanBalancerControl_ExplicitNonDryRunPropagates(t *testing.T) {
	plan := PlanBalancerControl(BalancerState{Enabled: false}, BalancerPolicy{DesiredEnabled: true, DryRun: false})
	if plan.DryRun {
		t.Errorf("policy.DryRun=false 전파 기대 (명시 opt-in)")
	}
	if plan.Action != ActionEnable {
		t.Errorf("Enable 기대, got %s", plan.Action)
	}
}
