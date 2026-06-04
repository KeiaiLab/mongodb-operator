/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Package dr 는 mongodb-operator 의 재해 복구(Disaster Recovery) v2 패키지.
//
// 책임 범위 (계획):
//   - Cross-region 백업 복제 오케스트레이션
//   - Point-in-time recovery 자동화
//   - Failover/Failback 워크플로우 관리
//   - DR 테스트 시뮬레이션 (dry-run)
//
// 본 패키지는 skeleton 단계이며, 구현은 후속 이슈에서 진행한다.
// See: #233
package dr
