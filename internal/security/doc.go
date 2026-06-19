/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Package security 는 mongodb-operator 의 보안 강화 v2 패키지.
//
// 본 패키지의 함수는 모두 *순수 함수* — k8s/mongo/driver 직접 호출 0
// (internal/topology 와 동일한 패턴). spec 을 입력받아 advisory Finding 또는
// 결정적 SecurityContext 를 반환하며, side-effect(Event/Condition/적용)는
// 호출자(controller)가 담당한다.
//
// 구현 범위 (security v2 tractable, #325 분해):
//   - NetworkPolicy peer 검증 (ValidateNetworkPolicyPeers) — 빌드 시 조용히
//     drop 되거나 과도하게 넓은 peer 를 surface
//   - Pod/Container SecurityContext 기본값 (Default*SecurityContext) — restricted
//     baseline(non-root 999) 단일 진실원
//   - Container SecurityContext override preflight (PreflightContainerSecurityContext)
//     — restricted baseline 약화 감지
//
// 보류 범위 (security v2 deferred, #325 — GOVERNANCE §self-repair 금지영역):
//   - TLS 인증서 수명 주기 / Secret 로테이션 (비가역 키 회전)
//   - SCRAM/X509/LDAP/OIDC 인증 정합성 (실 mongod+IdP 의존, unit 격리 곤란)
//
// See: #325
package security
