/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package security 는 mongodb-operator 의 보안 강화 v2 패키지.
//
// 책임 범위 (계획):
//   - TLS 인증서 수명 주기 관리 (cert-manager 통합 강화)
//   - SCRAM/X509/LDAP/OIDC 인증 정합성 검증
//   - NetworkPolicy 자동 생성 및 갱신
//   - Pod SecurityContext 강제 (non-root, read-only rootfs)
//   - Secret 로테이션 오케스트레이션
//
// 본 패키지는 skeleton 단계이며, 구현은 후속 이슈에서 진행한다.
// See: #235
package security
