/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package topology

import "time"

// 액션 종류 상수 (goconst — 패키지 내 다중 등장).
const (
	ActionAddToZone      = "AddToZone"
	ActionRemoveFromZone = "RemoveFromZone"
	ActionUpdateKeyRange = "UpdateKeyRange"
	ActionEnable         = "Enable"
	ActionDisable        = "Disable"
	ActionNoop           = "Noop"
)

// Severity 상수 (info < warning < critical) — internal/insights 정합.
const (
	SevInfo     = "info"
	SevWarning  = "warning"
	SevCritical = "critical"
)

// ShardZone 는 한 shard 의 현재 zone 멤버십.
type ShardZone struct {
	ShardName string
	Zones     []string
}

// ZoneOp 는 계획된 단일 zone 변경 작업.
type ZoneOp struct {
	Action    string // ActionAddToZone | ActionRemoveFromZone | ActionUpdateKeyRange
	ShardName string
	Zone      string
	Reason    string
}

// ZonePlacementPlan 은 zone-aware placement 계획. DryRun 이 true 이면 advisory.
type ZonePlacementPlan struct {
	Ops    []ZoneOp
	DryRun bool
}

// ShardRemaining 은 removeShard 응답의 잔여 chunk/db 수
// (mongodb.RemoveShardResult.Remaining 형태 미러).
type ShardRemaining struct {
	Chunks    int
	Databases int
}

// PreflightInput 은 topology 변경(주로 removeShard) 전 안전검증 입력.
type PreflightInput struct {
	// ShardCount 는 변경 전 현재 shard 수.
	ShardCount int
	// TargetShard 는 제거 대상 shard 이름 (메시지용).
	TargetShard string
	// Remaining 은 대상 shard 의 잔여 chunk/db.
	Remaining ShardRemaining
	// BalancerEnabled 는 cluster balancer 활성 여부.
	BalancerEnabled bool
	// DrainTimeoutSeconds 는 ShardSpec.DrainTimeoutSeconds 와 동일 의미.
	// nil = 미지정(기본 30s, 안전). 명시 0 = 즉시 timeout(위험) → Warning.
	DrainTimeoutSeconds *int32
}

// PreflightVerdict 은 안전검증 결과. Safe = (len(Blockers) == 0).
type PreflightVerdict struct {
	Safe     bool
	Blockers []string
	Warnings []string
}

// BalancerState 는 호출자가 mongos 에서 관측한 balancer 상태.
type BalancerState struct {
	Enabled            bool
	Running            bool
	ActiveWindow       bool
	InflightMigrations int
}

// BalancerPolicy 는 원하는 balancer 정책. DryRun 기본 true 권장.
type BalancerPolicy struct {
	DesiredEnabled bool
	DryRun         bool
}

// BalancerPlan 은 balancer 제어 계획 (advisory).
type BalancerPlan struct {
	Action string // ActionEnable | ActionDisable | ActionNoop
	Reason string
	DryRun bool
}

// ThrottleInput 은 chunk migration throttle 계산 입력.
type ThrottleInput struct {
	RemainingChunks    int
	InflightMigrations int
	MongosCPUUtil      float64 // 0.0 ~ 1.0
	LastPollInterval   time.Duration
}

// ThrottlePlan 은 throttle 계산 결과.
type ThrottlePlan struct {
	PollInterval            time.Duration
	MaxConcurrentMigrations int
	Reason                  string
}
