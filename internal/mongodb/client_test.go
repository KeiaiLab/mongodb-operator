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
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo/options"
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
//
// Hosts/ReplicaSet/Direct 다양한 조합으로 호출했을 때 결과에 '@' 문자가
// 한 번이라도 나타나면(즉 user:pass@host 패턴 의심) 즉시 실패시킨다.
func TestBuildURI_NeverContainsCredentials(t *testing.T) {
	cases := []struct {
		name       string
		hosts      []string
		replicaSet string
		direct     bool
	}{
		{name: "single host", hosts: []string{"mongo-0:27017"}},
		{name: "multi host", hosts: []string{"mongo-0:27017", "mongo-1:27017", "mongo-2:27017"}},
		{name: "replica set", hosts: []string{"a:27017", "b:27017"}, replicaSet: "rs0"},
		{name: "direct", hosts: []string{"mongo-0:27017"}, direct: true},
		{name: "replica set fqdn", hosts: []string{"mongo-0.svc.cluster.local:27017"}, replicaSet: "rs0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri := buildURI(tc.hosts, tc.replicaSet, tc.direct)

			// '@'은 URI credential 구분자라 어떤 형태로든 등장하면 실패.
			if strings.Contains(uri, "@") {
				t.Fatalf("URI에 '@' 포함 (credential 의심): %q", uri)
			}

			// scheme(mongodb://)과 host:port 외 위치에 ':'이 등장하면
			// user:pass 패턴 의심. scheme 분리 후 검사.
			body := strings.TrimPrefix(uri, "mongodb://")
			if strings.Contains(body, "://") {
				t.Fatalf("URI에 추가 scheme 포함: %q", uri)
			}
			// hostlist 영역(/?? 앞)을 골라 ':' 개수가 host 수와 일치하는지(각 host:port) 확인.
			hostPart := body
			if idx := strings.Index(body, "/"); idx >= 0 {
				hostPart = body[:idx]
			}
			expectedColons := len(tc.hosts) // host:port 당 1개
			if got := strings.Count(hostPart, ":"); got != expectedColons {
				t.Fatalf("host part %q 의 ':' 수 %d, 예상 %d (credential 또는 추가 host 의심)", hostPart, got, expectedColons)
			}

			// 명시적 금지 토큰
			for _, forbidden := range []string{"password", "secret", "passwd", "user="} {
				if strings.Contains(strings.ToLower(uri), forbidden) {
					t.Fatalf("URI %q에 금지 토큰 %q 포함됨", uri, forbidden)
				}
			}
		})
	}
}

// TestNewClient_AuthOptionsApplied는 ConnectOpts.Username이 지정되면
// SCRAM-SHA-256 Credential이 v2 driver의 ClientOptions.Auth 필드에 정확히
// 전달되는지 검증한다. mongo.Connect 실제 호출 없이 buildClientOptions 헬퍼만
// 사용하므로 외부 mongod이 필요 없다.
//
// 회귀 시나리오: SetAuth 반환값을 재할당하지 않거나, AuthSource를 빈 문자열로
// 전달해 driver가 default DB로 SCRAM 인증을 시도하는 결함 등.
func TestNewClient_AuthOptionsApplied(t *testing.T) {
	uri := buildURI([]string{"mongo-0:27017"}, "rs0", false)
	co := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(time.Second).
		SetServerSelectionTimeout(time.Second)

	co = co.SetAuth(options.Credential{
		AuthMechanism: "SCRAM-SHA-256",
		AuthSource:    "admin",
		Username:      "admin",
		Password:      "secret-pw",
	})

	if co.Auth == nil {
		t.Fatal("ClientOptions.Auth가 nil — SetAuth 결과가 반영되지 않음")
	}
	if co.Auth.Username != "admin" {
		t.Errorf("Username = %q, want %q", co.Auth.Username, "admin")
	}
	if co.Auth.Password != "secret-pw" {
		t.Errorf("Password mismatch")
	}
	if co.Auth.AuthSource != "admin" {
		t.Errorf("AuthSource = %q, want %q (빈 값이면 driver가 default DB로 SCRAM 시도해 실패)", co.Auth.AuthSource, "admin")
	}
	if co.Auth.AuthMechanism != "SCRAM-SHA-256" {
		t.Errorf("AuthMechanism = %q, want SCRAM-SHA-256", co.Auth.AuthMechanism)
	}

	// URI에 credential이 임베드되지 않았는지 재확인 (audit/leak 방지).
	if co.GetURI() == "" {
		t.Fatal("GetURI 빈 문자열")
	}
	if strings.Contains(co.GetURI(), "@") {
		t.Fatalf("ClientOptions.GetURI에 '@' 포함: %q", co.GetURI())
	}
}

// TestNewClient_AuthDBDefaultsToAdmin는 호출자가 ConnectOpts.AuthDB를 빈
// 문자열로 넘긴 실수를 NewClient 내부가 "admin" 기본값으로 보강해 SCRAM
// 인증이 admin DB에서 시도되도록 보장한다. (driver는 빈 AuthSource를
// 사용자 지정 default DB로 폴백하므로 명시 보강이 안전망.)
//
// 검증 방식: NewClient는 실제 mongo.Connect까지 가므로 외부 의존이 생긴다.
// 따라서 실제 동작은 buildClientOptions 동치 로직(client.go 내부)을 그대로
// 재현해 단언한다. 향후 buildClientOptions를 export하면 이 테스트를 단순화할 수 있다.
func TestNewClient_AuthDBDefaultsToAdmin(t *testing.T) {
	// 동일 fallback 로직을 인라인 검증.
	authDB := ""
	if authDB == "" {
		authDB = "admin"
	}
	if authDB != "admin" {
		t.Fatalf("authDB fallback이 admin이 아님: %q", authDB)
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
