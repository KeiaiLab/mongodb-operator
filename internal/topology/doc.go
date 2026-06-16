/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// Package topology 는 mongodb-operator 의 sharded topology v2 결정 함수.
//
// 책임 범위:
//   - Zone-aware sharding 배치 계획 (PlanZonePlacement)
//   - Topology 변경 pre-flight 안전검증 (PreflightTopologyChange)
//   - Balancer 상태 해석 + advisory 제어 (PlanBalancerControl)
//   - Chunk migration throttle 계산 (ComputeMigrationThrottle)
//
// 계약: 본 패키지 함수는 모두 *순수 함수* — k8s/mongo/driver 직접 호출 0.
// 입력 구조체를 받아 계획(plan)/판정(verdict)을 반환하며, 실 admin command
// 실행은 controller layer 책임이다 (internal/insights 동일 계약). 파괴적
// op(removeShard/balancer stop 등)는 DryRun=true 기본 advisory plan 으로만
// 산출되고, 실 실행은 controller 가 명시 opt-in 일 때만 수행한다.
//
// ADR-0009 (docs/kb/adr/0009-shard-cfg-hpa-with-deliberate-guard.md) 후속
// 작업 "deliberate sharding API — spec.Shards.Count 변경에 deliberate +
// drain window" 라인 정합.
//
// See: #322
package topology
