/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Reconcile requeue cadence — *purpose-driven* 분기 (single-const 강제 X).
//
// CNPG / OT-Redis 등 mature operator OSS 가 채택한 패턴: cadence 는 *재활성화
// 의도* 에 따라 분류. 본 SSoT (B-P0-5) 는 cross-repo 정합 (valkey 동일 const
// 보유, postgres 는 statusPollInterval=5s 단일 — alpha 단계).
package controller

import "time"

const (
	// requeueSteady — 정상 운영 중 status drift 감지 polling.
	// CR spec 변경은 watch 가 즉시 trigger 하므로 본 cadence 는 *external
	// drift* (manual kubectl edit / Pod restart 등) 회수 용도.
	requeueSteady = 30 * time.Second

	// requeueProvisioning — 초기화 단계 polling (Pod startup / Secret 대기).
	// Pod ready 까지 평균 5-30s, 매 reconcile cycle 마다 status update 위해
	// 빈번하게 polling.
	requeueProvisioning = 10 * time.Second

	// requeueWaitForExternal — external resource (Secret / sibling CR) 의
	// 짧은 race-window 대기. 5s 가 일반적인 K8s controller 의 *fast retry*
	// baseline.
	requeueWaitForExternal = 5 * time.Second
)
