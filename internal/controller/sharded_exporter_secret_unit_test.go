/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

// 본 파일은 sharded exporter-uri Secret reconcile 갭(비-sharded
// MongoDBReconciler.reconcileExporterSecret 에만 존재하던 로직이
// MongoDBShardedReconciler 에 부재 → monitoring.enabled=true 시 mongos exporter
// sidecar 가 CreateContainerConfigError)를 메우는 변경의 envtest-free 회귀 가드.
// fake client + 직접 함수 호출만 사용한다.
package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// newExporterTestSharded는 admin 자격 Secret 참조와 monitoring.enabled=true 를
// 가진 sharded CR 을 만든다(exporter secret reconcile 경로만 검증).
func newExporterTestSharded(name string) *mongodbv1alpha1.MongoDBSharded {
	mdbsh := newP1TestSharded(name)
	mdbsh.Spec.Auth.AdminCredentialsSecretRef = corev1.LocalObjectReference{Name: name + "-admin"}
	// ADR-0018 Phase 1: MonitoringSpec 타입에 deprecation marker 가 있으나, 본
	// 변경이 parity 복구하는 대상이 바로 Monitoring.Enabled 게이트의 exporter
	// sidecar 메커니즘이다(비-sharded 가 이미 구현·운영). 게이트 자체는 유효 —
	// builder.go 가 동일 사유로 //nolint:staticcheck 처리하는 패턴을 따른다.
	//lint:ignore SA1019 ADR-0018 Phase 1: MonitoringSpec 타입은 deprecated 이나 Enabled 게이트의 exporter sidecar 메커니즘은 유효 — 본 테스트가 그 parity 를 가드.
	mdbsh.Spec.Monitoring = &mongodbv1alpha1.MonitoringSpec{Enabled: true} //nolint:staticcheck
	return mdbsh
}

// adminSecretFor는 username/password 키를 가진 admin 자격 Secret 을 만든다.
func adminSecretFor(mdbsh *mongodbv1alpha1.MongoDBSharded, user, pass string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name, Namespace: mdbsh.Namespace},
		Data: map[string][]byte{
			"username": []byte(user),
			"password": []byte(pass),
		},
	}
}

// TestShardedExporterHost는 sidecar 가 mongos 에 루프백으로 접속하는지 가드한다.
// (service DNS 사용 시 다른 mongos pod 로 라우팅되는 self-scrape 불일치 회피)
func TestShardedExporterHost(t *testing.T) {
	if got := shardedExporterHost(); got != "127.0.0.1:27017" {
		t.Fatalf("기대 127.0.0.1:27017, got %q", got)
	}
}

// TestReconcileShardedExporterSecret_CreatesSecret는 monitoring.enabled=true +
// admin secret 존재 시 <name>-exporter-uri Secret 이 mongos 루프백 URI("uri"
// 키)로 생성되고 owner reference 가 설정되는지 검증한다.
func TestReconcileShardedExporterSecret_CreatesSecret(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newExporterTestSharded("ex-create")
	adminSecret := adminSecretFor(mdbsh, "admin", "secret123")

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdbsh, adminSecret).
		Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	if err := r.reconcileShardedExporterSecret(context.Background(), mdbsh); err != nil {
		t.Fatalf("기대 err=nil, got %v", err)
	}

	got := &corev1.Secret{}
	if err := cl.Get(context.Background(), types.NamespacedName{
		Name: mdbsh.Name + "-exporter-uri", Namespace: mdbsh.Namespace,
	}, got); err != nil {
		t.Fatalf("exporter-uri Secret 미생성: %v", err)
	}

	uri := got.StringData["uri"]
	if uri == "" {
		// fake client 는 StringData 를 Data 로 정규화할 수 있어 양쪽 확인.
		uri = string(got.Data["uri"])
	}
	want := "mongodb://admin:secret123@127.0.0.1:27017/?authSource=admin"
	if uri != want {
		t.Fatalf("URI 불일치\n기대: %q\n실제: %q", want, uri)
	}

	// owner reference (controller) 설정 검증 — CR 삭제 시 GC 보장.
	if len(got.OwnerReferences) != 1 || got.OwnerReferences[0].Name != mdbsh.Name {
		t.Fatalf("owner reference 미설정 또는 불일치: %+v", got.OwnerReferences)
	}
}

// TestReconcileShardedExporterSecret_SpecialPassword는 패스워드 예약문자가
// buildExporterURI 재사용으로 안전하게 인코딩되는지(URI 구조 미파손) 가드한다.
func TestReconcileShardedExporterSecret_SpecialPassword(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newExporterTestSharded("ex-special")
	adminSecret := adminSecretFor(mdbsh, "admin", "p@ss:w/rd")

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdbsh, adminSecret).
		Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	if err := r.reconcileShardedExporterSecret(context.Background(), mdbsh); err != nil {
		t.Fatalf("기대 err=nil, got %v", err)
	}

	got := &corev1.Secret{}
	if err := cl.Get(context.Background(), types.NamespacedName{
		Name: mdbsh.Name + "-exporter-uri", Namespace: mdbsh.Namespace,
	}, got); err != nil {
		t.Fatalf("exporter-uri Secret 미생성: %v", err)
	}
	uri := got.StringData["uri"]
	if uri == "" {
		uri = string(got.Data["uri"])
	}
	if strings.Contains(uri, "p@ss:w/rd") {
		t.Fatalf("패스워드가 미인코딩 상태로 URI에 노출됨: %q", uri)
	}
	want := "mongodb://admin:p%40ss%3Aw%2Frd@127.0.0.1:27017/?authSource=admin"
	if uri != want {
		t.Fatalf("URI 불일치\n기대: %q\n실제: %q", want, uri)
	}
}

// TestReconcileShardedExporterSecret_MissingAdminRef는 monitoring.enabled 인데
// adminCredentialsSecretRef 가 비어 있으면 에러를 반환하는지(빈 자격으로 깨진
// URI 생성 방지) 검증한다.
func TestReconcileShardedExporterSecret_MissingAdminRef(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newExporterTestSharded("ex-noref")
	mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name = ""

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdbsh).
		Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	if err := r.reconcileShardedExporterSecret(context.Background(), mdbsh); err == nil {
		t.Fatalf("adminCredentialsSecretRef 부재 시 에러를 기대했으나 nil")
	}
}

// TestReconcileShardedExporterSecret_MissingPassword는 admin secret 에 password
// 키가 없으면 buildExporterURI 의 ok-idiom 으로 에러가 전파되는지 검증한다.
func TestReconcileShardedExporterSecret_MissingPassword(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newExporterTestSharded("ex-nopw")
	adminSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name, Namespace: mdbsh.Namespace},
		Data:       map[string][]byte{"username": []byte("admin")},
	}

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdbsh, adminSecret).
		Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	err := r.reconcileShardedExporterSecret(context.Background(), mdbsh)
	if err == nil {
		t.Fatalf("password 부재 시 에러를 기대했으나 nil")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Fatalf("에러 메시지에 password 누락 사유 기대, got %q", err.Error())
	}
}
