/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package topology

import (
	"fmt"
	"time"
)

const (
	// throttleMinInterval / throttleMaxInterval 은 poll interval clamp 범위.
	throttleMinInterval = 30 * time.Second
	throttleMaxInterval = 5 * time.Minute
	// throttleHighCPUThreshold 초과 시 migration 을 1개로 제한하고 최대 backoff.
	throttleHighCPUThreshold = 0.80
	// throttleDefaultConcurrency 는 저부하 시 동시 migration 상한.
	throttleDefaultConcurrency = 2
)

// ComputeMigrationThrottle 은 chunk migration 진행 상태와 mongos 부하를 보고
// poll interval + 동시 migration 상한을 계산하는 순수 함수다. sleep 0 —
// time.Duration 산술만 수행하므로 deterministic clock 이 불필요하다
// (execution.md §1.4 sleep 동기화 금지 정합). scaleInPollInterval(controller)
// 의 elapsed backoff 를 일반화한다.
func ComputeMigrationThrottle(in ThrottleInput) ThrottlePlan {
	if in.RemainingChunks <= 0 {
		return ThrottlePlan{
			PollInterval:            0,
			MaxConcurrentMigrations: 0,
			Reason:                  "잔여 chunk 0 — migration 완료, throttle 불요",
		}
	}
	if in.MongosCPUUtil > throttleHighCPUThreshold {
		return ThrottlePlan{
			PollInterval:            throttleMaxInterval,
			MaxConcurrentMigrations: 1,
			Reason: fmt.Sprintf("mongos CPU=%.2f > %.2f — migration 1개 제한 + 최대 backoff",
				in.MongosCPUUtil, throttleHighCPUThreshold),
		}
	}
	// 저부하 + 잔여 chunk: LastPollInterval 기반 단조 backoff(2배) + 상·하한 clamp.
	next := in.LastPollInterval * 2
	if next < throttleMinInterval {
		next = throttleMinInterval
	}
	if next > throttleMaxInterval {
		next = throttleMaxInterval
	}
	return ThrottlePlan{
		PollInterval:            next,
		MaxConcurrentMigrations: throttleDefaultConcurrency,
		Reason:                  "저부하 — 표준 backoff",
	}
}
