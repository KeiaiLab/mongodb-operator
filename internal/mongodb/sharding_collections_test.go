/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Track B — 선언적 컬렉션 샤딩 단위 + adversarial 테스트.
//
// shardKeyEqual/normalizeShardKeyValue 의 key-drift 비흡수 로직 + 신규 메서드의
// connect-error label 경로를 결정론적으로 검증한다(네트워크 0, server 무관).

package mongodb

import (
	"context"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestShardKeyEqual — compound key 순서 + 값(범위/hashed) 정규화 비교.
//
// adversarial 핵심: 이미 다른 키로 샤딩된 컬렉션을 동일하다고 오판하면(false
// positive) ShardKeyDrift 가 silent 흡수된다. 순서 차이 / 값 차이 / 개수 차이를
// 모두 불일치로 판정해야 한다.
func TestShardKeyEqual(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a    bson.D
		b    bson.D
		want bool
	}{
		{
			name: "동일 단일 hashed",
			a:    bson.D{{Key: "meta.symbol", Value: "hashed"}},
			b:    bson.D{{Key: "meta.symbol", Value: "hashed"}},
			want: true,
		},
		{
			name: "범위키 int32 vs float64 정규화 동등",
			a:    bson.D{{Key: "userId", Value: int32(1)}},
			b:    bson.D{{Key: "userId", Value: float64(1)}},
			want: true,
		},
		{
			name: "범위키 int32 vs int64 정규화 동등",
			a:    bson.D{{Key: "userId", Value: int32(-1)}},
			b:    bson.D{{Key: "userId", Value: int64(-1)}},
			want: true,
		},
		{
			name: "필드 순서 다름 → 불일치(compound order 의미)",
			a:    bson.D{{Key: "region", Value: int32(1)}, {Key: "userId", Value: int32(1)}},
			b:    bson.D{{Key: "userId", Value: int32(1)}, {Key: "region", Value: int32(1)}},
			want: false,
		},
		{
			name: "값 다름(1 vs hashed) → 불일치",
			a:    bson.D{{Key: "x", Value: int32(1)}},
			b:    bson.D{{Key: "x", Value: "hashed"}},
			want: false,
		},
		{
			name: "필드명 다름 → 불일치",
			a:    bson.D{{Key: "x", Value: int32(1)}},
			b:    bson.D{{Key: "y", Value: int32(1)}},
			want: false,
		},
		{
			name: "개수 다름 → 불일치",
			a:    bson.D{{Key: "x", Value: int32(1)}},
			b:    bson.D{{Key: "x", Value: int32(1)}, {Key: "y", Value: int32(1)}},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shardKeyEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("shardKeyEqual(%v,%v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestNormalizeShardKeyValue — 숫자 타입 정규화 + hashed 문자열.
func TestNormalizeShardKeyValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want string
	}{
		{int32(1), "1"},
		{int64(1), "1"},
		{int(1), "1"},
		{float64(1), "1"},
		{int32(-1), "-1"},
		{float64(-1), "-1"},
		{"hashed", "hashed"},
	}
	for _, tc := range cases {
		if got := normalizeShardKeyValue(tc.in); got != tc.want {
			t.Errorf("normalizeShardKeyValue(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestShardCollection_WithOptions_ConnectError — opts(unique/timeseries) 지정 시에도
// connect 실패가 shardCollection op label 로 wrap 되는지 검증. timeseries 서브
// 도큐먼트 구성 코드 경로가 컴파일/실행되는지 동시 확인.
func TestShardCollection_WithOptions_ConnectError(t *testing.T) {
	t.Parallel()
	mgr := NewShardManagerWithFactory(failingFactory("dial fail"))
	ctx := context.Background()

	opts := &ShardCollectionOptions{
		Unique: false,
		Timeseries: &TimeseriesOptions{
			TimeField:   "openTime",
			MetaField:   "meta",
			Granularity: "seconds",
		},
	}
	err := mgr.ShardCollection(ctx, "mongos-0", "ns", "u", "p", "crypto.binance",
		bson.D{{Key: "meta.symbol", Value: "hashed"}}, opts)
	if err == nil {
		t.Fatal("expected connect error")
	}
	if !strings.Contains(err.Error(), "shardCollection") {
		t.Errorf("error %q should contain shardCollection op label", err.Error())
	}
	if !strings.Contains(err.Error(), "dial fail") {
		t.Errorf("error %q should wrap connect error", err.Error())
	}
}

// TestEnsureShardedCollection_ConnectError — 연결 실패 시 명시 label.
func TestEnsureShardedCollection_ConnectError(t *testing.T) {
	t.Parallel()
	mgr := NewShardManagerWithFactory(failingFactory("conn refused"))
	err := mgr.EnsureShardedCollection(context.Background(), "mongos-0", "ns",
		"u", "p", "crypto", "binance", bson.D{{Key: "meta.symbol", Value: "hashed"}}, nil)
	if err == nil {
		t.Fatal("expected connect error")
	}
	if !strings.Contains(err.Error(), "connect for EnsureShardedCollection") {
		t.Errorf("missing expected label: %v", err)
	}
}

// TestListShardedNamespaces_ConnectError — 연결 실패 시 명시 label.
func TestListShardedNamespaces_ConnectError(t *testing.T) {
	t.Parallel()
	mgr := NewShardManagerWithFactory(failingFactory("nx"))
	_, err := mgr.ListShardedNamespaces(context.Background(), "mongos-0", "ns")
	if err == nil {
		t.Fatal("expected connect error")
	}
	if !strings.Contains(err.Error(), "connect for ListShardedNamespaces") {
		t.Errorf("missing expected label: %v", err)
	}
}
