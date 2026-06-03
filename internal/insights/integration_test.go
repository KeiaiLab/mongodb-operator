//go:build integration

/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0.
*/

// integration_test.go — 실 MongoDB 대상 insights round-trip 검증 (opt-in).
//
// 본 테스트는 `go test -tags integration ./internal/insights/...` 로만 실행되며
// (CI 기본 `go test ./...` 에서 제외 — build tag), 실 mongod 가 필요하다. 환경에서
// mongo 미가용 시 t.Skip 으로 graceful degrade.
//
// 검증 대상 (단위 테스트가 mongo 없이 가정한 BSON shape 를 *실측*):
//   - collectIndexStats → ConvertIndexStat: $indexStats 의 accesses.ops (BSON Long)
//     이 ReadInt64Any 로 올바르게 0 으로 디코드되어 UnusedIndex 검출되는지
//   - collectProfileDocs → ConvertProfile: system.profile 의 COLLSCAN / docsExamined
//     / nreturned 가 ProfileDoc 로 정확히 매핑되어 MissingIndex 검출되는지
//
// 실행 예:
//   docker run -d --name mdbtest -p 27077:27017 mongo:8.0
//   INSIGHTS_TEST_MONGO_HOST=localhost:27077 go test -tags integration ./internal/insights/...

package insights

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	mongoclient "github.com/keiailab/mongodb-operator/internal/mongodb"
)

func testMongoHost() string {
	if h := os.Getenv("INSIGHTS_TEST_MONGO_HOST"); h != "" {
		return h
	}
	return "localhost:27077"
}

// connectTestMongo — standalone mongo 직접 연결. 미가용 시 skip.
func connectTestMongo(t *testing.T) *mongo.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cli, err := mongoclient.NewClient(ctx, mongoclient.ConnectOpts{
		Hosts:   []string{testMongoHost()},
		Direct:  true,
		Timeout: 8 * time.Second,
	})
	if err != nil {
		t.Skipf("mongo 미가용 (%s): %v — 통합 테스트 skip", testMongoHost(), err)
	}
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := cli.Ping(pingCtx, nil); err != nil {
		_ = cli.Disconnect(context.Background())
		t.Skipf("mongo ping 실패 (%s): %v — 통합 테스트 skip", testMongoHost(), err)
	}
	return cli
}

// TestIntegration_CollectIndexStats_DetectsUnusedIndex — 실 $indexStats 로
// UnusedIndex 검출 (accesses.ops BSON Long → int64 0 디코드 검증).
func TestIntegration_CollectIndexStats_DetectsUnusedIndex(t *testing.T) {
	cli := connectTestMongo(t)
	defer func() { _ = cli.Disconnect(context.Background()) }()

	ctx := context.Background()
	dbName := "insights_itest_idx"
	coll := "users"
	db := cli.Database(dbName)
	t.Cleanup(func() { _ = db.Drop(context.Background()) })
	_ = db.Drop(ctx) // 이전 잔재 제거

	if _, err := db.Collection(coll).InsertMany(ctx, []any{
		bson.M{"a": 1, "email": "x@y.z"},
		bson.M{"a": 2, "email": "p@q.r"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// 사용되지 않을 인덱스 생성 (accesses.ops 가 0 으로 남음).
	if _, err := db.Collection(coll).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "email", Value: 1}},
	}); err != nil {
		t.Fatalf("createIndex: %v", err)
	}

	stats, err := collectIndexStats(ctx, cli)
	if err != nil {
		t.Fatalf("collectIndexStats: %v", err)
	}

	var foundEmail bool
	for _, s := range stats {
		if s.NS == dbName+"."+coll && s.IndexName == "email_1" {
			foundEmail = true
			if s.Accesses != 0 {
				t.Errorf("email_1 accesses=0 기대 (미사용), got %d", s.Accesses)
			}
		}
	}
	if !foundEmail {
		t.Fatalf("email_1 인덱스 stat 미수집, got %+v", stats)
	}

	// AnalyzeIndexUsage 가 실 stat 으로 UnusedIndex 권장 생성하는지.
	recs := AnalyzeIndexUsage(stats)
	var unusedForEmail bool
	for _, r := range recs {
		if r.Type == RecTypeUnusedIndex && strings.Contains(r.Detail, "email_1") {
			unusedForEmail = true
		}
	}
	if !unusedForEmail {
		t.Errorf("email_1 UnusedIndex 권장 기대, got %+v", recs)
	}
}

// TestIntegration_CollectProfileDocs_DetectsCollscan — 실 system.profile 로
// COLLSCAN → MissingIndex 검출 (ProfileDoc 매핑 검증).
func TestIntegration_CollectProfileDocs_DetectsCollscan(t *testing.T) {
	cli := connectTestMongo(t)
	defer func() { _ = cli.Disconnect(context.Background()) }()

	ctx := context.Background()
	dbName := "insights_itest_prof"
	coll := "events"
	db := cli.Database(dbName)
	t.Cleanup(func() {
		_ = db.RunCommand(context.Background(), bson.D{{Key: "profile", Value: 0}}).Err()
		_ = db.Drop(context.Background())
	})
	_ = db.Drop(ctx)

	docs := make([]any, 0, 50)
	for i := 0; i < 50; i++ {
		docs = append(docs, bson.M{"severity": "error", "n": i})
	}
	if _, err := db.Collection(coll).InsertMany(ctx, docs); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// 전체 profiling 활성 (slowms=0).
	if err := db.RunCommand(ctx, bson.D{{Key: "profile", Value: 2}, {Key: "slowms", Value: 0}}).Err(); err != nil {
		t.Fatalf("setProfilingLevel: %v", err)
	}
	// COLLSCAN 유발 query (인덱스 없는 필드).
	for i := 0; i < 3; i++ {
		cur, err := db.Collection(coll).Find(ctx, bson.M{"unindexed": "zzz"})
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		_ = cur.All(ctx, &[]bson.M{})
	}

	profileDocs, err := collectProfileDocs(ctx, cli, 500)
	if err != nil {
		t.Fatalf("collectProfileDocs: %v", err)
	}
	if len(profileDocs) == 0 {
		t.Fatalf("profile docs 0건 — profiling/수집 경로 확인 필요")
	}

	var collscanSeen bool
	for _, d := range profileDocs {
		if d.NS == dbName+"."+coll && d.PlanSummary == planSummaryCollscan {
			collscanSeen = true
		}
	}
	if !collscanSeen {
		t.Errorf("COLLSCAN profile doc 기대, got %d docs (planSummary 매핑 확인)", len(profileDocs))
	}

	// Analyze 가 실 profile docs 로 MissingIndex 검출하는지.
	recs := Analyze(profileDocs, 0)
	var missingSeen bool
	for _, r := range recs {
		if r.Type == RecTypeMissingIndex {
			missingSeen = true
		}
	}
	if !missingSeen {
		t.Errorf("MissingIndex 권장 기대, got %+v", recs)
	}
}
