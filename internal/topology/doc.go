/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Package topology 는 mongodb-operator 의 sharded topology v2 패키지.
//
// 책임 범위 (계획):
//   - Shard 추가/제거 오케스트레이션 (chunk migration 관리)
//   - Zone-aware sharding 설정 자동화
//   - Balancer 상태 모니터링 및 제어
//   - Topology 변경 안전성 검증 (pre-flight check)
//
// 본 패키지는 skeleton 단계이며, 구현은 후속 이슈에서 진행한다.
// See: #234
package topology
