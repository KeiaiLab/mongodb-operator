/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// fetcher.go — ProfileFetcher 좁은 interface + MongoProfileFetcher 구현.
//
// 본 refactor 의 목적 (Codex review (RFC-0045) #5 근본 fix):
//
//   기존 controller 의 AnalyzeOverride func(...) 는 ClusterRef 조회 + Secret
//   로드 + mongo connect + collectProfileDocs + convertProfile 5 책임을 *모두*
//   우회. e2e 시 mock 진입점이 분석 로직 위에 위치 → 통합 결합도 검증 불가.
//
//   본 ProfileFetcher 는 *system.profile docs 수집* 단일 책임만. controller
//   는 fetcher.Fetch + insights.Analyze 2-step. 통합 점이 좁아지고, e2e 시
//   FakeProfileFetcher 로 부분 mock 가능 (mongo 부분만 — k8s client 통합은
//   envtest 가 cover).

package insights

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongoclient "github.com/keiailab/mongodb-operator/internal/mongodb"
)

const (
	// 분석 대상에서 제외할 시스템 DB.
	skipDBAdmin  = "admin"
	skipDBLocal  = "local"
	skipDBConfig = "config"

	// defaultAuthDB — 사용자 미지정 시 mongo 인증 DB 기본값 (admin). skipDBAdmin
	// 과 값은 같지만 의미가 분리되므로 별도 상수.
	defaultAuthDB = "admin"

	// clusterKindMongoDB / clusterKindSharded — Insights.Spec.ClusterRef.Kind
	// 가 가질 수 있는 유효 값 두 가지.
	clusterKindMongoDB = "MongoDB"
	clusterKindSharded = "MongoDBSharded"

	defaultConnectTimeout = 10 * time.Second
)

// IsShardedKind — ClusterRef.Kind 가 MongoDBSharded 인지 판정 (controller 분기용
// exported helper — kind 리터럴 중복 회피 + 단일 SSOT).
func IsShardedKind(kind string) bool {
	return kind == clusterKindSharded
}

// ProfileFetcher — system.profile 수집 단일 책임 좁은 interface.
// 호출자는 sampleSize 만 알면 됨. ClusterRef / Secret / mongo connect 는
// 구현체 내부 책임.
type ProfileFetcher interface {
	Fetch(ctx context.Context, sampleSize int32) ([]ProfileDoc, error)
}

// MongoProfileFetcher — 실 MongoDB 클러스터에서 system.profile 수집.
// controller-runtime client.Client 를 통해 ClusterRef 가 가리키는 MongoDB CR
// + credentials Secret 을 조회한 뒤 mongo connect → listDatabases →
// system.profile.find(SetSort+SetLimit) streaming decode → ConvertProfile.
type MongoProfileFetcher struct {
	K8sClient client.Client
	Insights  *mongodbv1alpha1.MongoDBInsights
}

// Fetch — ProfileFetcher 인터페이스 구현.
//
// ROADMAP §3.2 cycle 9 P4: MongoDB + MongoDBSharded kind 둘 다 지원.
// - MongoDB: headless replicaset DNS (`<name>-headless`) + ReplicaSet opt
// - MongoDBSharded: mongos service DNS (`<name>-mongos`) + ReplicaSet="" (router)
// per-shard 직접 connect 는 후속 sub-task (정확도 향상).
func (f *MongoProfileFetcher) Fetch(ctx context.Context, sampleSize int32) ([]ProfileDoc, error) {
	if sampleSize <= 0 {
		sampleSize = 500
	}

	cli, cleanup, err := f.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// ROADMAP §3.2 cycle 9 P3: profiling level 자동 설정. spec 의
	// ProfilingLevel + SlowQueryThresholdMs 를 *각 분석 대상 DB* 에 적용
	// 하여 profile data 진본성 보장. 실패는 *비치명* — 일부 DB 가 권한
	// 부족 / read-only 시 log + 계속 (다른 DB 분석에 무관).
	//
	// Codex re-review (cycle 9 P4) #1 critical fix: mongos 는 profiler enable
	// 불가 (level 1/2 invalid). MongoDBSharded kind 시 skip — applyProfilingLevel
	// 호출이 silent no-op 으로 "적용됨" 오인 방지. per-shard 직접 적용은 후속
	// sub-task (P5).
	if f.Insights.Spec.ClusterRef.Kind == clusterKindMongoDB {
		if err := f.applyProfilingLevel(ctx, cli); err != nil {
			return nil, fmt.Errorf("apply profiling level: %w", err)
		}
	}

	return collectProfileDocs(ctx, cli, sampleSize)
}

// connect — nil 검증(Insights→kind→K8sClient 순) + target 해석 + 자격증명 로드
// + mongo connect 공통 진입. Fetch 와 FetchIndexStats 가 공유.
//
// 검증 순서/error 메시지는 기존 Fetch 계약(fetcher_test.go nil-guard)과 동일하게
// 보존 — 호출자는 반환된 cleanup 을 *반드시* defer 로 실행 (Codex re-review #2 의
// disconnect 전용 context 패턴: reconcile ctx 취소 무관 idempotent cleanup).
func (f *MongoProfileFetcher) connect(ctx context.Context) (*mongo.Client, func(), error) {
	if f.Insights == nil {
		return nil, nil, fmt.Errorf("MongoProfileFetcher.Insights nil")
	}
	// spec-level 검증 우선 (K8sClient 의존 없음).
	kind := f.Insights.Spec.ClusterRef.Kind
	if kind != clusterKindMongoDB && kind != clusterKindSharded {
		return nil, nil, fmt.Errorf("unsupported ClusterRef.Kind %q (allowed %s|%s)", kind, clusterKindMongoDB, clusterKindSharded)
	}
	if f.K8sClient == nil {
		return nil, nil, fmt.Errorf("MongoProfileFetcher.K8sClient nil")
	}

	target, err := f.resolveConnectTarget(ctx)
	if err != nil {
		return nil, nil, err
	}

	return f.connectTo(ctx, target)
}

// connectTo — 주어진 connectTarget 에 자격증명 로드 + mongo connect. connect (단일)
// 와 FetchShardedProfiles (per-shard) 가 공유. cleanup 은 호출자가 defer 실행.
func (f *MongoProfileFetcher) connectTo(ctx context.Context, target connectTarget) (*mongo.Client, func(), error) {
	creds, err := f.loadCredentialsFromSecret(ctx, target.AdminSecretName, target.Namespace)
	if err != nil {
		return nil, nil, err
	}

	cli, err := mongoclient.NewClient(ctx, mongoclient.ConnectOpts{
		Hosts:      []string{target.Host},
		Username:   creds.Username,
		Password:   creds.Password,
		AuthDB:     creds.AuthDB,
		ReplicaSet: target.ReplicaSet,
		Timeout:    defaultConnectTimeout,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("connect %s: %w", target.Host, err)
	}
	cleanup := func() {
		disconnectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = cli.Disconnect(disconnectCtx)
	}
	return cli, cleanup, nil
}

// shardConnectTargets — MongoDBSharded 의 각 shard RS connectTarget 목록 (순수).
// shard RS = <name>-shard-<i>, headless service <name>-shard-<i>-headless,
// i in 0..count-1 (builder.go shard StatefulSet 명명 + 라이브 실측 정합).
func shardConnectTargets(name, ns, adminSecret string, count int) []connectTarget {
	targets := make([]connectTarget, 0, count)
	for i := 0; i < count; i++ {
		targets = append(targets, connectTarget{
			Host:            fmt.Sprintf("%s-shard-%d-headless.%s.svc.cluster.local:27017", name, i, ns),
			ReplicaSet:      fmt.Sprintf("%s-shard-%d", name, i),
			AdminSecretName: adminSecret,
			Namespace:       ns,
		})
	}
	return targets
}

// resolveShardTargets — MongoDBSharded CR 조회 → shardConnectTargets.
func (f *MongoProfileFetcher) resolveShardTargets(ctx context.Context) ([]connectTarget, error) {
	ns := f.Insights.Namespace
	name := f.Insights.Spec.ClusterRef.Name
	mdbsh := &mongodbv1alpha1.MongoDBSharded{}
	if err := f.K8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, mdbsh); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("ClusterRef MongoDBSharded/%s not found", name)
		}
		return nil, fmt.Errorf("get MongoDBSharded: %w", err)
	}
	count := int(mdbsh.Spec.Shards.Count)
	if count <= 0 {
		return nil, fmt.Errorf("MongoDBSharded/%s shards.count=%d (>=1 필요)", name, count)
	}
	return shardConnectTargets(mdbsh.Name, mdbsh.Namespace, mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name, count), nil
}

// FetchShardedProfiles — MongoDBSharded 의 각 shard RS 에 직접 연결하여
// applyProfilingLevel + collectProfileDocs 후 merge (ROADMAP §3.2 per-shard).
// mongos 는 system.profile 비보유 + profiler enable 불가라 per-shard 가 정합.
// sampleSize 는 shard 수로 분배. 일부 shard 실패는 비치명 — 다른 shard 유지하되,
// 전 shard 연결 실패 시 error surface (silent empty 방지).
func (f *MongoProfileFetcher) FetchShardedProfiles(ctx context.Context, sampleSize int32) ([]ProfileDoc, error) {
	if f.Insights == nil {
		return nil, fmt.Errorf("MongoProfileFetcher.Insights nil")
	}
	if f.Insights.Spec.ClusterRef.Kind != clusterKindSharded {
		return nil, fmt.Errorf("FetchShardedProfiles 는 MongoDBSharded kind 전용 (got %q)", f.Insights.Spec.ClusterRef.Kind)
	}
	if f.K8sClient == nil {
		return nil, fmt.Errorf("MongoProfileFetcher.K8sClient nil")
	}
	if sampleSize <= 0 {
		sampleSize = 500
	}
	targets, err := f.resolveShardTargets(ctx)
	if err != nil {
		return nil, err
	}
	perShard := sampleSize / int32(len(targets))
	if perShard < 10 {
		perShard = 10
	}

	var all []ProfileDoc
	var lastErr error
	okCount := 0     // 연결 성공한 shard 수
	collectedOK := 0 // 수집(applyProfilingLevel + collectProfileDocs)까지 성공한 shard 수
	for _, target := range targets {
		cli, cleanup, cerr := f.connectTo(ctx, target)
		if cerr != nil {
			lastErr = cerr
			continue
		}
		okCount++
		if perr := f.applyProfilingLevel(ctx, cli); perr != nil {
			lastErr = perr
		} else if docs, derr := collectProfileDocs(ctx, cli, perShard); derr != nil {
			lastErr = derr
		} else {
			all = append(all, docs...)
			collectedOK++
		}
		cleanup()
	}
	// 전 shard 가 수집 실패 (연결조차 0 또는 연결됐으나 수집 0건) → silent empty 방지로 error surface.
	if collectedOK == 0 && lastErr != nil {
		return nil, fmt.Errorf("all %d shards failed (connected=%d): %w", len(targets), okCount, lastErr)
	}
	// 부분 실패 (일부 shard 만 수집 성공) → 수집된 결과 + 경고 error 동반.
	// 호출자가 '정상이지만 빈/불완전 결과' 로 오인하지 않도록 표면화한다. 결과 슬라이스를
	// 함께 반환하므로 호출자는 partial 데이터를 활용하거나(비치명 처리) 재시도를 결정할 수 있다.
	if collectedOK < len(targets) && lastErr != nil {
		return all, fmt.Errorf("partial shard failure (%d/%d shards collected): %w", collectedOK, len(targets), lastErr)
	}
	return all, nil
}

// FetchIndexStats — 분석 대상 DB 의 모든 collection 에 대해 $indexStats 수집
// (ROADMAP §3.2 UnusedIndex). AnalyzeIndexUsage 입력.
//
// Fetch 와 별도 connection — analysis 는 AnalysisInterval(기본 15분) 주기라
// 추가 connect 비용 무시 가능 (Simplicity > 단일 connection 최적화). $indexStats
// 는 mongos 에서도 동작하므로 MongoDB + MongoDBSharded 양 kind 지원.
func (f *MongoProfileFetcher) FetchIndexStats(ctx context.Context) ([]IndexStat, error) {
	cli, cleanup, err := f.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return collectIndexStats(ctx, cli)
}

// applyProfilingLevel — listDatabases → 각 분석 대상 DB 의 profile level 을
// Spec.ProfilingLevel 로 설정. slowms = Spec.SlowQueryThresholdMs.
//
// ROADMAP §3.2 cycle 9 P3. profile 데이터 진본성 보장 — 분석 직전 매번 적용
// (재기동/관리자 수동 변경 후 자동 회복 + idempotent).
//
// per-DB error 는 *log only*. 권한 부족 / read-only / 시스템 DB 적용 거부는
// 다른 DB 분석에 무관. listDatabases 자체 실패만 surface.
func (f *MongoProfileFetcher) applyProfilingLevel(ctx context.Context, cli *mongo.Client) error {
	level := int(f.Insights.Spec.ProfilingLevel)
	if level < 0 || level > 2 {
		// CRD enum 이 0|1|2 만 허용 — defensive guard.
		return fmt.Errorf("invalid ProfilingLevel %d (allowed 0|1|2)", level)
	}
	slowMs := int(f.Insights.Spec.SlowQueryThresholdMs)
	if slowMs <= 0 {
		slowMs = 100
	}

	dbNames, err := cli.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return fmt.Errorf("listDatabases: %w", err)
	}
	for _, n := range dbNames {
		switch n {
		case skipDBAdmin, skipDBLocal, skipDBConfig:
			continue
		}
		cmd := bson.D{
			{Key: "profile", Value: level},
			{Key: "slowms", Value: slowMs},
		}
		// RunCommand 결과 무시 — per-DB 실패는 log only (mongo log).
		_ = cli.Database(n).RunCommand(ctx, cmd).Err()
	}
	return nil
}

// connectTarget — kind 분기 결과 모음.
type connectTarget struct {
	Host            string // <name>-{headless|mongos}.<ns>.svc.cluster.local:27017
	ReplicaSet      string // MongoDB kind 만 set, MongoDBSharded 는 빈 문자열 (router)
	AdminSecretName string // Auth.AdminCredentialsSecretRef.Name (fallback)
	Namespace       string // CR namespace (Insights.Namespace 와 동일하지만 명시)
}

// resolveConnectTarget — ClusterRef.Kind 분기 → connectTarget.
// cycle 9 P4: MongoDB headless / MongoDBSharded mongos service.
func (f *MongoProfileFetcher) resolveConnectTarget(ctx context.Context) (connectTarget, error) {
	ns := f.Insights.Namespace
	name := f.Insights.Spec.ClusterRef.Name
	switch f.Insights.Spec.ClusterRef.Kind {
	case clusterKindMongoDB:
		mdb := &mongodbv1alpha1.MongoDB{}
		if err := f.K8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, mdb); err != nil {
			if apierrors.IsNotFound(err) {
				return connectTarget{}, fmt.Errorf("ClusterRef MongoDB/%s not found", name)
			}
			return connectTarget{}, fmt.Errorf("get MongoDB: %w", err)
		}
		return connectTarget{
			Host:            fmt.Sprintf("%s-headless.%s.svc.cluster.local:27017", mdb.Name, mdb.Namespace),
			ReplicaSet:      mdb.Name,
			AdminSecretName: mdb.Spec.Auth.AdminCredentialsSecretRef.Name,
			Namespace:       mdb.Namespace,
		}, nil
	case clusterKindSharded:
		mdbsh := &mongodbv1alpha1.MongoDBSharded{}
		if err := f.K8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, mdbsh); err != nil {
			if apierrors.IsNotFound(err) {
				return connectTarget{}, fmt.Errorf("ClusterRef MongoDBSharded/%s not found", name)
			}
			return connectTarget{}, fmt.Errorf("get MongoDBSharded: %w", err)
		}
		// BuildMongosService 정합: <name>-mongos, ClusterIP. router → ReplicaSet="" .
		return connectTarget{
			Host:            fmt.Sprintf("%s-mongos.%s.svc.cluster.local:27017", mdbsh.Name, mdbsh.Namespace),
			ReplicaSet:      "",
			AdminSecretName: mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name,
			Namespace:       mdbsh.Namespace,
		}, nil
	}
	return connectTarget{}, fmt.Errorf("unreachable: kind validation should have caught %q", f.Insights.Spec.ClusterRef.Kind)
}

// analysisCredentials — mongo connect 자격증명.
type analysisCredentials struct {
	Username string
	Password string
	AuthDB   string
}

// loadCredentialsFromSecret — Spec.AnalysisCredentialsSecretRef 우선,
// 미설정 시 fallbackSecretName 재사용 (보통 cluster 의 admin secret).
//
// Codex review (RFC-0045) #5 fix: secret 의 username + authDB (alias
// authSource) 를 우선 읽고, ref 미설정 시에만 admin/admin fallback.
// cycle 9 P4: MongoDB / MongoDBSharded 공통 path 위해 fallbackSecretName
// 파라미터화. ns 는 Insights namespace.
func (f *MongoProfileFetcher) loadCredentialsFromSecret(ctx context.Context, fallbackSecretName, ns string) (analysisCredentials, error) {
	custom := f.Insights.Spec.AnalysisCredentialsSecretRef != nil && f.Insights.Spec.AnalysisCredentialsSecretRef.Name != ""
	var name string
	if custom {
		name = f.Insights.Spec.AnalysisCredentialsSecretRef.Name
	} else {
		name = fallbackSecretName
	}
	if name == "" {
		return analysisCredentials{}, fmt.Errorf("no credentials secret resolvable for analysis")
	}
	sec := &corev1.Secret{}
	if err := f.K8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, sec); err != nil {
		return analysisCredentials{}, fmt.Errorf("get secret %s: %w", name, err)
	}
	pw, ok := sec.Data["password"]
	if !ok {
		return analysisCredentials{}, fmt.Errorf("secret %s missing 'password' key", name)
	}
	creds := analysisCredentials{Password: string(pw)}
	if u, ok := sec.Data["username"]; ok && len(u) > 0 {
		creds.Username = string(u)
	}
	if a, ok := sec.Data["authDB"]; ok && len(a) > 0 {
		creds.AuthDB = string(a)
	} else if a, ok := sec.Data["authSource"]; ok && len(a) > 0 {
		creds.AuthDB = string(a)
	}
	if !custom {
		if creds.Username == "" {
			creds.Username = defaultAuthDB
		}
		if creds.AuthDB == "" {
			creds.AuthDB = defaultAuthDB
		}
	} else {
		if creds.Username == "" {
			return analysisCredentials{}, fmt.Errorf("secret %s missing 'username' key (required for AnalysisCredentialsSecretRef)", name)
		}
		if creds.AuthDB == "" {
			// custom secret 인데 authDB 미설정 시 admin fallback.
			creds.AuthDB = defaultAuthDB
		}
	}
	return creds, nil
}

// collectProfileDocs — listDatabases → 각 DB 의 system.profile 에서
// SetSort(ts:-1) + SetLimit 으로 최신 sampleSize 문서 수집
// (admin/local/config 제외). Streaming decode + global cap.
func collectProfileDocs(ctx context.Context, cli *mongo.Client, sampleSize int32) ([]ProfileDoc, error) {
	dbNames, err := cli.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("listDatabases: %w", err)
	}

	// 분석 대상 DB 만 carve out.
	targetDBs := make([]string, 0, len(dbNames))
	for _, n := range dbNames {
		switch n {
		case skipDBAdmin, skipDBLocal, skipDBConfig:
			continue
		}
		targetDBs = append(targetDBs, n)
	}

	perDBLimit := int64(sampleSize)
	if len(targetDBs) > 0 {
		share := int64(sampleSize) / int64(len(targetDBs))
		if share < 10 {
			share = 10
		}
		perDBLimit = share
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "ts", Value: -1}}).
		SetLimit(perDBLimit)

	var all []ProfileDoc
	globalCap := int(sampleSize)
	for _, dbName := range targetDBs {
		coll := cli.Database(dbName).Collection("system.profile")
		cursor, err := coll.Find(ctx, bson.D{}, findOpts)
		if err != nil {
			// Codex re-review #3 fix: profiling 비활성 / namespace not found
			// 만 skip, 그 외 auth/network/context error 는 surface.
			if isProfileAbsent(err) {
				continue
			}
			return nil, fmt.Errorf("find system.profile on %s: %w", dbName, err)
		}
		for cursor.Next(ctx) {
			if len(all) >= globalCap {
				break
			}
			var m bson.M
			if decErr := cursor.Decode(&m); decErr != nil {
				continue
			}
			all = append(all, ConvertProfile(m))
		}
		// Codex re-review #3 fix: cursor.Err() 검사 — iteration 중 발생한
		// network/server error 가 Ready+0 docs 로 silent 잠재 차단.
		if cerr := cursor.Err(); cerr != nil {
			_ = cursor.Close(ctx)
			return nil, fmt.Errorf("cursor on %s.system.profile: %w", dbName, cerr)
		}
		_ = cursor.Close(ctx)
		if len(all) >= globalCap {
			break
		}
	}
	return all, nil
}

// collectIndexStats — listDatabases → 각 분석 대상 DB 의 collection 마다
// $indexStats aggregation → IndexStat. admin/local/config + system.* 제외.
//
// $indexStats 는 collection 별 인덱스 접근 카운터(accesses.ops)를 반환 — 0 이면
// 미사용 인덱스 후보 (AnalyzeIndexUsage 가 _id_ 제외 후 판정). collection drop
// race (NamespaceNotFound code 26) 는 skip, 그 외 error 는 surface.
// mongos 에서도 동작 ($indexStats 가 shard 별 카운터 aggregate).
func collectIndexStats(ctx context.Context, cli *mongo.Client) ([]IndexStat, error) {
	dbNames, err := cli.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("listDatabases: %w", err)
	}

	var all []IndexStat
	for _, dbName := range dbNames {
		switch dbName {
		case skipDBAdmin, skipDBLocal, skipDBConfig:
			continue
		}
		db := cli.Database(dbName)
		collNames, err := db.ListCollectionNames(ctx, bson.D{})
		if err != nil {
			return nil, fmt.Errorf("listCollections on %s: %w", dbName, err)
		}
		for _, collName := range collNames {
			if strings.HasPrefix(collName, "system.") {
				continue
			}
			ns := dbName + "." + collName
			cursor, err := db.Collection(collName).Aggregate(ctx, mongo.Pipeline{
				{{Key: "$indexStats", Value: bson.D{}}},
			})
			if err != nil {
				// isProfileAbsent 재사용 — code 26 (NamespaceNotFound) 만 skip.
				if isProfileAbsent(err) {
					continue
				}
				return nil, fmt.Errorf("$indexStats on %s: %w", ns, err)
			}
			for cursor.Next(ctx) {
				var m bson.M
				if decErr := cursor.Decode(&m); decErr != nil {
					continue
				}
				all = append(all, ConvertIndexStat(ns, m))
			}
			if cerr := cursor.Err(); cerr != nil {
				_ = cursor.Close(ctx)
				return nil, fmt.Errorf("cursor $indexStats on %s: %w", ns, cerr)
			}
			_ = cursor.Close(ctx)
		}
	}
	return all, nil
}

// isProfileAbsent — err 가 *namespace not found* / *collection doesn't exist*
// 류만 true. 다른 error (auth, network, context cancellation) 는 surface.
//
// mongo v2 driver 는 NamespaceNotFound (server error code 26) + 종종 error
// 메시지 substring 으로 노출. 본 helper 는 *문자열 패턴* 보수적 매칭.
// isProfileAbsent — mongo CommandError code 26 (NamespaceNotFound) 만 absent
// 로 인정. 그 외 (auth, network, command not supported, routing) 는 surface.
//
// Codex re-review (cycle 9 P4) #3 major fix: 이전 구현이 error 메시지 substring
// 만 봐서 sharded/mongos 의 unsupported/auth/routing error 가 namespace 를
// 포함하면 silent skip → "성공 + 0 docs" 함정. 본 fix 는 mongo.CommandError
// type assertion + code 26 정밀 매칭.
//
// fallback: mongo CommandError 가 아닌 error (driver 내부 wrap 또는 mongo-driver
// v2 의 다른 error type) 는 namespace not found 의 진본 substring marker
// 만 보수적으로 매칭.
func isProfileAbsent(err error) bool {
	if err == nil {
		return false
	}
	// 진본 mongo CommandError code 26 → NamespaceNotFound.
	var cmdErr mongo.CommandError
	if errors.As(err, &cmdErr) {
		return cmdErr.Code == 26 // NamespaceNotFound
	}
	// driver wrapping 시 정확한 type 추출 실패할 수 있어 marker fallback.
	// "system.profile" 단독 marker 는 제거 — auth/routing error 가 namespace
	// 를 포함 시 silent miss 함정 (Codex #3).
	lower := strings.ToLower(err.Error())
	for _, marker := range []string{
		"ns not found",
		"namespacenotfound",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
