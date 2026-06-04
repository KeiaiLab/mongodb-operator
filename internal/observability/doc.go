/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Package observability 는 mongodb-operator 의 관측성 v2 기반 패키지.
//
// 책임 범위 (계획):
//   - 구조화된 메트릭 수집 (Prometheus counter/gauge/histogram)
//   - 분산 트레이싱 통합 (OpenTelemetry)
//   - 알림 규칙 자동 생성 (PrometheusRule CRD)
//   - 대시보드 프로비저닝 (Grafana ConfigMap)
//
// 본 패키지는 skeleton 단계이며, 구현은 후속 이슈에서 진행한다.
// See: #232
package observability
