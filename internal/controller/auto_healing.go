/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// Package controller — auto_healing.go: Level V Auto Pilot 자가복구 (A5, B3-B5).
//
// 순수 함수 + 마킹 로직. 실제 RemoveMember/PVC patch 호출은 reconcile loop 책임.
package controller

import (
	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// Ensure mongodbv1alpha1 import is used.
var _ = mongodbv1alpha1.AutoHealingSpec{}

// LaggingMember describes a member that exceeded the lag threshold.
type LaggingMember struct {
	Name    string
	LagSecs int32
}

// MemberLag is the input describing a single member's observed replication lag.
type MemberLag struct {
	Name    string
	LagSecs int32
}

// DetectLaggingMembers returns members whose replication lag exceeds threshold.
// Input is a slice of MemberLag tuples (provided by caller from Prometheus metric
// MetricReplicationLagSeconds or rs.status() polling).
func DetectLaggingMembers(observations []MemberLag, spec *mongodbv1alpha1.AutoHealingSpec) []LaggingMember {
	if spec == nil || !spec.Enabled {
		return nil
	}
	threshold := spec.LagThresholdSeconds
	if threshold == 0 {
		threshold = 30
	}
	var out []LaggingMember
	for _, m := range observations {
		if m.LagSecs > threshold {
			out = append(out, LaggingMember(m))
		}
	}
	return out
}

// CrashLoopPod describes a pod that has exceeded the CrashLoopBackOff threshold.
type CrashLoopPod struct {
	Name         string
	RestartCount int32
}

// PVCExpansionPlan describes a planned PVC size increment.
type PVCExpansionPlan struct {
	PVCName       string
	CurrentSizeGi int64
	NewSizeGi     int64
	UsagePercent  int32
}

// PlanPVCExpansion computes new PVC size if usage exceeds threshold.
// Returns nil if expansion not needed or spec disabled.
func PlanPVCExpansion(currentGi int64, usagePercent int32, spec *mongodbv1alpha1.AutoHealingSpec) *PVCExpansionPlan {
	if spec == nil || !spec.Enabled {
		return nil
	}
	thresh := spec.PVCExpansionUsagePercent
	if thresh == 0 {
		thresh = 85
	}
	if usagePercent < thresh {
		return nil
	}
	increment := int64(spec.PVCExpansionIncrementGi)
	if increment == 0 {
		increment = 10
	}
	return &PVCExpansionPlan{
		CurrentSizeGi: currentGi,
		NewSizeGi:     currentGi + increment,
		UsagePercent:  usagePercent,
	}
}

// FilterCrashLoopPods returns pods exceeding the CrashLoopBackOff restart threshold.
func FilterCrashLoopPods(pods []CrashLoopPod, spec *mongodbv1alpha1.AutoHealingSpec) []CrashLoopPod {
	if spec == nil || !spec.Enabled {
		return nil
	}
	threshold := spec.PodCrashLoopThreshold
	if threshold == 0 {
		threshold = 5
	}
	var out []CrashLoopPod
	for _, p := range pods {
		if p.RestartCount >= threshold {
			out = append(out, p)
		}
	}
	return out
}
