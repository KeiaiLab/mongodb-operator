/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package mongodb

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ShardStatus represents the status of a shard
type ShardStatus struct {
	ID    string `json:"_id" bson:"_id"`
	Host  string `json:"host" bson:"host"`
	State int    `json:"state" bson:"state"`
}

// ShardManager는 mongos에 driver로 연결해 sharding 관리 명령을 수행한다.
//
// AddShard 같은 메서드는 mongos 라우터를 통해서만 의미가 있다 — config
// server나 shard pod에 직접 보내면 안 된다. 호출자는 ShardManager를 mongos
// pod에 대한 ConnectFactory로 만들어야 한다.
type ShardManager struct {
	connect ConnectFactory
}

// NewShardManagerWithFactory는 driver client factory를 주입받아 매니저를 만든다.
// factory는 반드시 mongos pod를 대상으로 해야 한다.
func NewShardManagerWithFactory(f ConnectFactory) *ShardManager {
	return &ShardManager{connect: f}
}

// NewShardManager — Deprecated.
func NewShardManager() (*ShardManager, error) {
	return nil, errors.New("NewShardManager는 deprecated. NewShardManagerWithFactory(factory) 사용")
}

// AddShard adds a shard to the cluster via mongos. Idempotent: 이미 추가된 shard면
// "already a shard" server error를 정상 처리.
func (s *ShardManager) AddShard(ctx context.Context, mongosPod, namespace, shardConnectionString string) error {
	return s.AddShardInContainer(ctx, mongosPod, namespace, "mongos", shardConnectionString, 27017)
}

// AddShardInContainer는 AddShard와 같지만 container/port 인자는 driver 모델에서
// 무시된다. (호환성 유지)
func (s *ShardManager) AddShardInContainer(ctx context.Context, mongosPod, namespace, container, shardConnectionString string, port int) error {
	return s.runAdminCommand(ctx, mongosPod, namespace,
		bson.D{{Key: "addShard", Value: shardConnectionString}}, "addShard")
}

// AddShardWithAuth adds a shard with authentication. adminUser/adminPassword는
// factory가 캡슐화하므로 무시된다 (호환성 유지).
func (s *ShardManager) AddShardWithAuth(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, shardConnectionString string) error {
	return s.AddShard(ctx, mongosPod, namespace, shardConnectionString)
}

// AddShardWithAuthInContainer — 호환성 wrapper.
func (s *ShardManager) AddShardWithAuthInContainer(ctx context.Context, mongosPod, namespace, container, adminUser, adminPassword, shardConnectionString string, port int) error {
	return s.AddShardInContainer(ctx, mongosPod, namespace, container, shardConnectionString, port)
}

// RemoveShard removes a shard from the cluster.
func (s *ShardManager) RemoveShard(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, shardName string) error {
	return s.runAdminCommand(ctx, mongosPod, namespace,
		bson.D{{Key: "removeShard", Value: shardName}}, "removeShard")
}

// RemoveShardResult는 MongoDB의 removeShard 명령 응답을 담는다.
// State는 "started" | "ongoing" | "completed" 중 하나로, controller는 이 값으로
// drain 진행 상황을 추적해 status.condition을 갱신한다.
type RemoveShardResult struct {
	State     string `bson:"state"`
	Msg       string `bson:"msg"`
	Shard     string `bson:"shard"`
	Remaining struct {
		Chunks    int `bson:"chunks"`
		Databases int `bson:"dbs"`
	} `bson:"remaining"`
}

// RemoveShardWithStatus는 removeShard 명령을 호출하고 응답의 state/remaining을
// 그대로 반환한다. RemoveShard와 달리 진행 상황을 controller가 관측 가능.
//
// 멱등성: 동일 shard에 반복 호출하면 매번 state가 진행되어(started → ongoing
// → completed) 안전하게 polling 가능. 이미 제거된 shard에 호출하면 server가
// "shard not found" error 반환 — 호출자가 isShardNotFound 등으로 처리.
func (s *ShardManager) RemoveShardWithStatus(ctx context.Context, mongosPod, namespace, shardName string) (*RemoveShardResult, error) {
	c, err := s.connect(ctx, mongosPod, namespace, true)
	if err != nil {
		return nil, fmt.Errorf("connect for removeShard: %w", err)
	}
	defer disconnectQuiet(c)

	var res RemoveShardResult
	if err := c.Database(adminUserDB).RunCommand(ctx, bson.D{{Key: "removeShard", Value: shardName}}).Decode(&res); err != nil {
		return nil, fmt.Errorf("removeShard: %w", err)
	}
	return &res, nil
}

// ListShards returns the list of shards in the cluster.
func (s *ShardManager) ListShards(ctx context.Context, mongosPod, namespace string) ([]ShardStatus, error) {
	c, err := s.connect(ctx, mongosPod, namespace, true)
	if err != nil {
		return nil, fmt.Errorf("connect for ListShards: %w", err)
	}
	defer disconnectQuiet(c)

	var result struct {
		Shards []ShardStatus `bson:"shards"`
		OK     float64       `bson:"ok"`
	}
	if err := c.Database(adminUserDB).RunCommand(ctx, bson.D{{Key: "listShards", Value: 1}}).Decode(&result); err != nil {
		return nil, fmt.Errorf("listShards: %w", err)
	}
	return result.Shards, nil
}

// IsShardAdded checks if a shard is already added to the cluster.
func (s *ShardManager) IsShardAdded(ctx context.Context, mongosPod, namespace, shardName string) (bool, error) {
	shards, err := s.ListShards(ctx, mongosPod, namespace)
	if err != nil {
		return false, err
	}
	for _, shard := range shards {
		if shard.ID == shardName {
			return true, nil
		}
	}
	return false, nil
}

// EnableSharding enables sharding on a database.
func (s *ShardManager) EnableSharding(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, database string) error {
	return s.runAdminCommand(ctx, mongosPod, namespace,
		bson.D{{Key: "enableSharding", Value: database}}, "enableSharding")
}

// ShardCollectionOptions 는 ShardCollection 의 선택적 인자를 담는다. 기존
// 호출부(key 만 지정)와의 호환을 위해 별도 구조체로 분리하고 ShardCollection 의
// 마지막 인자로 받는다(zero value = 기존 동작).
type ShardCollectionOptions struct {
	// Unique 는 shard key 에 unique 제약을 강제한다. timeseries 컬렉션은 unique
	// 불가(호출자/webhook 에서 차단).
	Unique bool

	// Timeseries 가 non-nil 이면 timeseries 컬렉션으로 샤딩한다. shardCollection
	// 명령에 timeseries:{timeField,metaField,granularity} 서브도큐먼트를 부착한다.
	Timeseries *TimeseriesOptions
}

// TimeseriesOptions 는 timeseries 컬렉션 샤딩 시 shardCollection 명령에 부착하는
// timeseries 서브도큐먼트.
type TimeseriesOptions struct {
	TimeField   string
	MetaField   string
	Granularity string
}

// ShardCollection shards a collection with the given key.
//
// key 는 bson.D(순서 보존 slice)로 받는다. compound shard key 의 필드 순서는
// MongoDB shardCollection 명령에서 의미가 있으므로 map(무순서)으로 받으면
// 매 호출마다 키 순서가 비결정적이 된다 — 의도와 다른 shard key 생성 위험.
//
// opts 가 nil 이면 unique/timeseries 옵션 없이 기존과 동일하게 동작한다.
func (s *ShardManager) ShardCollection(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, collection string, key bson.D, opts *ShardCollectionOptions) error {
	cmd := bson.D{
		{Key: "shardCollection", Value: collection},
		{Key: "key", Value: key},
	}
	if opts != nil {
		if opts.Unique {
			cmd = append(cmd, bson.E{Key: "unique", Value: true})
		}
		if opts.Timeseries != nil {
			ts := bson.D{{Key: "timeField", Value: opts.Timeseries.TimeField}}
			if opts.Timeseries.MetaField != "" {
				ts = append(ts, bson.E{Key: "metaField", Value: opts.Timeseries.MetaField})
			}
			if opts.Timeseries.Granularity != "" {
				ts = append(ts, bson.E{Key: "granularity", Value: opts.Timeseries.Granularity})
			}
			cmd = append(cmd, bson.E{Key: "timeseries", Value: ts})
		}
	}
	return s.runShardCollectionCommand(ctx, mongosPod, namespace, cmd)
}

// runShardCollectionCommand 는 shardCollection 전용 실행 경로다. runAdminCommand
// 의 'already sharded' substring 흡수를 *적용하지 않는다*(B-0 high) — 이미 다른
// 키로 샤딩된 컬렉션을 흡수하면 키 drift 가 silent 하게 묻힌다. 키 비교는
// EnsureShardedCollection 의 config.collections 사전 조회가 담당하고, 여기서는
// 명령 실패를 그대로 surface 한다.
func (s *ShardManager) runShardCollectionCommand(ctx context.Context, mongosPod, namespace string, cmd bson.D) error {
	c, err := s.connect(ctx, mongosPod, namespace, true)
	if err != nil {
		return fmt.Errorf("connect for shardCollection: %w", err)
	}
	defer disconnectQuiet(c)

	var result bson.M
	if err := c.Database(adminUserDB).RunCommand(ctx, cmd).Decode(&result); err != nil {
		return fmt.Errorf("shardCollection: %w", err)
	}
	return nil
}

// ShardedCollectionInfo 는 config.collections 에 등록된 샤딩 컬렉션의 관측 결과.
type ShardedCollectionInfo struct {
	// Namespace 는 "db.coll" 형식.
	Namespace string
	// Key 는 등록된 shard key (순서 보존).
	Key bson.D
}

// configCollectionDoc 는 config.collections 도큐먼트 디코드용. _id = namespace,
// key = shard key, dropped = soft-delete 마커.
type configCollectionDoc struct {
	ID      string `bson:"_id"`
	Key     bson.D `bson:"key"`
	Dropped bool   `bson:"dropped"`
}

// bucketsNamespace 는 timeseries 컬렉션이 config.collections 에 등록되는 내부
// buckets namespace 를 만든다 — MongoDB 는 샤딩된 timeseries 를 사용자 ns
// "<db>.<coll>" 이 아니라 "<db>.system.buckets.<coll>" 로 등록한다(라이브 실측,
// crypto.system.buckets.binance_spot_usdt_klines_1m). 이미 buckets prefix 가
// 붙은 ns 는 그대로 둔다(idempotent).
func bucketsNamespace(database, collection string) string {
	if strings.HasPrefix(collection, "system.buckets.") {
		return database + "." + collection
	}
	return database + ".system.buckets." + collection
}

// userNamespaceFromBuckets 는 buckets namespace 를 사용자 ns 로 역변환한다.
// "<db>.system.buckets.<coll>" → "<db>.<coll>". buckets ns 가 아니면 그대로 반환.
// Status 정규화(buckets ns 를 사용자 ns 로 기록)에 사용.
func userNamespaceFromBuckets(namespace string) string {
	dot := strings.IndexByte(namespace, '.')
	if dot < 0 {
		return namespace
	}
	db, rest := namespace[:dot], namespace[dot+1:]
	if !strings.HasPrefix(rest, "system.buckets.") {
		return namespace
	}
	return db + "." + strings.TrimPrefix(rest, "system.buckets.")
}

// lookupShardedCollection 은 config.collections 에서 namespace 의 현재 shard key
// 를 조회한다. 미등록(미샤딩)이면 (nil, nil). dropped=true 는 미샤딩으로 취급.
func (s *ShardManager) lookupShardedCollection(ctx context.Context, c *mongo.Client, namespace string) (*configCollectionDoc, error) {
	var doc configCollectionDoc
	err := c.Database("config").Collection("collections").
		FindOne(ctx, bson.D{{Key: "_id", Value: namespace}}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, fmt.Errorf("lookup config.collections %s: %w", namespace, err)
	}
	if doc.Dropped {
		return nil, nil
	}
	return &doc, nil
}

// lookupShardedCollectionAny 는 사용자 ns 와 timeseries buckets ns 를 모두 조회해
// 둘 중 *하나라도* 등록돼 있으면 그 도큐먼트를 반환한다(B-HIGH timeseries
// idempotency). MongoDB 는 샤딩된 timeseries 를 buckets ns 로 등록하므로 사용자
// ns 만 조회하면 매 reconcile 마다 미샤딩으로 오판 → 재샤딩 무한 루프가 발생한다.
//
// 반환: (doc, isBuckets, err). doc==nil 이면 둘 다 미등록(미샤딩). isBuckets 는
// buckets ns 에서 매칭됐는지(timeseries) 여부 — 호출자가 키 비교/Status 정규화
// 분기에 사용.
func (s *ShardManager) lookupShardedCollectionAny(ctx context.Context, c *mongo.Client, database, collection string) (*configCollectionDoc, bool, error) {
	userNS := database + "." + collection
	if doc, err := s.lookupShardedCollection(ctx, c, userNS); err != nil {
		return nil, false, err
	} else if doc != nil {
		return doc, false, nil
	}

	bucketsNS := bucketsNamespace(database, collection)
	if doc, err := s.lookupShardedCollection(ctx, c, bucketsNS); err != nil {
		return nil, false, err
	} else if doc != nil {
		return doc, true, nil
	}
	return nil, false, nil
}

// shardKeyEqual 은 두 shard key(bson.D)가 *순서까지* 동일한지 비교한다.
// compound key 의 필드 순서가 의미를 가지므로 순서 무시 비교는 부적절하다.
// 값은 string(범위: "1"/"-1") 또는 int32/int64/float64("hashed" 는 string,
// 범위 키는 BSON 에서 숫자) 가 섞일 수 있어 normalizeShardKeyValue 로 정규화 비교.
func shardKeyEqual(a, b bson.D) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key {
			return false
		}
		if normalizeShardKeyValue(a[i].Value) != normalizeShardKeyValue(b[i].Value) {
			return false
		}
	}
	return true
}

// normalizeShardKeyValue 는 shard key field 값(1/-1/"hashed")을 비교 가능한
// 정규 문자열로 변환한다. config.collections 의 key 값은 BSON 숫자(int32/int64/
// float64)로 디코드되고, spec 에서 빌드한 키는 같은 숫자 타입이지만 driver 디코드
// 경로가 달라 직접 == 비교가 깨질 수 있어 정규화한다.
//
// float64 는 *정수값일 때만* 정수 문자열로 정규화한다(예: 1.0 → "1", int32(1)
// 과 동등). 비정수 float(예: 2.9)을 int64(t) 로 truncate 하면 2.9 와 2 가 같은
// "2" 로 정규화돼 false-positive equality → 실제 drift 흡수(B-MEDIUM, 이 PR 이
// 막으려는 실패 모드). 비정수 float 은 truncate 하지 않고 %v fallback 으로 고유
// 표현("2.9")을 남겨 정수값과 구분한다.
func normalizeShardKeyValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int32:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case int:
		return fmt.Sprintf("%d", t)
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// EnsureShardedCollection 은 선언적 컬렉션 샤딩을 idempotent 하게 적용한다.
//
// 흐름:
//  1. config.collections 사전 조회로 namespace 의 현재 shard key 확인 — 사용자
//     ns "<db>.<coll>" 와 timeseries buckets ns "<db>.system.buckets.<coll>" 를
//     *모두* 조회한다(B-HIGH). MongoDB 는 샤딩된 timeseries 를 buckets ns 로만
//     등록하므로 사용자 ns 만 조회하면 매 reconcile 마다 미샤딩 오판 → 재샤딩
//     무한 루프(라이브 실측: crypto.system.buckets.binance_spot_usdt_klines_1m).
//  2. 미등록 → EnableSharding(db)[idempotent] 선행 후 ShardCollection 실행.
//  3. 이미 동일 키로 샤딩됨 → skip(no-op).
//  4. 이미 *다른 키*로 샤딩됨 → 에러 반환(흡수 금지, B-0 high). 호출자가
//     ShardKeyDrift condition 으로 노출한다.
//
// timeseries 특수성: buckets ns 에 등록된 키는 MongoDB 내부 표현이라(라이브 실측:
// spec meta.symbol:hashed → buckets 등록 key {meta.symbol:1}) spec 키와 직접
// 비교하면 영원히 drift 로 오판한다. 따라서 buckets ns 매칭은 *샤딩됨으로 판정해
// idempotent skip* 하고 키 drift 비교를 적용하지 않는다(재샤딩/재해석 불가능한
// 내부 키이므로). 사용자 ns 매칭(비-timeseries)만 엄격 키 비교를 유지한다.
//
// database/collection 은 호출자가 분리해 전달한다(namespace = db.coll).
// key 는 순서 보존 bson.D, opts 는 unique/timeseries.
func (s *ShardManager) EnsureShardedCollection(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, database, collection string, key bson.D, opts *ShardCollectionOptions) error {
	ns := database + "." + collection

	c, err := s.connect(ctx, mongosPod, namespace, true)
	if err != nil {
		return fmt.Errorf("connect for EnsureShardedCollection: %w", err)
	}
	cur, isBuckets, lookupErr := s.lookupShardedCollectionAny(ctx, c, database, collection)
	disconnectQuiet(c)
	if lookupErr != nil {
		return lookupErr
	}

	doShard, err := decideEnsureAction(cur, isBuckets, ns, key)
	if err != nil {
		return err
	}
	if !doShard {
		return nil // 이미 샤딩됨(동일 키 또는 timeseries buckets ns) — idempotent skip
	}

	// 미샤딩 — EnableSharding(idempotent) 선행 후 ShardCollection.
	if err := s.EnableSharding(ctx, mongosPod, namespace, adminUser, adminPassword, database); err != nil {
		return fmt.Errorf("enableSharding %s: %w", database, err)
	}
	if err := s.ShardCollection(ctx, mongosPod, namespace, adminUser, adminPassword, ns, key, opts); err != nil {
		return err
	}
	return nil
}

// decideEnsureAction 은 config.collections 조회 결과로부터 샤딩 액션을 결정하는
// pure 함수다(side effect 0 — 단위 테스트 가능, B-HIGH idempotency 의 핵심 결정
// 로직). 반환: (doShard, err).
//
//   - cur==nil(미등록) → (true, nil): ShardCollection 수행.
//   - cur!=nil & isBuckets(timeseries buckets ns 등록) → (false, nil): idempotent
//     skip. buckets ns 의 등록 키는 MongoDB 내부 표현이라 spec 키와 직접 비교하면
//     영원히 drift 로 오판하므로 키 비교 없이 샤딩됨으로 판정한다. 이것이 매
//     reconcile 재샤딩 → fatal 무한 루프(B-HIGH) 차단 핵심.
//   - cur!=nil & !isBuckets(사용자 ns 등록, 비-timeseries) → 엄격 키 비교:
//     동일 키면 (false,nil) skip, 다른 키면 (false, ShardKeyDrift err).
func decideEnsureAction(cur *configCollectionDoc, isBuckets bool, ns string, key bson.D) (bool, error) {
	if cur == nil {
		return true, nil
	}
	if isBuckets {
		return false, nil
	}
	if shardKeyEqual(cur.Key, key) {
		return false, nil
	}
	return false, fmt.Errorf("collection %s already sharded with different key %v (desired %v) — ShardKeyDrift", ns, cur.Key, key)
}

// ListShardedNamespaces 는 config.collections 에 등록된(미-dropped) 샤딩 컬렉션의
// namespace + shard key 목록을 반환한다. updateStatus 가 Status.ShardedCollections
// 를 실측으로 채울 때 사용. 관측 전용(side effect 0).
func (s *ShardManager) ListShardedNamespaces(ctx context.Context, mongosPod, namespace string) ([]ShardedCollectionInfo, error) {
	c, err := s.connect(ctx, mongosPod, namespace, true)
	if err != nil {
		return nil, fmt.Errorf("connect for ListShardedNamespaces: %w", err)
	}
	defer disconnectQuiet(c)

	cursor, err := c.Database("config").Collection("collections").
		Find(ctx, bson.D{{Key: "dropped", Value: false}})
	if err != nil {
		return nil, fmt.Errorf("find config.collections: %w", err)
	}
	defer func() { _ = cursor.Close(ctx) }()

	var infos []ShardedCollectionInfo
	for cursor.Next(ctx) {
		var doc configCollectionDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode config.collections doc: %w", err)
		}
		// config DB 내부 컬렉션(config.system.sessions 등)은 namespace prefix
		// "config." 로 시작 — 사용자 컬렉션만 노출하지 않고 그대로 반환하되,
		// 호출자가 필요 시 필터. 여기서는 등록된 모든 샤딩 ns 를 보고한다.
		//
		// timeseries 정규화(B-HIGH): MongoDB 는 샤딩된 timeseries 를 buckets ns
		// "<db>.system.buckets.<coll>" 로 등록한다 — Status.ShardedCollections 가
		// spec ns 와 영원히 불일치하지 않도록 사용자 ns "<db>.<coll>" 로 역변환해
		// 보고한다.
		infos = append(infos, ShardedCollectionInfo{Namespace: userNamespaceFromBuckets(doc.ID), Key: doc.Key})
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate config.collections: %w", err)
	}
	return infos, nil
}

// runAdminCommand는 mongos에 admin database 명령을 실행하고 idempotent한 server
// error("already a shard", "already enabled" 등)를 정상 처리한다.
func (s *ShardManager) runAdminCommand(ctx context.Context, mongosPod, namespace string, cmd bson.D, op string) error {
	c, err := s.connect(ctx, mongosPod, namespace, true)
	if err != nil {
		return fmt.Errorf("connect for %s: %w", op, err)
	}
	defer disconnectQuiet(c)

	var result bson.M
	err = c.Database(adminUserDB).RunCommand(ctx, cmd).Decode(&result)
	if err == nil {
		return nil
	}
	// idempotent server-side errors는 정상 처리.
	var srvErr mongo.ServerError
	if errors.As(err, &srvErr) {
		msg := srvErr.Error()
		if strings.Contains(msg, "already a shard") ||
			strings.Contains(msg, "already exists") ||
			strings.Contains(msg, "already enabled") ||
			strings.Contains(msg, "already sharded") {
			return nil
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

// BuildShardConnectionString builds a connection string for adding a shard.
// Format: shardName/host1:port,host2:port,host3:port
func BuildShardConnectionString(shardName, baseName, serviceName, namespace string, members int, port int) string {
	hosts := make([]string, members)
	for i := 0; i < members; i++ {
		podName := fmt.Sprintf("%s-%d", baseName, i)
		hosts[i] = GetPodFQDN(podName, serviceName, namespace, port)
	}
	return fmt.Sprintf("%s/%s", shardName, strings.Join(hosts, ","))
}
