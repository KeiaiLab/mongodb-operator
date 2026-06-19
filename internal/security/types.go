/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package security

// Severity 는 Finding 의 심각도. Error 는 사용자 의도가 조용히 사라지거나
// restricted baseline 이 깨지는 경우, Warning 은 권고 위반(동작은 유지).
type Severity string

const (
	SeverityWarning Severity = "Warning"
	SeverityError   Severity = "Error"
)

// Finding 은 순수 검증 결과 한 건(side-effect 0, advisory).
// internal/topology.PreflightVerdict 의 reason 패턴과 동일한 역할 — 호출자
// (controller)가 Event/Condition/log 로 surface 한다.
type Finding struct {
	// Field 는 Finding 을 유발한 spec 경로
	// (예: spec.networkPolicy.additionalIngressFrom[0]).
	Field string
	// Reason 은 사람이 읽을 설명.
	Reason string
	// Severity 는 심각도.
	Severity Severity
}
