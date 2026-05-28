/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0.
*/

// Package controller — auto_scaling.go: Level V Auto Pilot 자동 스케일링 (B1, B2).
//
// Shard chunk distribution + Oplog window 기반 scale decision.
// 실 sh.addShard()/rs.add() 호출은 reconcile loop 책임. 본 함수는 순수 결정.
package controller

import (
	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// ShardDistribution describes per-shard chunk count.
type ShardDistribution struct {
	ShardName string
	Chunks    int32
}

// ScaleDecision describes a planned scaling action.
type ScaleDecision struct {
	Reason     string
	Action     string // "AddShard" | "AddSecondary" | "Noop"
	Delta      int32  // positive count to add
	Confidence string // "high" | "medium" | "low"
}

// DecideShardScaling returns a scale decision based on chunk distribution imbalance.
// Triggers AddShard when max/min ratio exceeds 1.5x and chunks are not yet drained.
func DecideShardScaling(dist []ShardDistribution, spec *mongodbv1alpha1.AutoScalingSpec) ScaleDecision {
	if spec == nil || !spec.Enabled {
		return ScaleDecision{Action: "Noop", Reason: "auto-scaling disabled"}
	}
	if len(dist) < 2 {
		return ScaleDecision{Action: "Noop", Reason: "insufficient shards to compare"}
	}
	var minC, maxC = dist[0].Chunks, dist[0].Chunks
	for _, d := range dist {
		if d.Chunks < minC {
			minC = d.Chunks
		}
		if d.Chunks > maxC {
			maxC = d.Chunks
		}
	}
	if minC == 0 {
		return ScaleDecision{Action: "Noop", Reason: "empty shard present"}
	}
	ratio := float64(maxC) / float64(minC)
	if ratio > 1.5 {
		return ScaleDecision{
			Action:     "AddShard",
			Delta:      1,
			Reason:     "chunk imbalance ratio > 1.5",
			Confidence: "high",
		}
	}
	return ScaleDecision{Action: "Noop", Reason: "balanced"}
}

// DecideOplogWindowScaling triggers AddSecondary when oplog window falls below 1 hour.
func DecideOplogWindowScaling(currentWindowHours float64, spec *mongodbv1alpha1.AutoScalingSpec) ScaleDecision {
	if spec == nil || !spec.Enabled {
		return ScaleDecision{Action: "Noop", Reason: "auto-scaling disabled"}
	}
	if currentWindowHours >= 1.0 {
		return ScaleDecision{Action: "Noop", Reason: "oplog window healthy"}
	}
	confidence := "high"
	if currentWindowHours >= 0.5 {
		confidence = "medium"
	}
	return ScaleDecision{
		Action:     "AddSecondary",
		Delta:      1,
		Reason:     "oplog window below 1h threshold",
		Confidence: confidence,
	}
}
