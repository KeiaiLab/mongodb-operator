/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mongodbsharded_mtls_phase_test.go — Phase 5.4-c2 sharded reconcileMTLSPhase
// 배선 회귀 가드 (fake client). 안전 단계 결정은 security TestResolveMTLSStep 커버.

package controller

import (
	"context"
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func shardedMTLS(mode string) *mongodbv1alpha1.MongoDBSharded {
	return &mongodbv1alpha1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{Name: "sh", Namespace: "ns"},
		Spec: mongodbv1alpha1.MongoDBShardedSpec{
			ConfigServer: mongodbv1alpha1.ConfigServerSpec{Members: 3},
			Shards:       mongodbv1alpha1.ShardSpec{Count: 1, MembersPerShard: 3},
			TLS: &mongodbv1alpha1.TLSSpec{
				Enabled: true,
				MTLS:    &mongodbv1alpha1.MTLSSpec{Enabled: true, Mode: mode},
			},
		},
	}
}

// allReadyObjs returns cfg/shard/mongos workloads all fully ready.
func allReadyObjs(mdbsh *mongodbv1alpha1.MongoDBSharded) []client.Object {
	cfg := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: mdbsh.Name + "-cfg", Namespace: mdbsh.Namespace},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: mdbsh.Spec.ConfigServer.Members},
	}
	shard := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-shard-0", mdbsh.Name), Namespace: mdbsh.Namespace},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: mdbsh.Spec.Shards.MembersPerShard},
	}
	mongos := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: mdbsh.Name + "-mongos", Namespace: mdbsh.Namespace},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
	return []client.Object{cfg, shard, mongos}
}

func TestShardedReconcileMTLSPhase_DisabledNoOp(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := &mongodbv1alpha1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{Name: "sh", Namespace: "ns"},
		Spec:       mongodbv1alpha1.MongoDBShardedSpec{ConfigServer: mongodbv1alpha1.ConfigServerSpec{Members: 3}, Shards: mongodbv1alpha1.ShardSpec{Count: 1, MembersPerShard: 3}},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdbsh).WithStatusSubresource(mdbsh).Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}
	if err := r.reconcileMTLSPhase(context.Background(), mdbsh); err != nil {
		t.Fatalf("err=%v", err)
	}
	if mdbsh.Status.MTLSPhase != "" {
		t.Errorf("MTLS 비활성 시 MTLSPhase 빈값 기대, got %q", mdbsh.Status.MTLSPhase)
	}
}

func TestShardedReconcileMTLSPhase_NewClusterStartsSafe(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := shardedMTLS("x509") // 워크로드 없음 → rolloutReady=false → keyFile 부터
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdbsh).WithStatusSubresource(mdbsh).Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}
	if err := r.reconcileMTLSPhase(context.Background(), mdbsh); err != nil {
		t.Fatalf("err=%v", err)
	}
	if mdbsh.Status.MTLSPhase != "keyFile" {
		t.Errorf("신규 sharded 는 keyFile 부터 안전 시작 기대, got %q", mdbsh.Status.MTLSPhase)
	}
	if mdbsh.Spec.TLS.MTLS.Mode != "keyFile" {
		t.Errorf("effective mode=keyFile 기대, got %q", mdbsh.Spec.TLS.MTLS.Mode)
	}
}

func TestShardedReconcileMTLSPhase_AllReadyAdvancesOneStep(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := shardedMTLS("x509")
	mdbsh.Status.MTLSPhase = "sendKeyFile"
	objs := append([]client.Object{mdbsh}, allReadyObjs(mdbsh)...)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(mdbsh).Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}
	if err := r.reconcileMTLSPhase(context.Background(), mdbsh); err != nil {
		t.Fatalf("err=%v", err)
	}
	if mdbsh.Status.MTLSPhase != "sendX509" {
		t.Errorf("전 컴포넌트 ready 시 한 단계 전진(sendKeyFile→sendX509) 기대, got %q", mdbsh.Status.MTLSPhase)
	}
}

func TestShardedReconcileMTLSPhase_OneComponentLaggingHolds(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := shardedMTLS("x509")
	mdbsh.Status.MTLSPhase = "sendKeyFile"
	objs := allReadyObjs(mdbsh)
	// mongos 만 not-ready 로 강등 → 전체 rolloutReady=false → 현 단계 유지.
	objs[2] = &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: mdbsh.Name + "-mongos", Namespace: mdbsh.Namespace},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 0},
	}
	all := append([]client.Object{mdbsh}, objs...)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(all...).WithStatusSubresource(mdbsh).Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}
	if err := r.reconcileMTLSPhase(context.Background(), mdbsh); err != nil {
		t.Fatalf("err=%v", err)
	}
	if mdbsh.Status.MTLSPhase != "sendKeyFile" {
		t.Errorf("한 컴포넌트 미완 시 현 단계 유지(점프 금지) 기대, got %q", mdbsh.Status.MTLSPhase)
	}
}
