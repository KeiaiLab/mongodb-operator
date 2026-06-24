/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mongodbsearchindex_controller_test.go — MongoDBSearchIndex reconcile 회귀 가드. fake client +
// fake searchIndexOps 주입(실 mongo 불요). state machine(Pending/Building/Ready) + finalizer drop +
// readiness gate 를 결정론 검증.
package controller

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
	"github.com/keiailab/mongodb-operator/internal/mongodb"
)

// fakeSearchIndexOps — searchIndexOps 의 메모리 fake. list 결과/생성·삭제 호출을 기록.
type fakeSearchIndexOps struct {
	listResult   []mongodb.SearchIndexInfo
	createCalled bool
	dropCalled   bool
	createErr    error
}

func (f *fakeSearchIndexOps) List(_ context.Context, _, _, _, _, _ string) ([]mongodb.SearchIndexInfo, error) {
	return f.listResult, nil
}
func (f *fakeSearchIndexOps) Create(_ context.Context, _, _, _, _, _, _ string, _ bson.M) (string, error) {
	f.createCalled = true
	return "idx-id-1", f.createErr
}
func (f *fakeSearchIndexOps) Update(_ context.Context, _, _, _, _, _ string, _ bson.M) error {
	return nil
}
func (f *fakeSearchIndexOps) Drop(_ context.Context, _, _, _, _, _ string) error {
	f.dropCalled = true
	return nil
}

// siTestEnv — search index reconcile 테스트용 공통 fixture(source Running + search Ready).
func siTestEnv(t *testing.T, ops *fakeSearchIndexOps, idx *mongodbv1beta1.MongoDBSearchIndex) *MongoDBSearchIndexReconciler {
	t.Helper()
	s := newTestScheme(t)
	source := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "src", Namespace: "default"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members: 1, ReplicaSetName: "rs0",
			Auth: mongodbv1alpha1.AuthSpec{AdminCredentialsSecretRef: corev1.LocalObjectReference{Name: "src-admin"}},
		},
	}
	source.Status.Phase = mongodbPhaseRunning
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: "srch", Namespace: "default"},
		Spec: mongodbv1beta1.MongoDBSearchSpec{
			Source: mongodbv1beta1.SearchSource{MongoDBResourceRef: &corev1.LocalObjectReference{Name: "src"}, Kind: "MongoDB"},
		},
	}
	search.Status.Phase = searchPhaseReady
	adminSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "src-admin", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("pw")},
	}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(source, search, adminSecret, idx).
		WithStatusSubresource(source, search, idx).
		Build()
	return &MongoDBSearchIndexReconciler{
		Client: cl, Scheme: s,
		NewManager: func(_, _, _, _, _ string) searchIndexOps { return ops },
	}
}

func newTestSearchIndex() *mongodbv1beta1.MongoDBSearchIndex {
	return &mongodbv1beta1.MongoDBSearchIndex{
		ObjectMeta: metav1.ObjectMeta{Name: "idx", Namespace: "default"},
		Spec: mongodbv1beta1.MongoDBSearchIndexSpec{
			SearchRef: corev1.LocalObjectReference{Name: "srch"},
			Database:  "db", Collection: "coll", IndexName: "default", Type: "search",
			DefinitionJSON: `{"mappings":{"dynamic":true}}`,
		},
	}
}

func reconcileIdx(t *testing.T, r *MongoDBSearchIndexReconciler) {
	t.Helper()
	for i := 0; i < 3; i++ { // finalizer add → requeue → 본 reconcile 수렴
		if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "idx", Namespace: "default"}}); err != nil {
			t.Fatalf("Reconcile #%d: %v", i, err)
		}
	}
}

// TestSearchIndexReconcile_CreatesWhenAbsent — 인덱스 부재 → Create 호출 + Phase=Building.
func TestSearchIndexReconcile_CreatesWhenAbsent(t *testing.T) {
	f := &fakeSearchIndexOps{listResult: nil} // 부재
	r := siTestEnv(t, f, newTestSearchIndex())
	reconcileIdx(t, r)

	if !f.createCalled {
		t.Error("인덱스 부재 시 Create 미호출")
	}
	got := &mongodbv1beta1.MongoDBSearchIndex{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "idx", Namespace: "default"}, got)
	if got.Status.Phase != siPhaseBuilding {
		t.Errorf("Phase=%q want Building(생성 직후)", got.Status.Phase)
	}
	// finalizer 부착 확인.
	if len(got.Finalizers) == 0 {
		t.Error("finalizer 미부착")
	}
}

// TestSearchIndexReconcile_ReadyWhenQueryable — 인덱스 존재 READY+queryable → Phase=Ready.
func TestSearchIndexReconcile_ReadyWhenQueryable(t *testing.T) {
	f := &fakeSearchIndexOps{listResult: []mongodb.SearchIndexInfo{{ID: "x", Name: "default", Status: "READY", Queryable: true}}}
	r := siTestEnv(t, f, newTestSearchIndex())
	reconcileIdx(t, r)

	got := &mongodbv1beta1.MongoDBSearchIndex{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "idx", Namespace: "default"}, got)
	if got.Status.Phase != siPhaseReady {
		t.Errorf("Phase=%q want Ready", got.Status.Phase)
	}
	if !got.Status.Queryable {
		t.Error("Queryable=false want true")
	}
	if got.Status.IndexID != "x" {
		t.Errorf("IndexID=%q want x", got.Status.IndexID)
	}
	if f.createCalled {
		t.Error("이미 존재하는데 Create 호출됨")
	}
}

// TestSearchIndexReconcile_PendingWhenSourceNotReady — source not Running → Building(대기), Create 안 함.
func TestSearchIndexReconcile_PendingWhenSourceNotReady(t *testing.T) {
	f := &fakeSearchIndexOps{}
	s := newTestScheme(t)
	source := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "src", Namespace: "default"},
		Spec:       mongodbv1alpha1.MongoDBSpec{Auth: mongodbv1alpha1.AuthSpec{AdminCredentialsSecretRef: corev1.LocalObjectReference{Name: "src-admin"}}},
		// Phase 미설정 → not Running
	}
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: "srch", Namespace: "default"},
		Spec:       mongodbv1beta1.MongoDBSearchSpec{Source: mongodbv1beta1.SearchSource{MongoDBResourceRef: &corev1.LocalObjectReference{Name: "src"}, Kind: "MongoDB"}},
	}
	adminSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "src-admin", Namespace: "default"}, Data: map[string][]byte{"password": []byte("pw")}}
	idx := newTestSearchIndex()
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(source, search, adminSecret, idx).WithStatusSubresource(source, search, idx).Build()
	r := &MongoDBSearchIndexReconciler{Client: cl, Scheme: s, NewManager: func(_, _, _, _, _ string) searchIndexOps { return f }}

	reconcileIdx(t, r)
	if f.createCalled {
		t.Error("source not Running 인데 Create 호출 — readiness gate 위반")
	}
	got := &mongodbv1beta1.MongoDBSearchIndex{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "idx", Namespace: "default"}, got)
	if got.Status.Phase != siPhaseBuilding {
		t.Errorf("Phase=%q want Building(source 대기)", got.Status.Phase)
	}
}

// TestSearchIndexReconcile_DropsOnDelete — 삭제 시 finalizer drop 호출 + finalizer 제거.
func TestSearchIndexReconcile_DropsOnDelete(t *testing.T) {
	f := &fakeSearchIndexOps{listResult: []mongodb.SearchIndexInfo{{ID: "x", Name: "default", Status: "READY", Queryable: true}}}
	idx := newTestSearchIndex()
	idx.Finalizers = []string{searchIndexFinalizer}
	now := metav1.Now()
	idx.DeletionTimestamp = &now
	r := siTestEnv(t, f, idx)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "idx", Namespace: "default"}}); err != nil {
		t.Fatalf("Reconcile(delete): %v", err)
	}
	if !f.dropCalled {
		t.Error("삭제 시 Drop 미호출 — finalizer cleanup 누락")
	}
}

// TestSearchIndexReconcile_FailsOnInvalidJSON — definitionJSON 파싱 실패 → Phase=Failed.
func TestSearchIndexReconcile_FailsOnInvalidJSON(t *testing.T) {
	f := &fakeSearchIndexOps{}
	idx := newTestSearchIndex()
	idx.Spec.DefinitionJSON = `{not valid`
	r := siTestEnv(t, f, idx)
	reconcileIdx(t, r)

	got := &mongodbv1beta1.MongoDBSearchIndex{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "idx", Namespace: "default"}, got)
	if got.Status.Phase != siPhaseFailed {
		t.Errorf("Phase=%q want Failed(invalid JSON)", got.Status.Phase)
	}
	if f.createCalled {
		t.Error("invalid JSON 인데 Create 호출")
	}
}
