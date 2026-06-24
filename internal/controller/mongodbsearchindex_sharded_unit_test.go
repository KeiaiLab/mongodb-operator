/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mongodbsearchindex_sharded_unit_test.go — PR4: Sharded source 의 search index reconcile 회귀
// 가드. fake client + fake searchIndexOps 주입. sharded source 면 인덱스 명령을 mongos 경유로
// 실행한다(개별 shard 아님 — mongos 가 per-shard mongot 전파). readiness gate 는 MongoDBSharded
// Status.Phase=Running 기준.
package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
	"github.com/keiailab/mongodb-operator/internal/mongodb"
)

// siShardedTestEnv — sharded source 기반 search index 테스트 fixture. shardedRunning=true 면
// MongoDBSharded Status.Phase=Running(readiness gate 통과). admin secret 은 <name>-admin.
func siShardedTestEnv(t *testing.T, ops *fakeSearchIndexOps, idx *mongodbv1beta1.MongoDBSearchIndex, shardedRunning bool) *MongoDBSearchIndexReconciler {
	t.Helper()
	s := newTestScheme(t)
	const shName = "ksh"
	mdbsh := newShardedForSearch(shName)
	if shardedRunning {
		mdbsh.Status.Phase = mongodbv1alpha1.ShardedPhaseRunning
	}
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: "srch", Namespace: "default"},
		Spec: mongodbv1beta1.MongoDBSearchSpec{
			Source: mongodbv1beta1.SearchSource{MongoDBResourceRef: &corev1.LocalObjectReference{Name: shName}, Kind: "MongoDBSharded"},
		},
	}
	search.Status.Phase = searchPhaseReady
	adminSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: shName + "-admin", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("pw")},
	}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(mdbsh, search, adminSecret, idx).
		WithStatusSubresource(mdbsh, search, idx).
		Build()
	return &MongoDBSearchIndexReconciler{
		Client: cl, Scheme: s,
		NewManager: func(_, _, _, _, _ string) searchIndexOps { return ops },
	}
}

// TestSearchIndexReconcile_ShardedCreatesViaMongos — sharded source(Running) + 인덱스 부재 →
// mongos 경유 Create 호출 + Phase=Building. (이전엔 "Sharded not yet supported" Failed reject 였음.)
func TestSearchIndexReconcile_ShardedCreatesViaMongos(t *testing.T) {
	f := &fakeSearchIndexOps{listResult: nil} // 부재
	r := siShardedTestEnv(t, f, newTestSearchIndex(), true)
	reconcileIdx(t, r)

	if !f.createCalled {
		t.Error("sharded source 인덱스 부재 시 Create 미호출 (mongos 경유 생성 기대)")
	}
	got := &mongodbv1beta1.MongoDBSearchIndex{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "idx", Namespace: "default"}, got)
	if got.Status.Phase != siPhaseBuilding {
		t.Errorf("Phase=%q want Building(생성 직후)", got.Status.Phase)
	}
	if len(got.Finalizers) == 0 {
		t.Error("finalizer 미부착")
	}
}

// TestSearchIndexReconcile_ShardedReadyWhenQueryable — sharded source + 인덱스 READY+queryable →
// Phase=Ready, Create 미호출.
func TestSearchIndexReconcile_ShardedReadyWhenQueryable(t *testing.T) {
	f := &fakeSearchIndexOps{listResult: []mongodb.SearchIndexInfo{{ID: "sx", Name: "default", Status: "READY", Queryable: true}}}
	r := siShardedTestEnv(t, f, newTestSearchIndex(), true)
	reconcileIdx(t, r)

	got := &mongodbv1beta1.MongoDBSearchIndex{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "idx", Namespace: "default"}, got)
	if got.Status.Phase != siPhaseReady {
		t.Errorf("Phase=%q want Ready", got.Status.Phase)
	}
	if f.createCalled {
		t.Error("이미 존재하는데 Create 호출됨")
	}
}

// TestSearchIndexReconcile_ShardedWaitsWhenNotRunning — sharded source not Running → readiness gate
// 가 Building(대기)로 막고 Create 안 함.
func TestSearchIndexReconcile_ShardedWaitsWhenNotRunning(t *testing.T) {
	f := &fakeSearchIndexOps{}
	r := siShardedTestEnv(t, f, newTestSearchIndex(), false)
	reconcileIdx(t, r)

	if f.createCalled {
		t.Error("sharded not Running 인데 Create 호출 — readiness gate 위반")
	}
	got := &mongodbv1beta1.MongoDBSearchIndex{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "idx", Namespace: "default"}, got)
	if got.Status.Phase != siPhaseBuilding {
		t.Errorf("Phase=%q want Building(source 대기)", got.Status.Phase)
	}
}
