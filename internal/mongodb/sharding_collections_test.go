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

// TestNormalizeShardKeyValue_NonIntegerFloat — B-MEDIUM: 비정수 float 은 int64
// truncate 로 정수와 충돌하면 안 된다(2.9 와 2 가 같은 "2" → false-positive
// equality → drift 흡수). 비정수 float 은 고유 표현을 남겨야 한다.
func TestNormalizeShardKeyValue_NonIntegerFloat(t *testing.T) {
	t.Parallel()
	// 2.9 != 2: truncate 시 둘 다 "2" 로 충돌하던 버그.
	if normalizeShardKeyValue(float64(2.9)) == normalizeShardKeyValue(int32(2)) {
		t.Errorf("float64(2.9) 와 int32(2) 가 같은 정규화 결과(%q)로 충돌 — drift 흡수 위험",
			normalizeShardKeyValue(float64(2.9)))
	}
	if normalizeShardKeyValue(float64(2.9)) == normalizeShardKeyValue(int64(3)) {
		t.Errorf("float64(2.9) 가 int64(3) 와 충돌해서는 안 됨")
	}
	// 정수값 float 은 여전히 정수와 동등(1.0 == int32(1)).
	if normalizeShardKeyValue(float64(1.0)) != normalizeShardKeyValue(int32(1)) {
		t.Errorf("float64(1.0) 는 int32(1) 와 동등해야 함")
	}
	// shardKeyEqual 레벨에서도 비정수 float drift 가 흡수되지 않는지.
	a := bson.D{{Key: "x", Value: float64(2.9)}}
	b := bson.D{{Key: "x", Value: int32(2)}}
	if shardKeyEqual(a, b) {
		t.Error("shardKeyEqual(x:2.9, x:2) = true — 비정수 float drift 가 흡수됨(B-MEDIUM)")
	}
}

// TestBucketsNamespace — timeseries buckets ns 변환(사용자 ns → buckets ns).
func TestBucketsNamespace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		db, coll, want string
	}{
		{"crypto", "binance_spot_usdt_klines_1m", "crypto.system.buckets.binance_spot_usdt_klines_1m"},
		{"db", "c", "db.system.buckets.c"},
		// 이미 buckets prefix 가 붙은 collection 은 중복 부착 안 함(idempotent).
		{"crypto", "system.buckets.x", "crypto.system.buckets.x"},
	}
	for _, tc := range cases {
		if got := bucketsNamespace(tc.db, tc.coll); got != tc.want {
			t.Errorf("bucketsNamespace(%q,%q) = %q, want %q", tc.db, tc.coll, got, tc.want)
		}
	}
}

// TestUserNamespaceFromBuckets — buckets ns → 사용자 ns 역변환(Status 정규화).
func TestUserNamespaceFromBuckets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		// 라이브 실측 형태.
		{"crypto.system.buckets.binance_spot_usdt_klines_1m", "crypto.binance_spot_usdt_klines_1m"},
		{"db.system.buckets.c", "db.c"},
		// buckets ns 가 아니면 그대로(비-timeseries 사용자 ns).
		{"crypto.binance", "crypto.binance"},
		{"db.users", "db.users"},
		// 점 없는 비정상 입력은 그대로.
		{"weird", "weird"},
	}
	for _, tc := range cases {
		if got := userNamespaceFromBuckets(tc.in); got != tc.want {
			t.Errorf("userNamespaceFromBuckets(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDecideEnsureAction — B-HIGH idempotency 결정 로직(pure). config.collections
// 조회 결과로부터 "재샤딩 여부"를 결정한다. 핵심: timeseries 가 buckets ns 로
// 등록된 상태에서 매 reconcile 마다 재샤딩하지 않아야(doShard=false) 무한 루프가
// 차단된다.
func TestDecideEnsureAction(t *testing.T) {
	t.Parallel()
	hashedKey := bson.D{{Key: "meta.symbol", Value: "hashed"}}
	rangeKey := bson.D{{Key: "userId", Value: int32(1)}}

	cases := []struct {
		name      string
		cur       *configCollectionDoc
		isBuckets bool
		key       bson.D
		wantShard bool
		wantErr   bool
	}{
		{
			name:      "미등록 → 샤딩 수행",
			cur:       nil,
			key:       hashedKey,
			wantShard: true,
		},
		{
			name: "timeseries buckets ns 등록(키 표현 다름) → idempotent skip(재샤딩 안 함)",
			// 라이브 실측: spec meta.symbol:hashed → buckets 등록 {meta.symbol:1}.
			// 키가 달라 보여도 buckets ns 매칭은 무조건 skip(B-HIGH 무한 루프 차단).
			cur:       &configCollectionDoc{ID: "crypto.system.buckets.binance_spot_usdt_klines_1m", Key: bson.D{{Key: "meta.symbol", Value: int32(1)}}},
			isBuckets: true,
			key:       hashedKey,
			wantShard: false,
			wantErr:   false,
		},
		{
			name:      "사용자 ns 등록 동일 키 → idempotent skip",
			cur:       &configCollectionDoc{ID: "db.users", Key: bson.D{{Key: "userId", Value: int32(1)}}},
			isBuckets: false,
			key:       rangeKey,
			wantShard: false,
			wantErr:   false,
		},
		{
			name:      "사용자 ns 등록 다른 키 → ShardKeyDrift err(흡수 금지)",
			cur:       &configCollectionDoc{ID: "db.users", Key: bson.D{{Key: "region", Value: int32(1)}}},
			isBuckets: false,
			key:       rangeKey,
			wantShard: false,
			wantErr:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doShard, err := decideEnsureAction(tc.cur, tc.isBuckets, "ns", tc.key)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if doShard != tc.wantShard {
				t.Errorf("doShard = %v, want %v", doShard, tc.wantShard)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), "ShardKeyDrift") {
				t.Errorf("err %q should contain ShardKeyDrift", err.Error())
			}
		})
	}
}

// TestDecideEnsureAction_TimeseriesIdempotentAcrossReconciles — B-HIGH 회귀 가드.
// 첫 reconcile 에서 샤딩(미등록 → doShard=true) 후, MongoDB 가 buckets ns 로
// 등록한 상태를 둘째 reconcile 이 관측하면 *재샤딩하지 않아야* 한다(doShard=false).
// 이 두 단계가 idempotency 의 핵심 — 둘째에서 true 면 매 reconcile 재샤딩 무한
// 루프가 재발한다.
func TestDecideEnsureAction_TimeseriesIdempotentAcrossReconciles(t *testing.T) {
	t.Parallel()
	specKey := bson.D{{Key: "meta.symbol", Value: "hashed"}}

	// 1차 reconcile: 사용자 ns/buckets ns 모두 미등록 → 샤딩 수행.
	if doShard, err := decideEnsureAction(nil, false, "crypto.binance_spot_usdt_klines_1m", specKey); err != nil || !doShard {
		t.Fatalf("1차 reconcile: doShard=%v err=%v, want true/nil", doShard, err)
	}

	// 2차 reconcile: lookupShardedCollectionAny 가 buckets ns 에서 매칭한 상태
	// (라이브 실측 등록 키 {meta.symbol:1}). 재샤딩 안 함.
	registered := &configCollectionDoc{
		ID:  "crypto.system.buckets.binance_spot_usdt_klines_1m",
		Key: bson.D{{Key: "meta.symbol", Value: int32(1)}},
	}
	doShard, err := decideEnsureAction(registered, true, "crypto.binance_spot_usdt_klines_1m", specKey)
	if err != nil {
		t.Fatalf("2차 reconcile: unexpected err %v", err)
	}
	if doShard {
		t.Error("2차 reconcile: doShard=true — 이미 샤딩된 timeseries 를 재샤딩(B-HIGH 무한 루프 재발)")
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
