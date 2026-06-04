/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// Package controller — auto_anomaly.go: Level V Auto Pilot 이상 감지 (B6, B7).
//
// Traffic spike, auth failure spike 감지. 순수 함수.
package controller

import (
	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// Severity 등급 상수 (goconst — "warning"/"critical" 다중 등장 통일).
const (
	severityWarning  = "warning"
	severityCritical = "critical"
)

// AnomalyAlert describes a detected anomaly.
type AnomalyAlert struct {
	Type     string // "TrafficSpike" | "AuthFailureSpike"
	Severity string // "warning" | "critical"
	Message  string
	Metric   string
	Observed float64
	Baseline float64
}

// DetectTrafficSpike compares current connection rate to baseline.
// Triggers TrafficSpike when current > baseline * multiplier.
func DetectTrafficSpike(currentConns, baselineConns float64, spec *mongodbv1alpha1.AnomalyDetectionSpec) *AnomalyAlert {
	if spec == nil || !spec.Enabled {
		return nil
	}
	mul := spec.ConnectionSpikeMultiplier
	if mul == 0 {
		mul = 3
	}
	if baselineConns <= 0 {
		return nil
	}
	threshold := baselineConns * float64(mul)
	if currentConns <= threshold {
		return nil
	}
	severity := severityWarning
	if currentConns > threshold*2 {
		severity = severityCritical
	}
	return &AnomalyAlert{
		Type:     "TrafficSpike",
		Severity: severity,
		Message:  "connection rate exceeded baseline multiplier",
		Metric:   "mongodb_connections_active",
		Observed: currentConns,
		Baseline: baselineConns,
	}
}

// DetectAuthFailureSpike triggers when auth failure rate exceeds threshold.
func DetectAuthFailureSpike(failuresPerMin float64, spec *mongodbv1alpha1.AnomalyDetectionSpec) *AnomalyAlert {
	if spec == nil || !spec.Enabled {
		return nil
	}
	thresh := spec.AuthFailureRatePerMin
	if thresh == 0 {
		thresh = 10
	}
	if failuresPerMin <= float64(thresh) {
		return nil
	}
	severity := severityWarning
	if failuresPerMin > float64(thresh)*5 {
		severity = severityCritical
	}
	return &AnomalyAlert{
		Type:     "AuthFailureSpike",
		Severity: severity,
		Message:  "authentication failures exceed threshold",
		Metric:   "mongodb_audit_events_total{atype=authenticate}",
		Observed: failuresPerMin,
		Baseline: float64(thresh),
	}
}
