/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Condition reason constants — postgres-operator/internal/controller/status.go:44-72
// reference 패턴 차용 (B-P0-2 cross-cut). 외부 사용자 (ArgoCD / Argo Events / 운영자
// kubectl jsonpath) 가 의존하는 *공개 contract* 으로, 변경 시 SemVer minor bump 의무.
//
// 명명 규약: PascalCase 동사구 / Conditioned 형식. metav1.Condition.Reason 필드.
package v1alpha1

// 표준 reconciler 결과 — Ready / Progressing 등 generic condition 의 reason.
const (
	// ReasonReconcileFailed — 마지막 reconcile 이 error 로 종료. helpers.go 의
	// updateClusterStatusOnError 가 사용. retry 후속.
	ReasonReconcileFailed = "ReconcileFailed"

	// ReasonReconcileSucceeded — 마지막 reconcile 이 성공. helpers.go 의
	// updateClusterStatusOnSuccess 가 사용.
	ReasonReconcileSucceeded = "ReconcileSucceeded"
)

// Reachability — primary 연결 상태 condition (PrimaryUnreachable type) 의 reason.
const (
	// ReasonConnectError — admin user 로 mongo client 연결 실패. mongo 서버
	// startup 진행 중 / network policy 잘못 / auth 미초기화.
	ReasonConnectError = "ConnectError"

	// ReasonReachable — admin client 가 ping 성공. 정상 운영.
	ReasonReachable = "Reachable"
)

// Scale 정책 — replicaset members 변경 안전 가드 (operational guard) 의 reason.
const (
	// ReasonScalePolicyDeliberateFalse — 사용자가 spec.scalePolicy.deliberate=true
	// 명시적 설정 *없이* members 를 변경. controller 가 *변경 거부* (1 시점 무중단성
	// 보장 — Phase II "Seamless Upgrades" 의 사용자 안전 baseline).
	ReasonScalePolicyDeliberateFalse = "ScalePolicyDeliberateFalse"
)
