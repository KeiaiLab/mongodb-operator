/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Track B — 선언적 컬렉션 샤딩 controller pure-function 단위 테스트.
//
// buildShardKey(order→bson 값 매핑) + buildShardCollectionOptions(unique/timeseries
// 매핑) + isNamespaceNotFound/isConnectError(비치명 분류) 를 결정론적으로 검증한다.
// envtest 무관, 순수 함수.

package controller

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// TestBuildShardKey — order "1"/"-1"/"hashed" 가 bson.D 값으로 올바르게 매핑되고
// 순서가 보존되는지(compound key) 검증.
func TestBuildShardKey(t *testing.T) {
	t.Parallel()
	fields := []mongodbv1alpha1.ShardKeyField{
		{Field: "region", Order: "1"},
		{Field: "score", Order: "-1"},
		{Field: "meta.symbol", Order: "hashed"},
	}
	got := buildShardKey(fields)
	if len(got) != 3 {
		t.Fatalf("expected 3 key fields, got %d", len(got))
	}
	if got[0].Key != "region" || got[0].Value != int32(1) {
		t.Errorf("field[0] = %v, want region:1", got[0])
	}
	if got[1].Key != "score" || got[1].Value != int32(-1) {
		t.Errorf("field[1] = %v, want score:-1", got[1])
	}
	if got[2].Key != "meta.symbol" || got[2].Value != "hashed" {
		t.Errorf("field[2] = %v, want meta.symbol:hashed", got[2])
	}
}

// TestBuildShardCollectionOptions — unique/timeseries 매핑.
func TestBuildShardCollectionOptions(t *testing.T) {
	t.Parallel()

	// (1) 옵션 없음 → nil (기존 동작 보존).
	plain := mongodbv1alpha1.ShardedCollectionSpec{Database: "db", Collection: "c"}
	if got := buildShardCollectionOptions(plain); got != nil {
		t.Errorf("plain spec should map to nil opts, got %v", got)
	}

	// (2) unique=true → opts.Unique.
	uniq := mongodbv1alpha1.ShardedCollectionSpec{Database: "db", Collection: "c", Unique: true}
	got := buildShardCollectionOptions(uniq)
	if got == nil || !got.Unique || got.Timeseries != nil {
		t.Errorf("unique spec mapping wrong: %+v", got)
	}

	// (3) timeseries 매핑.
	ts := mongodbv1alpha1.ShardedCollectionSpec{
		Database:   "crypto",
		Collection: "binance",
		Timeseries: &mongodbv1alpha1.TimeseriesSpec{TimeField: "openTime", MetaField: "meta", Granularity: "seconds"},
	}
	gotTs := buildShardCollectionOptions(ts)
	if gotTs == nil || gotTs.Timeseries == nil {
		t.Fatalf("timeseries spec should produce opts.Timeseries, got %+v", gotTs)
	}
	if gotTs.Timeseries.TimeField != "openTime" || gotTs.Timeseries.MetaField != "meta" || gotTs.Timeseries.Granularity != "seconds" {
		t.Errorf("timeseries mapping wrong: %+v", gotTs.Timeseries)
	}
}

// TestBuildShardKey_DefaultOrder — 알 수 없는 order 는 "1"(ascending)로 fallback.
// (webhook 이 enum 을 강제하지만 builder 의 default 안전성 가드.)
func TestBuildShardKey_DefaultOrder(t *testing.T) {
	t.Parallel()
	got := buildShardKey([]mongodbv1alpha1.ShardKeyField{{Field: "x", Order: ""}})
	if got[0].Value != int32(1) {
		t.Errorf("empty order should fall back to 1, got %v", got[0].Value)
	}
}

// TestIsNamespaceNotFound — 컬렉션 미생성 에러 분류(비치명).
func TestIsNamespaceNotFound(t *testing.T) {
	t.Parallel()
	cases := []struct {
		msg  string
		want bool
	}{
		{"shardCollection: (NamespaceNotFound) ns not found", true},
		{"ns not found", true},
		{"collection db.c does not exist", true},
		{"ShardKeyDrift: different key", false},
		{"some other server error", false},
	}
	for _, tc := range cases {
		if got := isNamespaceNotFound(tc.msg); got != tc.want {
			t.Errorf("isNamespaceNotFound(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// TestIsConnectError — 연결/네트워크 일시 실패 분류(비치명 requeue).
func TestIsConnectError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		msg  string
		want bool
	}{
		{"connect for EnsureShardedCollection: dial fail", true},
		{"server selection error: context deadline exceeded", true},
		{"connection refused", true},
		{"no reachable servers", true},
		{"ShardKeyDrift", false},
		{"NamespaceNotFound", false},
	}
	for _, tc := range cases {
		if got := isConnectError(tc.msg); got != tc.want {
			t.Errorf("isConnectError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// 컴파일 가드: bson 패키지가 buildShardKey 반환 타입과 정합한지.
var _ = bson.D{}
