/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
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

	defaultConnectTimeout = 10 * time.Second
)

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
func (f *MongoProfileFetcher) Fetch(ctx context.Context, sampleSize int32) ([]ProfileDoc, error) {
	if f.Insights == nil {
		return nil, fmt.Errorf("MongoProfileFetcher.Insights nil")
	}
	// spec-level 검증 우선 (K8sClient 의존 없음).
	if f.Insights.Spec.ClusterRef.Kind != "MongoDB" {
		// MongoDBSharded 는 후속 sub-task.
		return nil, fmt.Errorf("unsupported ClusterRef.Kind %q (only MongoDB in this cycle)", f.Insights.Spec.ClusterRef.Kind)
	}
	if f.K8sClient == nil {
		return nil, fmt.Errorf("MongoProfileFetcher.K8sClient nil")
	}
	if sampleSize <= 0 {
		sampleSize = 500
	}

	mdb := &mongodbv1alpha1.MongoDB{}
	if err := f.K8sClient.Get(ctx, types.NamespacedName{
		Name:      f.Insights.Spec.ClusterRef.Name,
		Namespace: f.Insights.Namespace,
	}, mdb); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("ClusterRef MongoDB/%s not found", f.Insights.Spec.ClusterRef.Name)
		}
		return nil, fmt.Errorf("get MongoDB: %w", err)
	}

	creds, err := f.loadCredentials(ctx, mdb)
	if err != nil {
		return nil, err
	}

	host := fmt.Sprintf("%s-headless.%s.svc.cluster.local:27017", mdb.Name, mdb.Namespace)
	cli, err := mongoclient.NewClient(ctx, mongoclient.ConnectOpts{
		Hosts:      []string{host},
		Username:   creds.Username,
		Password:   creds.Password,
		AuthDB:     creds.AuthDB,
		ReplicaSet: mdb.Name,
		Timeout:    defaultConnectTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", host, err)
	}
	// Codex re-review #2 fix: disconnect 전용 context. reconcile ctx 가 이미
	// canceled/timeout 인 경우에도 cleanup 보장 — context.Background() + 짧은
	// timeout. 누수 방지 + idempotent.
	defer func() {
		disconnectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = cli.Disconnect(disconnectCtx)
	}()

	// ROADMAP §3.2 cycle 9 P3: profiling level 자동 설정. spec 의
	// ProfilingLevel + SlowQueryThresholdMs 를 *각 분석 대상 DB* 에 적용
	// 하여 profile data 진본성 보장. 실패는 *비치명* — 일부 DB 가 권한
	// 부족 / read-only 시 log + 계속 (다른 DB 분석에 무관).
	if err := f.applyProfilingLevel(ctx, cli); err != nil {
		// applyProfilingLevel 자체 fatal error 만 surface (listDatabases 실패).
		// per-DB profile 적용 실패는 내부에서 log only + 계속.
		return nil, fmt.Errorf("apply profiling level: %w", err)
	}

	return collectProfileDocs(ctx, cli, sampleSize)
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

// analysisCredentials — mongo connect 자격증명.
type analysisCredentials struct {
	Username string
	Password string
	AuthDB   string
}

// loadCredentials — Spec.AnalysisCredentialsSecretRef 우선,
// 미설정 시 cluster 의 Auth.AdminCredentialsSecretRef 재사용.
//
// Codex review (RFC-0045) #5 fix: secret 의 username + authDB (alias
// authSource) 를 우선 읽고, ref 미설정 시에만 admin/admin fallback.
func (f *MongoProfileFetcher) loadCredentials(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (analysisCredentials, error) {
	custom := f.Insights.Spec.AnalysisCredentialsSecretRef != nil && f.Insights.Spec.AnalysisCredentialsSecretRef.Name != ""
	var name string
	if custom {
		name = f.Insights.Spec.AnalysisCredentialsSecretRef.Name
	} else {
		name = mdb.Spec.Auth.AdminCredentialsSecretRef.Name
	}
	if name == "" {
		return analysisCredentials{}, fmt.Errorf("no credentials secret resolvable for analysis")
	}
	sec := &corev1.Secret{}
	if err := f.K8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: f.Insights.Namespace}, sec); err != nil {
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
			creds.Username = "admin"
		}
		if creds.AuthDB == "" {
			creds.AuthDB = "admin"
		}
	} else {
		if creds.Username == "" {
			return analysisCredentials{}, fmt.Errorf("secret %s missing 'username' key (required for AnalysisCredentialsSecretRef)", name)
		}
		if creds.AuthDB == "" {
			// custom secret 인데 authDB 미설정 시 admin fallback.
			creds.AuthDB = "admin"
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

// isProfileAbsent — err 가 *namespace not found* / *collection doesn't exist*
// 류만 true. 다른 error (auth, network, context cancellation) 는 surface.
//
// mongo v2 driver 는 NamespaceNotFound (server error code 26) + 종종 error
// 메시지 substring 으로 노출. 본 helper 는 *문자열 패턴* 보수적 매칭.
func isProfileAbsent(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	for _, marker := range []string{
		"ns not found",
		"namespacenotfound",
		"system.profile",
		"profiling is off",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
