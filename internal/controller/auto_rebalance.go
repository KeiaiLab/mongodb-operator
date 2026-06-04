/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// Package controller — auto_rebalance.go: Phase 5.3 auto-rebalance feedback loop.
//
// Shard 간 chunk 분포 불균형을 분석해 balancer ON/OFF 권고를 *순수 결정* 한다.
// 실 chunk 마이그레이션 트리거는 mongos balancer 책임 (advisory only — 본 함수는
// 결정만, 부수효과 없음). DecideShardScaling (auto_scaling.go) 의 sister.
package controller

import "math"

// ChunkDistribution describes per-shard chunk count for rebalance analysis.
type ChunkDistribution struct {
	ShardName string
	Chunks    int32
}

// DistributionStats summarises chunk distribution across shards.
// ImbalanceRatio = (max-min)/mean — 0 이면 완전 균형, 클수록 불균형.
type DistributionStats struct {
	MaxChunks      int32
	MinChunks      int32
	MeanChunks     float64
	StdDev         float64
	ImbalanceRatio float64
	ShardCount     int
}

// BalancerControlDecision is the advisory action for the balancer.
type BalancerControlDecision struct {
	Action     string // "TurnOn" | "TurnOff" | "Maintain"
	Reason     string
	Confidence string // "high" | "medium" | "low"
}

// AnalyzeChunkDistribution computes spread statistics across shards.
// 빈 입력 또는 단일 shard 는 불균형 0 (비교 대상 없음).
func AnalyzeChunkDistribution(dist []ChunkDistribution) DistributionStats {
	s := DistributionStats{ShardCount: len(dist)}
	if len(dist) == 0 {
		return s
	}
	s.MaxChunks, s.MinChunks = dist[0].Chunks, dist[0].Chunks
	var sum int64
	for _, d := range dist {
		if d.Chunks > s.MaxChunks {
			s.MaxChunks = d.Chunks
		}
		if d.Chunks < s.MinChunks {
			s.MinChunks = d.Chunks
		}
		sum += int64(d.Chunks)
	}
	s.MeanChunks = float64(sum) / float64(len(dist))

	if len(dist) < 2 || s.MeanChunks == 0 {
		return s // 단일 shard 또는 전부 0 → 불균형 0
	}
	var sq float64
	for _, d := range dist {
		diff := float64(d.Chunks) - s.MeanChunks
		sq += diff * diff
	}
	s.StdDev = math.Sqrt(sq / float64(len(dist)))
	s.ImbalanceRatio = float64(s.MaxChunks-s.MinChunks) / s.MeanChunks
	return s
}

// DetectImbalance reports whether the distribution exceeds the imbalance
// threshold (e.g. 0.3 = 30%). threshold<=0 falls back to 0.3.
func DetectImbalance(stats DistributionStats, threshold float64) bool {
	if threshold <= 0 {
		threshold = 0.3
	}
	if stats.ShardCount < 2 {
		return false
	}
	return stats.ImbalanceRatio > threshold
}

// DecideBalancerControl returns an advisory balancer action given the current
// distribution and the balancer's current state ("on"/"off"). Hysteresis avoids
// flapping: turn ON above onThreshold, turn OFF only well below (onThreshold/2).
// threshold<=0 falls back to 0.3.
func DecideBalancerControl(stats DistributionStats, currentState string, onThreshold float64) BalancerControlDecision {
	if onThreshold <= 0 {
		onThreshold = 0.3
	}
	offThreshold := onThreshold / 2

	if stats.ShardCount < 2 {
		return BalancerControlDecision{Action: "Maintain", Reason: "비교할 shard 부족", Confidence: "low"}
	}

	imbalanced := stats.ImbalanceRatio > onThreshold
	wellBalanced := stats.ImbalanceRatio < offThreshold
	on := currentState == "on"

	switch {
	case imbalanced && !on:
		return BalancerControlDecision{Action: "TurnOn", Confidence: "high",
			Reason: "불균형 비율이 임계 초과 — 재분배 위해 balancer 활성 권고"}
	case wellBalanced && on:
		return BalancerControlDecision{Action: "TurnOff", Confidence: "medium",
			Reason: "균형 회복 (hysteresis 하한 미만) — balancer 비활성 권고"}
	default:
		return BalancerControlDecision{Action: "Maintain", Confidence: "low",
			Reason: "현 상태 유지 (hysteresis 구간 또는 상태 일치)"}
	}
}
