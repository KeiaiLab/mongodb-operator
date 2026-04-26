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
