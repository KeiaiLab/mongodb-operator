/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0.
*/

// fetcher_test.go — ProfileFetcher 인터페이스 + MongoProfileFetcher 의
// nil-guard 검증. 실 mongo connect path 는 envtest 가 cover (controller suite).

package insights

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// FakeProfileFetcher — 테스트 용 ProfileFetcher 구현. fetcher.Fetch + Analyze
// 의 통합 path 를 mongo 의존성 없이 검증.
type FakeProfileFetcher struct {
	Docs []ProfileDoc
	Err  error
}

func (f *FakeProfileFetcher) Fetch(_ context.Context, _ int32) ([]ProfileDoc, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Docs, nil
}

func TestProfileFetcher_InterfaceContract(t *testing.T) {
	// FakeProfileFetcher 가 ProfileFetcher 인터페이스를 구현하는지 컴파일 타임
	// 보장 — interface 누락 시 본 라인이 컴파일 실패.
	var _ ProfileFetcher = (*FakeProfileFetcher)(nil)
	var _ ProfileFetcher = (*MongoProfileFetcher)(nil)
}

func TestFakeFetcher_ReturnsSeededDocs(t *testing.T) {
	docs := []ProfileDoc{
		{Op: "query", NS: "app.users", Millis: 50, PlanSummary: "IXSCAN", DocsExamined: 1, NReturned: 1},
	}
	f := &FakeProfileFetcher{Docs: docs}
	got, err := f.Fetch(context.Background(), 100)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 || got[0].NS != "app.users" {
		t.Errorf("seeded docs 반환 실패, got %+v", got)
	}
}

func TestFakeFetcher_PropagatesError(t *testing.T) {
	want := errors.New("simulated mongo connect error")
	f := &FakeProfileFetcher{Err: want}
	_, err := f.Fetch(context.Background(), 100)
	if !errors.Is(err, want) {
		t.Errorf("err propagation 실패, got %v want %v", err, want)
	}
}

func TestMongoProfileFetcher_NilGuards(t *testing.T) {
	// Insights nil
	f := &MongoProfileFetcher{}
	_, err := f.Fetch(context.Background(), 100)
	if err == nil {
		t.Errorf("Insights nil 시 error 기대")
	}

	// K8sClient nil + valid Kind
	f2 := &MongoProfileFetcher{
		Insights: &mongodbv1alpha1.MongoDBInsights{
			ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
			Spec: mongodbv1alpha1.MongoDBInsightsSpec{
				ClusterRef: mongodbv1alpha1.ClusterReference{Name: "c", Kind: "MongoDB"},
			},
		},
	}
	_, err = f2.Fetch(context.Background(), 100)
	if err == nil {
		t.Errorf("K8sClient nil 시 error 기대")
	}
}

// cycle 9 P4: MongoDBSharded kind 가 더 이상 "unsupported" 로 reject 되지
// 않음. K8sClient nil 단계에서 reject (실 cluster 조회 시도 직전).
func TestMongoProfileFetcher_AcceptsShardedKindAndFailsOnNilClient(t *testing.T) {
	f := &MongoProfileFetcher{
		Insights: &mongodbv1alpha1.MongoDBInsights{
			ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
			Spec: mongodbv1alpha1.MongoDBInsightsSpec{
				ClusterRef: mongodbv1alpha1.ClusterReference{Name: "cluster1", Kind: "MongoDBSharded"},
			},
		},
		// K8sClient nil
	}
	_, err := f.Fetch(context.Background(), 100)
	if err == nil {
		t.Fatalf("K8sClient nil 시 error 기대")
	}
	if !strings.Contains(err.Error(), "K8sClient nil") {
		t.Errorf("error 가 K8sClient nil 표시 기대, got %q", err.Error())
	}
}

// cycle 9 P4: MongoDB / MongoDBSharded 외 kind 는 unsupported 로 fail-fast.
func TestMongoProfileFetcher_RejectsUnknownKind(t *testing.T) {
	f := &MongoProfileFetcher{
		Insights: &mongodbv1alpha1.MongoDBInsights{
			ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
			Spec: mongodbv1alpha1.MongoDBInsightsSpec{
				ClusterRef: mongodbv1alpha1.ClusterReference{Name: "c", Kind: "Postgres"},
			},
		},
	}
	_, err := f.Fetch(context.Background(), 100)
	if err == nil {
		t.Fatalf("unknown kind 시 error 기대")
	}
	if !strings.Contains(err.Error(), "unsupported ClusterRef.Kind") {
		t.Errorf("error 가 unsupported kind 표시 기대, got %q", err.Error())
	}
}

// FetchIndexStats (UnusedIndex 통합) 도 Fetch 와 동일한 connect nil-guard 계약을
// 공유함을 검증 — shared connect() helper 가 양 진입점에 wiring 됐는지 회귀 가드.
func TestMongoProfileFetcher_FetchIndexStats_NilGuards(t *testing.T) {
	// Insights nil → error.
	f := &MongoProfileFetcher{}
	if _, err := f.FetchIndexStats(context.Background()); err == nil {
		t.Errorf("Insights nil 시 error 기대")
	}

	// 유효 kind + K8sClient nil → "K8sClient nil" error (kind 검증 통과 후).
	f2 := &MongoProfileFetcher{
		Insights: &mongodbv1alpha1.MongoDBInsights{
			ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
			Spec: mongodbv1alpha1.MongoDBInsightsSpec{
				ClusterRef: mongodbv1alpha1.ClusterReference{Name: "c", Kind: "MongoDB"},
			},
		},
	}
	_, err := f2.FetchIndexStats(context.Background())
	if err == nil || !strings.Contains(err.Error(), "K8sClient nil") {
		t.Errorf("K8sClient nil error 기대, got %v", err)
	}
}

// --- sharded per-shard 프로파일링 (ROADMAP §3.2) ---

func TestShardConnectTargets(t *testing.T) {
	targets := shardConnectTargets("mdb", "data", "mdb-admin", 3)
	if len(targets) != 3 {
		t.Fatalf("3 shard targets 기대, got %d", len(targets))
	}
	if targets[0].Host != "mdb-shard-0-headless.data.svc.cluster.local:27017" {
		t.Errorf("shard-0 host 불일치: %s", targets[0].Host)
	}
	if targets[0].ReplicaSet != "mdb-shard-0" {
		t.Errorf("shard-0 RS 불일치: %s", targets[0].ReplicaSet)
	}
	if targets[0].AdminSecretName != "mdb-admin" || targets[0].Namespace != "data" {
		t.Errorf("shard-0 secret/ns 불일치: %+v", targets[0])
	}
	if targets[2].ReplicaSet != "mdb-shard-2" {
		t.Errorf("shard-2 RS 불일치: %s", targets[2].ReplicaSet)
	}
}

func TestShardConnectTargets_ZeroCount(t *testing.T) {
	if got := shardConnectTargets("x", "ns", "s", 0); len(got) != 0 {
		t.Errorf("count=0 → 빈 목록 기대, got %d", len(got))
	}
}

func TestMongoProfileFetcher_FetchShardedProfiles_Guards(t *testing.T) {
	// Insights nil → error.
	if _, err := (&MongoProfileFetcher{}).FetchShardedProfiles(context.Background(), 100); err == nil {
		t.Errorf("Insights nil 시 error 기대")
	}

	// MongoDB kind (sharded 아님) → 전용 error.
	fMongo := &MongoProfileFetcher{
		Insights: &mongodbv1alpha1.MongoDBInsights{
			ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
			Spec: mongodbv1alpha1.MongoDBInsightsSpec{
				ClusterRef: mongodbv1alpha1.ClusterReference{Name: "c", Kind: "MongoDB"},
			},
		},
	}
	if _, err := fMongo.FetchShardedProfiles(context.Background(), 100); err == nil || !strings.Contains(err.Error(), "MongoDBSharded kind 전용") {
		t.Errorf("MongoDB kind → 'MongoDBSharded kind 전용' error 기대, got %v", err)
	}

	// Sharded kind + K8sClient nil → K8sClient nil error.
	fSharded := &MongoProfileFetcher{
		Insights: &mongodbv1alpha1.MongoDBInsights{
			ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
			Spec: mongodbv1alpha1.MongoDBInsightsSpec{
				ClusterRef: mongodbv1alpha1.ClusterReference{Name: "c", Kind: "MongoDBSharded"},
			},
		},
	}
	if _, err := fSharded.FetchShardedProfiles(context.Background(), 100); err == nil || !strings.Contains(err.Error(), "K8sClient nil") {
		t.Errorf("K8sClient nil error 기대, got %v", err)
	}
}
