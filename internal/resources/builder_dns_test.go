/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// builder_dns_test.go — mongod / cfg / shard / mongos pod 의 resolver 옵션 가드.
//
// 왜: UDP DNS 질의 하나가 유실되면 glibc 기본 timeout(5s)만큼 해석이 멈춘다 — 라이브
// 실측 2026-11-15 LAN 순단 때 dnsResolutionTimeMillis 5003. timeout 1 · attempts 3 이면
// 유실 1건의 비용이 ~1s 다.

package resources

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func assertDNSOptions(t *testing.T, label string, spec corev1.PodSpec) {
	t.Helper()
	if spec.DNSConfig == nil {
		t.Fatalf("%s: DNSConfig 가 없다", label)
	}

	got := map[string]string{}
	for _, o := range spec.DNSConfig.Options {
		if o.Value == nil {
			t.Fatalf("%s: 옵션 %s 에 값이 없다", label, o.Name)
		}
		got[o.Name] = *o.Value
	}

	want := map[string]string{"timeout": "1", "attempts": "3"}
	if len(got) != len(want) {
		t.Errorf("%s: 옵션 = %v, 기대 %v", label, got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s = %q, 기대 %q", label, k, got[k], v)
		}
	}

	if len(spec.DNSConfig.Nameservers) != 0 || len(spec.DNSConfig.Searches) != 0 {
		t.Errorf("%s: nameservers/searches 는 클러스터 기본을 따라야 한다: %+v", label, spec.DNSConfig)
	}
}

func TestPodDNSOptions(t *testing.T) {
	t.Parallel()

	sharded := newTestSharded()
	cases := map[string]corev1.PodSpec{
		"replicaset": BuildReplicaSetStatefulSet(newTestMongoDB()).Spec.Template.Spec,
		"cfg":        BuildConfigServerStatefulSet(sharded).Spec.Template.Spec,
		"shard":      BuildShardStatefulSet(sharded, 0).Spec.Template.Spec,
		"mongos":     BuildMongosDeployment(sharded).Spec.Template.Spec,
		"mongos-sts": BuildMongosStatefulSet(sharded).Spec.Template.Spec,
	}
	for label, spec := range cases {
		assertDNSOptions(t, label, spec)
	}
}
