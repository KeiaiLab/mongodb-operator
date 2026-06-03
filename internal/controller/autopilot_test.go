package controller

import (
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// ----- auto_healing -----

func TestDetectLaggingMembers_Disabled(t *testing.T) {
	obs := []MemberLag{{Name: "m0", LagSecs: 100}}
	if r := DetectLaggingMembers(obs, nil); r != nil {
		t.Fatalf("nil spec returns nil, got %v", r)
	}
	if r := DetectLaggingMembers(obs, &mongodbv1alpha1.AutoHealingSpec{Enabled: false}); r != nil {
		t.Fatalf("disabled spec returns nil, got %v", r)
	}
}

func TestDetectLaggingMembers_Threshold(t *testing.T) {
	obs := []MemberLag{
		{Name: "m0", LagSecs: 10},
		{Name: "m1", LagSecs: 31},
		{Name: "m2", LagSecs: 100},
	}
	r := DetectLaggingMembers(obs, &mongodbv1alpha1.AutoHealingSpec{Enabled: true, LagThresholdSeconds: 30})
	if len(r) != 2 {
		t.Fatalf("expected 2 lagging members (>30s), got %d", len(r))
	}
}

func TestPlanPVCExpansion_BelowThreshold(t *testing.T) {
	if p := PlanPVCExpansion(100, 50, &mongodbv1alpha1.AutoHealingSpec{Enabled: true, PVCExpansionUsagePercent: 85}); p != nil {
		t.Fatalf("usage below threshold returns nil, got %+v", p)
	}
}

func TestPlanPVCExpansion_ExpandsByIncrement(t *testing.T) {
	p := PlanPVCExpansion(100, 90, &mongodbv1alpha1.AutoHealingSpec{Enabled: true, PVCExpansionUsagePercent: 85, PVCExpansionIncrementGi: 20})
	if p == nil {
		t.Fatal("expected expansion plan")
	}
	if p.NewSizeGi != 120 {
		t.Errorf("expected 120Gi, got %d", p.NewSizeGi)
	}
}

func TestFilterCrashLoopPods(t *testing.T) {
	pods := []CrashLoopPod{{Name: "a", RestartCount: 2}, {Name: "b", RestartCount: 6}}
	r := FilterCrashLoopPods(pods, &mongodbv1alpha1.AutoHealingSpec{Enabled: true, PodCrashLoopThreshold: 5})
	if len(r) != 1 || r[0].Name != "b" {
		t.Fatalf("expected 1 pod (b), got %+v", r)
	}
}

// ----- auto_scaling -----

func TestDecideShardScaling_Balanced(t *testing.T) {
	dist := []ShardDistribution{
		{ShardName: "s0", Chunks: 100},
		{ShardName: "s1", Chunks: 110},
	}
	d := DecideShardScaling(dist, &mongodbv1alpha1.AutoScalingSpec{Enabled: true})
	if d.Action != "Noop" {
		t.Errorf("balanced shards should noop, got %s", d.Action)
	}
}

func TestDecideShardScaling_Imbalanced(t *testing.T) {
	dist := []ShardDistribution{
		{ShardName: "s0", Chunks: 50},
		{ShardName: "s1", Chunks: 200},
	}
	d := DecideShardScaling(dist, &mongodbv1alpha1.AutoScalingSpec{Enabled: true})
	if d.Action != "AddShard" {
		t.Errorf("imbalanced shards should AddShard, got %s", d.Action)
	}
	if d.Delta != 1 {
		t.Errorf("expected Delta=1, got %d", d.Delta)
	}
}

func TestDecideOplogWindowScaling_Healthy(t *testing.T) {
	d := DecideOplogWindowScaling(5.0, &mongodbv1alpha1.AutoScalingSpec{Enabled: true})
	if d.Action != "Noop" {
		t.Errorf("healthy oplog should noop, got %s", d.Action)
	}
}

func TestDecideOplogWindowScaling_LowWindow(t *testing.T) {
	d := DecideOplogWindowScaling(0.5, &mongodbv1alpha1.AutoScalingSpec{Enabled: true})
	if d.Action != "AddSecondary" {
		t.Errorf("low window should AddSecondary, got %s", d.Action)
	}
	if d.Confidence != "medium" {
		t.Errorf("expected medium confidence at 0.5h, got %s", d.Confidence)
	}
}

// ----- auto_anomaly -----

func TestDetectTrafficSpike_Disabled(t *testing.T) {
	if a := DetectTrafficSpike(1000, 100, nil); a != nil {
		t.Fatal("nil spec returns nil")
	}
}

func TestDetectTrafficSpike_BelowMultiplier(t *testing.T) {
	if a := DetectTrafficSpike(250, 100, &mongodbv1alpha1.AnomalyDetectionSpec{Enabled: true, ConnectionSpikeMultiplier: 3}); a != nil {
		t.Fatalf("250 < 100*3 should not alert, got %+v", a)
	}
}

func TestDetectTrafficSpike_AboveMultiplier(t *testing.T) {
	a := DetectTrafficSpike(500, 100, &mongodbv1alpha1.AnomalyDetectionSpec{Enabled: true, ConnectionSpikeMultiplier: 3})
	if a == nil {
		t.Fatal("500 > 100*3 should alert")
	}
	if a.Type != "TrafficSpike" {
		t.Errorf("expected TrafficSpike, got %s", a.Type)
	}
}

func TestDetectTrafficSpike_CriticalSeverity(t *testing.T) {
	a := DetectTrafficSpike(1000, 100, &mongodbv1alpha1.AnomalyDetectionSpec{Enabled: true, ConnectionSpikeMultiplier: 3})
	if a == nil || a.Severity != "critical" {
		t.Errorf("expected critical, got %v", a)
	}
}

func TestDetectAuthFailureSpike_BelowThreshold(t *testing.T) {
	if a := DetectAuthFailureSpike(5, &mongodbv1alpha1.AnomalyDetectionSpec{Enabled: true, AuthFailureRatePerMin: 10}); a != nil {
		t.Fatal("5/min < 10/min threshold should not alert")
	}
}

func TestDetectAuthFailureSpike_AboveThreshold(t *testing.T) {
	a := DetectAuthFailureSpike(15, &mongodbv1alpha1.AnomalyDetectionSpec{Enabled: true, AuthFailureRatePerMin: 10})
	if a == nil || a.Type != "AuthFailureSpike" {
		t.Errorf("expected AuthFailureSpike alert, got %v", a)
	}
}
