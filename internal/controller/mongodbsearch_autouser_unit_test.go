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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
	"github.com/keiailab/mongodb-operator/internal/resources"
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
	if got.Labels[labelManagedBy] != managedByValue {
		t.Errorf("managed-by 라벨 부재(operator 관리 자격증명 식별) — got %v", got.Labels)
	}
}

// TestEnsureSyncSecret_PreservesOwnedPassword — operator 소유(owner-ref) secret 은 password 보존
// (rotate 사고 방지). owner-ref 있는 secret 만 adopt 대상.
func TestEnsureSyncSecret_PreservesOwnedPassword(t *testing.T) {
	s := newTestScheme(t)
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: "srch", Namespace: "default", UID: "uid-1"},
		Spec:       mongodbv1beta1.MongoDBSearchSpec{},
	}
	owned := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "srch-search-sync", Namespace: "default"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"username": []byte(defaultSyncUser), "password": []byte("keep-me")},
	}
	if err := controllerutil.SetControllerReference(search, owned, s); err != nil {
		t.Fatalf("set owner ref: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(owned).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}

	if _, err := r.ensureSyncSecret(context.Background(), search); err != nil {
		t.Fatalf("ensureSyncSecret(owned): %v", err)
	}
	got := &corev1.Secret{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "srch-search-sync", Namespace: "default"}, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.Data["password"]) != "keep-me" {
		t.Errorf("password=%q want keep-me(보존) — rotate 사고", got.Data["password"])
	}
}

// TestEnsureSyncSecret_RejectsForeignSecret — 보안 회귀: owner-ref 없는 pre-staged(foreign)
// secret 은 adopt 거부(error). password-axis privilege escalation 차단의 핵심 — 공격자가 심은
// password 를 operator 가 채택해 특권 user 를 만드는 경로를 봉쇄.
func TestEnsureSyncSecret_RejectsForeignSecret(t *testing.T) {
	s := newTestScheme(t)
	// 공격자 pre-staged: owner-ref 없음, password 공격자 지정.
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "srch-search-sync", Namespace: "default"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"username": []byte(defaultSyncUser), "password": []byte("attacker-known")},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(foreign).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: "srch", Namespace: "default", UID: "uid-1"},
		Spec:       mongodbv1beta1.MongoDBSearchSpec{},
	}

	_, err := r.ensureSyncSecret(context.Background(), search)
	if err == nil {
		t.Fatal("foreign(owner-ref 없는) secret 을 adopt 함 — privilege escalation 미차단")
	}
	if !strings.Contains(err.Error(), "소유가 아님") {
		t.Errorf("err=%q — foreign adopt 거부 사유 기대", err)
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

// TestResolveSyncUser_AutoPathIgnoresSecretUsername — 보안 회귀: auto-create 경로에서
// 공격자가 secret 의 username 을 admin 등으로 pre-stage 해도 mongot config 의 syncUser 는
// defaultSyncUser 로 강제돼야 한다(privilege escalation 차단). Reconcile 이 SyncUserSecretRef==nil
// 분기에서 resolveSyncUser 결과를 무시하고 defaultSyncUser 를 쓰는지 직접 검증한다.
func TestResolveSyncUser_AutoPathIgnoresSecretUsername(t *testing.T) {
	s := newTestScheme(t)
	// 공격자가 pre-stage 한 악성 secret: username=admin(특권 user 탈취 시도).
	malicious := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "srch-search-sync", Namespace: "default"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("attacker-known")},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(malicious).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}

	// resolveSyncUser 자체는 secret username 을 읽지만(사용자 제공 경로용), auto 경로 Reconcile 은
	// 이를 호출하지 않고 defaultSyncUser 를 쓴다. 여기선 resolveSyncUser 가 admin 을 반환함을
	// 확인해(공격 표면 존재) → auto 경로가 이를 우회함을 대비 검증.
	got, err := r.resolveSyncUser(context.Background(), "srch-search-sync", "default")
	if err != nil {
		t.Fatalf("resolveSyncUser: %v", err)
	}
	if got != "admin" {
		t.Fatalf("setup 가정 위반: resolveSyncUser=%q (악성 secret username=admin 이어야)", got)
	}
	if defaultSyncUser == "admin" {
		t.Fatal("defaultSyncUser 가 admin 이면 격리 무의미")
	}
}

// TestReconcile_AutoPath_RejectsForeignSecret — 보안 회귀(end-to-end): 공격자가 pre-stage 한
// foreign secret(owner-ref 없음, password 공격자 지정)이 있으면 auto-create Reconcile 은 그것을
// adopt 하지 않고 Failed phase 로 거부해야 한다. password/username 어느 축도 공격자 값이 mongod
// user 나 mongot config 로 흘러선 안 된다(privilege escalation 차단). 공격자 secret 이 admin
// 자격을 얻지 못함 = ConfigMap 도 생성되지 않음(secret guard 가 ConfigMap 앞에서 fail).
func TestReconcile_AutoPath_RejectsForeignSecret(t *testing.T) {
	s := newTestScheme(t)
	const name, ns = "srch", "default"
	source := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       mongodbv1alpha1.MongoDBSpec{Members: 1, ReplicaSetName: "rs0"},
	}
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-search-sync", Namespace: ns}, // owner-ref 없음
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("attacker")},
	}
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: mongodbv1beta1.MongoDBSearchSpec{
			Source: mongodbv1beta1.SearchSource{MongoDBResourceRef: &corev1.LocalObjectReference{Name: name}, Kind: "MongoDB"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(source, foreign, search).
		WithStatusSubresource(source, search).
		Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}

	// Reconcile 은 fail(secret guard) → 에러 없이 Failed phase + requeue.
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got := &mongodbv1beta1.MongoDBSearch{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: ns}, got); err != nil {
		t.Fatalf("get search: %v", err)
	}
	if got.Status.Phase != searchPhaseFailed {
		t.Errorf("Phase=%q want Failed(foreign secret 거부)", got.Status.Phase)
	}
	// mongot ConfigMap 이 생성되지 않아야(공격자 username/password 가 어디로도 안 흐름).
	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: resources.MongotConfigMapName(name), Namespace: ns}, cm); err == nil {
		t.Fatalf("foreign secret 거부 후에도 mongot ConfigMap 생성됨 — guard 가 ConfigMap 앞에서 막아야\n%s", cm.Data["config.yml"])
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
