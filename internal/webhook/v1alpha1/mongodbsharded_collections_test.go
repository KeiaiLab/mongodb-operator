/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Track B — 선언적 컬렉션 샤딩 webhook 형식 검증 단위 테스트.
//
// validateShardedCollections 의 형식 검증(db/coll non-empty, order enum, 중복 ns,
// timeseries + unique 모순, timeField hashed 금지)을 결정론적으로 검증한다.
// 네트워크 0, server 무관 — pure 검증 함수.

package v1alpha1

import (
	"strings"
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func scSpec(db, coll string, key []mongodbv1alpha1.ShardKeyField) mongodbv1alpha1.ShardedCollectionSpec {
	return mongodbv1alpha1.ShardedCollectionSpec{Database: db, Collection: coll, ShardKey: key}
}

func k(field, order string) mongodbv1alpha1.ShardKeyField {
	return mongodbv1alpha1.ShardKeyField{Field: field, Order: order}
}

// TestValidateShardedCollections_Valid — 정상 케이스는 에러 0.
func TestValidateShardedCollections_Valid(t *testing.T) {
	t.Parallel()
	scs := []mongodbv1alpha1.ShardedCollectionSpec{
		scSpec("crypto", "binance", []mongodbv1alpha1.ShardKeyField{k("meta.symbol", "hashed")}),
		scSpec("app", "users", []mongodbv1alpha1.ShardKeyField{k("region", "1"), k("userId", "1")}),
	}
	errs := validateShardedCollections(nil, scs)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

// TestValidateShardedCollections_EmptyDBColl — db/coll 빈 값 거부.
func TestValidateShardedCollections_EmptyDBColl(t *testing.T) {
	t.Parallel()
	scs := []mongodbv1alpha1.ShardedCollectionSpec{
		scSpec("", "coll", []mongodbv1alpha1.ShardKeyField{k("x", "1")}),
		scSpec("db", "", []mongodbv1alpha1.ShardKeyField{k("x", "1")}),
	}
	errs := validateShardedCollections(nil, scs)
	var dbErr, collErr bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "database") {
			dbErr = true
		}
		if strings.Contains(e.Error(), "collection") {
			collErr = true
		}
	}
	if !dbErr || !collErr {
		t.Errorf("expected db+coll errors, got %v", errs)
	}
}

// TestValidateShardedCollections_BadOrder — order enum 외 값 거부.
func TestValidateShardedCollections_BadOrder(t *testing.T) {
	t.Parallel()
	scs := []mongodbv1alpha1.ShardedCollectionSpec{
		scSpec("db", "coll", []mongodbv1alpha1.ShardKeyField{k("x", "2")}),
	}
	errs := validateShardedCollections(nil, scs)
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "order") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected order error, got %v", errs)
	}
}

// TestValidateShardedCollections_DuplicateNamespace — 동일 ns 중복 선언 거부.
func TestValidateShardedCollections_DuplicateNamespace(t *testing.T) {
	t.Parallel()
	scs := []mongodbv1alpha1.ShardedCollectionSpec{
		scSpec("db", "coll", []mongodbv1alpha1.ShardKeyField{k("x", "1")}),
		scSpec("db", "coll", []mongodbv1alpha1.ShardKeyField{k("y", "hashed")}),
	}
	errs := validateShardedCollections(nil, scs)
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "Duplicate") || strings.Contains(e.Error(), "duplicate") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate namespace error, got %v", errs)
	}
}

// TestValidateShardedCollections_EmptyKey — shardKey 0개 거부.
func TestValidateShardedCollections_EmptyKey(t *testing.T) {
	t.Parallel()
	scs := []mongodbv1alpha1.ShardedCollectionSpec{
		scSpec("db", "coll", nil),
	}
	errs := validateShardedCollections(nil, scs)
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "shardKey") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected shardKey error, got %v", errs)
	}
}

// TestValidateShardedCollections_TimeseriesUnique — timeseries + unique 모순 거부.
func TestValidateShardedCollections_TimeseriesUnique(t *testing.T) {
	t.Parallel()
	sc := scSpec("crypto", "binance", []mongodbv1alpha1.ShardKeyField{k("meta.symbol", "hashed")})
	sc.Unique = true
	sc.Timeseries = &mongodbv1alpha1.TimeseriesSpec{TimeField: "openTime", MetaField: "meta", Granularity: "seconds"}
	errs := validateShardedCollections(nil, []mongodbv1alpha1.ShardedCollectionSpec{sc})
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "unique") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unique+timeseries conflict error, got %v", errs)
	}
}

// TestValidateShardedCollections_TimeseriesEmptyTimeField — timeField 빈 값 거부.
func TestValidateShardedCollections_TimeseriesEmptyTimeField(t *testing.T) {
	t.Parallel()
	sc := scSpec("crypto", "binance", []mongodbv1alpha1.ShardKeyField{k("meta.symbol", "hashed")})
	sc.Timeseries = &mongodbv1alpha1.TimeseriesSpec{TimeField: ""}
	errs := validateShardedCollections(nil, []mongodbv1alpha1.ShardedCollectionSpec{sc})
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "timeField") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected timeField error, got %v", errs)
	}
}

// TestValidateShardedCollections_TimeFieldHashed — timeField 를 hashed 키로 쓰면 거부.
func TestValidateShardedCollections_TimeFieldHashed(t *testing.T) {
	t.Parallel()
	sc := scSpec("crypto", "binance", []mongodbv1alpha1.ShardKeyField{k("openTime", "hashed")})
	sc.Timeseries = &mongodbv1alpha1.TimeseriesSpec{TimeField: "openTime", MetaField: "meta"}
	errs := validateShardedCollections(nil, []mongodbv1alpha1.ShardedCollectionSpec{sc})
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "hashed shard key") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected timeField-hashed error, got %v", errs)
	}
}

// TestValidateMongoDBShardedSpec_ShardedCollections_Integration — 통합 경로에서
// validateShardedCollections 가 호출되는지 확인(2-layer).
func TestValidateMongoDBShardedSpec_ShardedCollections_Integration(t *testing.T) {
	t.Parallel()
	m := &mongodbv1alpha1.MongoDBSharded{}
	m.Spec.Version.Version = "8.3"
	m.Spec.Shards.Count = 2
	m.Spec.Shards.MembersPerShard = 3
	m.Spec.Auth.AdminCredentialsSecretRef.Name = "creds"
	m.Spec.ShardedCollections = []mongodbv1alpha1.ShardedCollectionSpec{
		scSpec("db", "coll", []mongodbv1alpha1.ShardKeyField{k("x", "bad")}),
	}
	errs := validateMongoDBShardedSpec(m)
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "order") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected order error surfaced via validateMongoDBShardedSpec, got %v", errs)
	}
}
