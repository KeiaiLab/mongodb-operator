/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mongodbsearch_autouser_unit_test.go — PR2: searchCoordinator sync user auto-create
// 회귀 가드. fake client 만 사용(실 mongo 불요) — secret 생성/보존/라벨 + admin 자격 읽기 +
// user-provided vs auto-create 분기를 결정론적으로 검증. 실 mongod 의 createUser(dual SCRAM)
// 동작 + usersInfo precheck 는 e2e(PR5)에서 실증(repo 에 mongo mock 부재).
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
)

// TestGenerateSyncPassword — 랜덤성 + 길이.
func TestGenerateSyncPassword(t *testing.T) {
	a, b := generateSyncPassword(), generateSyncPassword()
	if len(a) < 32 {
		t.Errorf("password 너무 짧음(%d) — 32byte base64url 기대", len(a))
	}
	if a == b {
		t.Error("연속 생성이 동일 — crypto/rand randomness 결함")
	}
}

// TestSourceAdminPassword — source admin secret 의 password 키 읽기.
func TestSourceAdminPassword(t *testing.T) {
	s := newTestScheme(t)
	source := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "src", Namespace: "default"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Auth: mongodbv1alpha1.AuthSpec{AdminCredentialsSecretRef: corev1.LocalObjectReference{Name: "src-admin"}},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "src-admin", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("p4ss")},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}

	pw, err := r.sourceAdminPassword(context.Background(), source)
	if err != nil {
		t.Fatalf("sourceAdminPassword: %v", err)
	}
	if pw != "p4ss" {
		t.Errorf("pw=%q want p4ss", pw)
	}
}

// TestSourceAdminPassword_MissingSecret — secret 부재 시 fail-fast.
func TestSourceAdminPassword_MissingSecret(t *testing.T) {
	s := newTestScheme(t)
	source := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "src", Namespace: "default"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Auth: mongodbv1alpha1.AuthSpec{AdminCredentialsSecretRef: corev1.LocalObjectReference{Name: "missing"}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}

	if _, err := r.sourceAdminPassword(context.Background(), source); err == nil {
		t.Error("admin secret 부재 시 에러 기대(fail-fast)")
	}
}

// TestEnsureSyncSecret_UserProvided — SyncUserSecretRef 제공 시 그 이름 반환 + operator secret 미생성.
func TestEnsureSyncSecret_UserProvided(t *testing.T) {
	s := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: "srch", Namespace: "default"},
		Spec:       mongodbv1beta1.MongoDBSearchSpec{SyncUserSecretRef: &corev1.LocalObjectReference{Name: "my-sync"}},
	}

	name, err := r.ensureSyncSecret(context.Background(), search)
	if err != nil {
		t.Fatalf("ensureSyncSecret: %v", err)
	}
	if name != "my-sync" {
		t.Errorf("name=%q want my-sync(사용자 제공)", name)
	}
	got := &corev1.Secret{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "srch-search-sync", Namespace: "default"}, got); err == nil {
		t.Error("사용자 제공 시 operator 가 secret 생성하면 안 됨")
	}
}

// TestEnsureSyncSecret_AutoCreate — SyncUserSecretRef nil → <name>-search-sync secret 생성
// (username/password 채움 + managed-by 라벨).
func TestEnsureSyncSecret_AutoCreate(t *testing.T) {
	s := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: "srch", Namespace: "default"},
		Spec:       mongodbv1beta1.MongoDBSearchSpec{}, // nil ref → auto
	}

	name, err := r.ensureSyncSecret(context.Background(), search)
	if err != nil {
		t.Fatalf("ensureSyncSecret(auto): %v", err)
	}
	if name != "srch-search-sync" {
		t.Errorf("name=%q want srch-search-sync", name)
	}
	got := &corev1.Secret{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "srch-search-sync", Namespace: "default"}, got); err != nil {
		t.Fatalf("auto secret 미생성: %v", err)
	}
	if string(got.Data["username"]) != defaultSyncUser {
		t.Errorf("username=%q want %q", got.Data["username"], defaultSyncUser)
	}
	if len(got.Data["password"]) == 0 {
		t.Error("password 미설정")
	}
	if got.Labels["app.kubernetes.io/managed-by"] != "mongodb-operator" {
		t.Errorf("managed-by 라벨 부재(operator 관리 자격증명 식별) — got %v", got.Labels)
	}
}

// TestEnsureSyncSecret_PreservesPassword — 재호출 시 기존 password 보존(SecretIfNotExists no-op).
func TestEnsureSyncSecret_PreservesPassword(t *testing.T) {
	s := newTestScheme(t)
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "srch-search-sync", Namespace: "default"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"username": []byte(defaultSyncUser), "password": []byte("keep-me")},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: "srch", Namespace: "default"},
		Spec:       mongodbv1beta1.MongoDBSearchSpec{},
	}

	if _, err := r.ensureSyncSecret(context.Background(), search); err != nil {
		t.Fatalf("ensureSyncSecret: %v", err)
	}
	got := &corev1.Secret{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "srch-search-sync", Namespace: "default"}, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.Data["password"]) != "keep-me" {
		t.Errorf("password=%q want keep-me(보존) — rotate 사고", got.Data["password"])
	}
}

// TestEnsureSyncMongoUser_UserProvided — 사용자 제공 시 mongod 연결 없이 no-op.
func TestEnsureSyncMongoUser_UserProvided(t *testing.T) {
	s := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: "srch", Namespace: "default"},
		Spec:       mongodbv1beta1.MongoDBSearchSpec{SyncUserSecretRef: &corev1.LocalObjectReference{Name: "my-sync"}},
	}
	source := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "src", Namespace: "default"},
		Status:     mongodbv1alpha1.MongoDBStatus{Phase: mongodbPhaseRunning},
	}
	// 사용자 제공 → mongo 연결 시도 없이 nil(연결하면 fake 환경에서 hang/err).
	if err := r.ensureSyncMongoUser(context.Background(), search, source, "my-sync"); err != nil {
		t.Errorf("user-provided 시 no-op 기대, got %v", err)
	}
}

// TestEnsureSyncMongoUser_NotRunning — source 미Running 시 mongod 연결 없이 no-op(다음 reconcile).
func TestEnsureSyncMongoUser_NotRunning(t *testing.T) {
	s := newTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: "srch", Namespace: "default"},
		Spec:       mongodbv1beta1.MongoDBSearchSpec{}, // auto
	}
	source := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "src", Namespace: "default"},
		// Phase 미설정 → not Running
	}
	if err := r.ensureSyncMongoUser(context.Background(), search, source, "srch-search-sync"); err != nil {
		t.Errorf("not-Running 시 no-op 기대, got %v", err)
	}
}
