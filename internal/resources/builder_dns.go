/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package resources

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

// resolver 옵션 (RS / cfg / shard / mongos 공용 SSOT).
//
// glibc 기본은 timeout 5s · attempts 2 라 UDP 질의 하나가 유실되면 해석이 5s 멈춘다 —
// 2026-11-15 LAN 순단 실측 dnsResolutionTimeMillis 5003. timeout 1 · attempts 3 이면
// 유실 1건 비용 ~1s, 전부 유실돼도 3s 안에 실패한다. nameservers · searches 는
// dnsPolicy ClusterFirst 가 만든 값을 그대로 둔다(options 만 덧붙는다).
const (
	dnsOptionTimeout  = "timeout"
	dnsOptionAttempts = "attempts"
	dnsTimeoutSec     = 1
	dnsAttempts       = 3
)

// buildPodDNSConfig 는 DB pod 의 resolver 옵션을 만든다.
func buildPodDNSConfig() *corev1.PodDNSConfig {
	timeout := strconv.Itoa(dnsTimeoutSec)
	attempts := strconv.Itoa(dnsAttempts)

	return &corev1.PodDNSConfig{
		Options: []corev1.PodDNSConfigOption{
			{Name: dnsOptionTimeout, Value: &timeout},
			{Name: dnsOptionAttempts, Value: &attempts},
		},
	}
}
