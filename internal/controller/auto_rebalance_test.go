/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// auto_rebalance_test.go — Phase 5.3 auto-rebalance 순수 결정 함수 회귀 가드.
// envtest 불요 (입력 분포 → 통계/판정/권고 결정함수).

package controller

import (
	"math"
	"testing"
)

func cd(name string, chunks int32) ChunkDistribution {
	return ChunkDistribution{ShardName: name, Chunks: chunks}
}

func TestAnalyzeChunkDistribution(t *testing.T) {
	t.Parallel()
	t.Run("빈 입력 → 0", func(t *testing.T) {
		s := AnalyzeChunkDistribution(nil)
		if s.ShardCount != 0 || s.ImbalanceRatio != 0 {
			t.Errorf("빈 입력 통계 0 기대, got %+v", s)
		}
	})
	t.Run("단일 shard → 불균형 0", func(t *testing.T) {
		s := AnalyzeChunkDistribution([]ChunkDistribution{cd("s0", 100)})
		if s.ImbalanceRatio != 0 || s.MaxChunks != 100 {
			t.Errorf("단일 shard 불균형 0 기대, got %+v", s)
		}
	})
	t.Run("균형 분포 → ratio 0", func(t *testing.T) {
		s := AnalyzeChunkDistribution([]ChunkDistribution{cd("s0", 100), cd("s1", 100), cd("s2", 100)})
		if s.ImbalanceRatio != 0 || s.MeanChunks != 100 {
			t.Errorf("균형 분포 ratio 0 기대, got %+v", s)
		}
	})
	t.Run("불균형 분포 → ratio = (max-min)/mean", func(t *testing.T) {
		// 60,100,140 → mean=100, (140-60)/100 = 0.8
		s := AnalyzeChunkDistribution([]ChunkDistribution{cd("s0", 60), cd("s1", 100), cd("s2", 140)})
		if math.Abs(s.ImbalanceRatio-0.8) > 1e-9 {
			t.Errorf("ratio 0.8 기대, got %v (%+v)", s.ImbalanceRatio, s)
		}
		if s.MaxChunks != 140 || s.MinChunks != 60 {
			t.Errorf("max140/min60 기대, got %+v", s)
		}
	})
	t.Run("전부 0 → ratio 0 (0 나눗셈 방지)", func(t *testing.T) {
		s := AnalyzeChunkDistribution([]ChunkDistribution{cd("s0", 0), cd("s1", 0)})
		if s.ImbalanceRatio != 0 {
			t.Errorf("전부 0 시 ratio 0 기대, got %v", s.ImbalanceRatio)
		}
	})
}

func TestDetectImbalance(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		ratio     float64
		shards    int
		threshold float64
		want      bool
	}{
		{"임계 초과 → true", 0.5, 3, 0.3, true},
		{"임계 미만 → false", 0.2, 3, 0.3, false},
		{"임계 정확히 = → false (초과만)", 0.3, 3, 0.3, false},
		{"단일 shard → false", 0.9, 1, 0.3, false},
		{"threshold 0 → 기본 0.3 적용 (0.4>0.3 true)", 0.4, 3, 0, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := DistributionStats{ImbalanceRatio: tc.ratio, ShardCount: tc.shards}
			if got := DetectImbalance(s, tc.threshold); got != tc.want {
				t.Errorf("DetectImbalance(ratio=%v,shards=%d,th=%v) = %v, want %v", tc.ratio, tc.shards, tc.threshold, got, tc.want)
			}
		})
	}
}

func TestDecideBalancerControl(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		ratio      float64
		shards     int
		state      string
		threshold  float64
		wantAction string
	}{
		{"불균형 + off → TurnOn", 0.5, 3, "off", 0.3, "TurnOn"},
		{"불균형 + 이미 on → Maintain", 0.5, 3, "on", 0.3, "Maintain"},
		{"균형(하한 미만) + on → TurnOff", 0.1, 3, "on", 0.3, "TurnOff"},
		{"균형 + 이미 off → Maintain", 0.1, 3, "off", 0.3, "Maintain"},
		{"hysteresis 구간(0.15~0.3) + off → Maintain", 0.2, 3, "off", 0.3, "Maintain"},
		{"hysteresis 구간 + on → Maintain (flap 방지)", 0.2, 3, "on", 0.3, "Maintain"},
		{"단일 shard → Maintain", 0.9, 1, "off", 0.3, "Maintain"},
		{"threshold 0 → 기본 0.3 (0.5>0.3 off → TurnOn)", 0.5, 3, "off", 0, "TurnOn"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := DistributionStats{ImbalanceRatio: tc.ratio, ShardCount: tc.shards}
			got := DecideBalancerControl(s, tc.state, tc.threshold)
			if got.Action != tc.wantAction {
				t.Errorf("DecideBalancerControl(ratio=%v,state=%q,th=%v) = %q, want %q (%s)",
					tc.ratio, tc.state, tc.threshold, got.Action, tc.wantAction, got.Reason)
			}
		})
	}
}
