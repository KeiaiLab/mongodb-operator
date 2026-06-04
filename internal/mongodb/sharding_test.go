/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package mongodb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// TestBuildShardConnectionString는 shard 연결 문자열 형식이
// "shardName/host1:port,host2:port,..." 임을 검증한다. 이 형식은 mongos의
// addShard 명령이 직접 받는 값이라 회귀 시 sharded 부트스트랩이 실패한다.
func TestBuildShardConnectionString(t *testing.T) {
	got := BuildShardConnectionString("shard0", "myrs-shard0", "myrs-shard0-svc", "default", 3, 27018)

	if !strings.HasPrefix(got, "shard0/") {
		t.Errorf("expected shard name prefix, got %q", got)
	}
	hosts := strings.TrimPrefix(got, "shard0/")
	parts := strings.Split(hosts, ",")
	if len(parts) != 3 {
		t.Fatalf("expected 3 host entries, got %d (%q)", len(parts), got)
	}
	for i, p := range parts {
		want := "myrs-shard0-" + string(rune('0'+i)) + ".myrs-shard0-svc.default.svc.cluster.local:27018"
		if p != want {
			t.Errorf("host[%d] = %q, want %q", i, p, want)
		}
	}
}

// TestBuildShardConnectionString_SingleMember는 단일 멤버 shard도 정상 처리되는지
// 확인. arbiter-only 또는 PSA 토폴로지 변형에서 발생할 수 있는 경우.
func TestBuildShardConnectionString_SingleMember(t *testing.T) {
	got := BuildShardConnectionString("shard1", "rs-shard1", "rs-shard1-svc", "ns", 1, 27017)
	want := "shard1/rs-shard1-0.rs-shard1-svc.ns.svc.cluster.local:27017"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestNewShardManager_ReturnsDeprecated는 옛 생성자가 거부되는지 검증.
func TestNewShardManager_ReturnsDeprecated(t *testing.T) {
	mgr, err := NewShardManager()
	if err == nil {
		t.Fatal("expected deprecation error, got nil")
	}
	if mgr != nil {
		t.Errorf("expected nil manager, got %p", mgr)
	}
	if !strings.Contains(err.Error(), "deprecated") {
		t.Errorf("error should mention deprecation: %v", err)
	}
}

// TestNewShardManagerWithFactory_NotNil은 권장 생성자가 매니저를 정상 반환.
func TestNewShardManagerWithFactory_NotNil(t *testing.T) {
	mgr := NewShardManagerWithFactory(nil)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
}

// failingFactory는 driver connect 단계에서 항상 에러를 반환하는 stub.
// 모든 ShardManager 메서드의 connect 실패 경로 검증용.
func failingFactory(errMsg string) ConnectFactory {
	return func(ctx context.Context, podName, namespace string, direct bool) (*mongo.Client, error) {
		return nil, errors.New(errMsg)
	}
}

// TestShardManager_ConnectErrorPaths는 AddShard/RemoveShard/ListShards/EnableSharding/
// ShardCollection 등 모든 wrapper가 connect 에러를 op 이름과 함께 wrap해 반환하는지
// 일괄 검증. 각 메서드의 op label이 깨지면 운영 로그에서 원인 추적이 어려워진다.
func TestShardManager_ConnectErrorPaths(t *testing.T) {
	mgr := NewShardManagerWithFactory(failingFactory("dial fail"))
	ctx := context.Background()

	cases := []struct {
		name   string
		invoke func() error
		wantOp string
	}{
		{"AddShard", func() error { return mgr.AddShard(ctx, "mongos-0", "ns", "shard0/host:27017") }, "addShard"},
		{"AddShardInContainer", func() error {
			return mgr.AddShardInContainer(ctx, "mongos-0", "ns", "mongos", "shard0/host:27017", 27017)
		}, "addShard"},
		{"AddShardWithAuth", func() error {
			return mgr.AddShardWithAuth(ctx, "mongos-0", "ns", "u", "p", "shard0/host:27017")
		}, "addShard"},
		{"AddShardWithAuthInContainer", func() error {
			return mgr.AddShardWithAuthInContainer(ctx, "mongos-0", "ns", "mongos", "u", "p", "shard0/host:27017", 27017)
		}, "addShard"},
		{"RemoveShard", func() error { return mgr.RemoveShard(ctx, "mongos-0", "ns", "u", "p", "shard0") }, "removeShard"},
		{"EnableSharding", func() error { return mgr.EnableSharding(ctx, "mongos-0", "ns", "u", "p", "mydb") }, "enableSharding"},
		{"ShardCollection", func() error {
			return mgr.ShardCollection(ctx, "mongos-0", "ns", "u", "p", "mydb.coll", bson.D{{Key: "_id", Value: "hashed"}})
		}, "shardCollection"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.invoke()
			if err == nil {
				t.Fatal("expected error from failing connect")
			}
			if !strings.Contains(err.Error(), tc.wantOp) {
				t.Errorf("error %q should contain op label %q", err.Error(), tc.wantOp)
			}
			if !strings.Contains(err.Error(), "dial fail") {
				t.Errorf("error %q should wrap underlying connect error", err.Error())
			}
		})
	}
}

// TestShardManager_ListShards_ConnectError는 ListShards가 connect 실패 시 명시
// label과 함께 에러를 반환하는지 검증.
func TestShardManager_ListShards_ConnectError(t *testing.T) {
	mgr := NewShardManagerWithFactory(failingFactory("conn refused"))
	_, err := mgr.ListShards(context.Background(), "mongos-0", "ns")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connect for ListShards") {
		t.Errorf("missing expected label: %v", err)
	}
}

// TestShardManager_IsShardAdded_ConnectError는 IsShardAdded가 ListShards를 통해
// connect 에러를 그대로 전파함을 보장.
func TestShardManager_IsShardAdded_ConnectError(t *testing.T) {
	mgr := NewShardManagerWithFactory(failingFactory("nx"))
	added, err := mgr.IsShardAdded(context.Background(), "mongos-0", "ns", "shard0")
	if err == nil {
		t.Fatal("expected error")
	}
	if added {
		t.Error("expected added=false on error")
	}
}
