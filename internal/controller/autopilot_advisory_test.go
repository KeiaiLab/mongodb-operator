/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0.
*/

// autopilot_advisory_test.go — buildAutoPilotActions 순수 단위 (envtest 불요).
// Auto Pilot A등급 advisory: Recommendations → 조치 계획 (DryRun surface-only).

package controller

import (
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestBuildAutoPilotActions_SurfacesDryRunPlans(t *testing.T) {
	recs := []mongodbv1alpha1.Recommendation{
		{Type: "MissingIndex", Severity: "warning", DB: "app", Collection: "users",
			Detail: "COLLSCAN 감지 — filter status 에 대한 인덱스 권장 ns app.users", AvgLatencyMs: 600},
		{Type: "SlowQueryPattern", Severity: "critical", DB: "app", Collection: "orders",
			Detail: "filter region — 5회 발생 ns app.orders", AvgLatencyMs: 2000},
		// UnusedIndex 는 auto-action 미생성 (drop 은 비가역 — advisory only).
		{Type: "UnusedIndex", Severity: "warning", Detail: `Index "x_1" on app.users has 0 accesses`},
	}
	autoIdx := &mongodbv1alpha1.AutoIndexSpec{Enabled: true, MinSeverity: "warning", DryRun: true}
	autoHint := &mongodbv1alpha1.AutoQueryHintSpec{Enabled: true, SlowQueryThresholdMs: 1000, DryRun: true}

	got := buildAutoPilotActions(recs, autoIdx, autoHint)

	var idx, hint int
	for _, a := range got {
		if !a.DryRun {
			t.Errorf("DryRun=true 기대 (surface-only), got %+v", a)
		}
		switch a.Type {
		case "MissingIndex":
			idx++
		case "SlowQueryHint":
			hint++
		default:
			t.Errorf("예상치 못한 Type %q", a.Type)
		}
	}
	if idx != 1 {
		t.Errorf("MissingIndex action 1건 기대 (UnusedIndex 제외), got %d: %+v", idx, got)
	}
	if hint != 1 {
		t.Errorf("SlowQueryHint action 1건 기대, got %d: %+v", hint, got)
	}
}

func TestBuildAutoPilotActions_NilSpecsReturnsNil(t *testing.T) {
	recs := []mongodbv1alpha1.Recommendation{{Type: "MissingIndex", Severity: "critical", Detail: "x"}}
	if got := buildAutoPilotActions(recs, nil, nil); got != nil {
		t.Errorf("nil spec → nil 기대 (비활성), got %+v", got)
	}
}

func TestBuildAutoPilotActions_DisabledReturnsNil(t *testing.T) {
	recs := []mongodbv1alpha1.Recommendation{{Type: "MissingIndex", Severity: "critical", Detail: "x"}}
	autoIdx := &mongodbv1alpha1.AutoIndexSpec{Enabled: false}
	autoHint := &mongodbv1alpha1.AutoQueryHintSpec{Enabled: false}
	if got := buildAutoPilotActions(recs, autoIdx, autoHint); got != nil {
		t.Errorf("disabled spec → nil 기대, got %+v", got)
	}
}

func TestBuildAutoPilotActions_SeverityGate(t *testing.T) {
	// MinSeverity=critical → warning MissingIndex 는 제외.
	recs := []mongodbv1alpha1.Recommendation{
		{Type: "MissingIndex", Severity: "warning", Detail: "ns app.low"},
		{Type: "MissingIndex", Severity: "critical", Detail: "ns app.high"},
	}
	autoIdx := &mongodbv1alpha1.AutoIndexSpec{Enabled: true, MinSeverity: "critical", DryRun: true}
	got := buildAutoPilotActions(recs, autoIdx, nil)
	if len(got) != 1 {
		t.Fatalf("critical 1건만 기대, got %d: %+v", len(got), got)
	}
	if got[0].Severity != "critical" {
		t.Errorf("critical severity 기대, got %q", got[0].Severity)
	}
}
