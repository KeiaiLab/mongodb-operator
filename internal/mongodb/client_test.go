/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package mongodb

import (
	"context"
	"strings"
	"testing"
)

func TestBuildURI(t *testing.T) {
	cases := []struct {
		name       string
		hosts      []string
		replicaSet string
		direct     bool
		want       string
	}{
		{
			name:  "single host no options",
			hosts: []string{"mongo-0:27017"},
			want:  "mongodb://mongo-0:27017",
		},
		{
			name:       "replica set",
			hosts:      []string{"mongo-0:27017", "mongo-1:27017", "mongo-2:27017"},
			replicaSet: "rs0",
			want:       "mongodb://mongo-0:27017,mongo-1:27017,mongo-2:27017/?replicaSet=rs0",
		},
		{
			name:   "direct connection",
			hosts:  []string{"mongo-0:27017"},
			direct: true,
			want:   "mongodb://mongo-0:27017/?directConnection=true",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildURI(tc.hosts, tc.replicaSet, tc.direct)
			if got != tc.want {
				t.Fatalf("buildURI = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildURI_NeverContainsCredentials는 자격증명이 절대 URI 문자열에
// 포함되지 않음을 단정한다. URI 안 자격증명은 인코딩 누락 위험과 audit log
// 노출 위험이 있어 우리 헬퍼는 항상 SetAuth로 분리해야 한다.
func TestBuildURI_NeverContainsCredentials(t *testing.T) {
	uri := buildURI([]string{"mongo-0:27017", "mongo-1:27017"}, "rs0", false)
	for _, forbidden := range []string{"@", "password", "secret", "user:"} {
		if strings.Contains(uri, forbidden) {
			t.Fatalf("URI %q에 금지 토큰 %q 포함됨", uri, forbidden)
		}
	}
}

func TestNewClient_RejectsEmptyHosts(t *testing.T) {
	_, err := NewClient(context.Background(), ConnectOpts{})
	if err == nil {
		t.Fatal("Hosts 비어있을 때 에러를 기대했지만 nil")
	}
	if !strings.Contains(err.Error(), "Hosts 비어있음") {
		t.Fatalf("에러 메시지가 기대와 다름: %v", err)
	}
}

func TestNewClient_RejectsDirectAndReplicaSet(t *testing.T) {
	_, err := NewClient(context.Background(), ConnectOpts{
		Hosts:      []string{"mongo-0:27017"},
		Direct:     true,
		ReplicaSet: "rs0",
	})
	if err == nil {
		t.Fatal("Direct+ReplicaSet 동시 지정 시 에러를 기대했지만 nil")
	}
	if !strings.Contains(err.Error(), "동시 지정 불가") {
		t.Fatalf("에러 메시지가 기대와 다름: %v", err)
	}
}

// TestGetPodFQDN은 StatefulSet headless service 명명 규약(<pod>.<svc>.<ns>.svc.cluster.local:<port>)이
// 정확히 적용되는지 확인한다. driver 연결의 host 기반이라 형식이 깨지면 모든 reconcile이 실패.
func TestGetPodFQDN(t *testing.T) {
	got := GetPodFQDN("mongo-1", "mongo-svc", "mongodb-system", 27017)
	want := "mongo-1.mongo-svc.mongodb-system.svc.cluster.local:27017"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestGetPodsFQDN은 StatefulSet ordinal 패턴(0..N-1)으로 FQDN 슬라이스가 정확히
// 생성되는지 검증.
func TestGetPodsFQDN(t *testing.T) {
	got := GetPodsFQDN("rs", "rs-svc", "default", 3, 27017)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	expectations := []string{
		"rs-0.rs-svc.default.svc.cluster.local:27017",
		"rs-1.rs-svc.default.svc.cluster.local:27017",
		"rs-2.rs-svc.default.svc.cluster.local:27017",
	}
	for i, want := range expectations {
		if got[i] != want {
			t.Errorf("index %d: got %q, want %q", i, got[i], want)
		}
	}
}

// TestGetPodsFQDN_Zero는 replicas=0 입력에서 빈 슬라이스가 반환되는 경계 케이스를
// 보장. 갓 생성된 CR이 reconcile 중간 상태에서 호출될 수 있다.
func TestGetPodsFQDN_Zero(t *testing.T) {
	got := GetPodsFQDN("rs", "rs-svc", "default", 0, 27017)
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}
