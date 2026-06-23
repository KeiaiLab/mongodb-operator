/*
Copyright 2024 Keiailab.

SPDX-License-Identifier: MIT
*/

// adopted_upgrade_unit_test.go — Helm→OLM 인수(adopt)된 기존 클러스터의 무중단 업그레이드
// 회귀 가드. fake client + 직접 reconcile 호출만 사용한다(envtest 불요).
//
// 버그(v1.15.1 이하): operator가 인수(adopt)한 기존 클러스터는 Status.Version 이 비어
// 있다(완료 시 completeUpgrade 에서만 set). 특히 Sharded 컨트롤러는 Status.Version 백필
// 경로가 *전혀 없어*(RS 의 updateStatus 642줄 같은 보강 없음) reconcileShardedUpgrade
// 진입 시 currentVersion="" → `if currentVersion == ""` 으로 업그레이드 orchestration 이
// 영구 skip 된다. 결과: 사용자가 spec.Version 을 올려도 업그레이드가 감지되지 않는다
// (keiailab-mongo 8.3.4 패치 사고).
//
// fix: reconcile 진입부에서 Status.Version 이 비어 있으면 EffectiveVersion(=desired)으로
// seed 해, 이후 spec.Version 변경이 업그레이드로 감지되게 한다. builder 는 EffectiveVersion
// 만 쓰므로 Status.Version seed 는 업그레이드 *감지*에만 영향(무롤링 보존).
package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// newAdoptedSharded 는 adopt 직후(Status 전부 비어 있음) 상태의 Sharded CR 을 만든다.
// Helm→OLM 인수 클러스터를 모사 — operator 가 이 CR 을 한 번도 업그레이드 완료한 적이 없어
// Status.Version / Status.EffectiveVersion 이 "" 이다.
func newAdoptedSharded(name, version string) *mongodbv1alpha1.MongoDBSharded {
	return &mongodbv1alpha1.MongoDBSharded{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "mongodb.keiailab.com/v1alpha1",
			Kind:       "MongoDBSharded",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: mongodbv1alpha1.MongoDBShardedSpec{
			Version: mongodbv1alpha1.MongoDBVersion{Version: version},
			ConfigServer: mongodbv1alpha1.ConfigServerSpec{
				Members: 3,
				Storage: mongodbv1alpha1.StorageSpec{Size: resource.MustParse("10Gi")},
			},
			Shards: mongodbv1alpha1.ShardSpec{
				Count:           2,
				MembersPerShard: 3,
				Storage:         mongodbv1alpha1.StorageSpec{Size: resource.MustParse("50Gi")},
			},
			Mongos: mongodbv1alpha1.MongosSpec{Replicas: 2},
		},
	}
}

// TestShardedAdopted_SeedsStatusVersion — adopt 된 Sharded 클러스터를 reconcile 하면
// Status.Version 이 desired 로 seed 되어야 한다(비어 있으면 안 됨). 버그 버전은 sharded
// 경로에 Status.Version 백필이 없어 "" 로 남아 업그레이드 감지가 영구 불가했다.
func TestShardedAdopted_SeedsStatusVersion(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newAdoptedSharded("adopted-shard", "8.3.3")

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdbsh).
		WithStatusSubresource(mdbsh).
		Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	if _, _, err := r.reconcileShardedUpgrade(context.Background(), mdbsh); err != nil {
		t.Fatalf("reconcileShardedUpgrade(adopted): %v", err)
	}

	if mdbsh.Status.Version == "" {
		t.Fatal("Status.Version 이 seed 되지 않음 — adopt 클러스터 업그레이드 감지 영구 skip 버그")
	}
	if mdbsh.Status.Version != "8.3.3" {
		t.Errorf("Status.Version = %q, want 8.3.3 (desired 로 seed)", mdbsh.Status.Version)
	}
	if mdbsh.Status.EffectiveVersion != "8.3.3" {
		t.Errorf("Status.EffectiveVersion = %q, want 8.3.3", mdbsh.Status.EffectiveVersion)
	}
}

// TestShardedAdopted_DetectsLaterUpgrade — adopt(8.3.3) 후 사용자가 spec.Version 을
// 8.3.4 로 올리면 업그레이드가 감지되어야 한다(UpgradePhase=Upgrading). 이것이 실제
// keiailab-mongo 8.3.4 패치 사고의 직접 재현 — 버그 버전은 Status.Version="" 때문에
// 두 번째 reconcile 에서도 `if currentVersion == ""` 로 빠져 업그레이드를 영구 무시했다.
func TestShardedAdopted_DetectsLaterUpgrade(t *testing.T) {
	s := newTestScheme(t)
	mdbsh := newAdoptedSharded("adopted-shard-upgrade", "8.3.3")

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdbsh).
		WithStatusSubresource(mdbsh).
		Build()
	r := &MongoDBShardedReconciler{Client: cl, Scheme: s}

	// reconcile #1: adopt — Status.Version seed(8.3.3), 업그레이드 아님.
	if _, handled, err := r.reconcileShardedUpgrade(context.Background(), mdbsh); err != nil {
		t.Fatalf("reconcileShardedUpgrade #1(adopt): %v", err)
	} else if handled {
		t.Fatal("adopt(spec==running) 에서 업그레이드로 오판(handled=true) — 무롤링 위반")
	}

	// 사용자가 patch 업그레이드 트리거: spec.Version 8.3.3 → 8.3.4.
	mdbsh.Spec.Version.Version = "8.3.4"

	// reconcile #2: 업그레이드 감지되어야 함.
	_, handled, err := r.reconcileShardedUpgrade(context.Background(), mdbsh)
	if err != nil {
		t.Fatalf("reconcileShardedUpgrade #2(upgrade): %v", err)
	}
	if !handled {
		t.Fatal("spec.Version 8.3.3→8.3.4 업그레이드 미감지 — adopt 클러스터 영구 skip 버그")
	}
	if mdbsh.Status.UpgradePhase != UpgradePhaseUpgrading {
		t.Errorf("UpgradePhase = %q, want %q", mdbsh.Status.UpgradePhase, UpgradePhaseUpgrading)
	}
	if mdbsh.Status.PreviousVersion != "8.3.3" {
		t.Errorf("PreviousVersion = %q, want 8.3.3", mdbsh.Status.PreviousVersion)
	}
	if mdbsh.Status.EffectiveVersion != "8.3.4" {
		t.Errorf("EffectiveVersion = %q, want 8.3.4 (builder 가 신버전으로 롤링)", mdbsh.Status.EffectiveVersion)
	}
}

// TestRSAdopted_SeedsStatusVersion — RS(MongoDB) 경로의 동일 seed 대칭 검증. RS 는
// updateStatus 백필이 있어 버그가 좁지만, reconcileUpgrade 진입부 seed 로 업그레이드 중
// (handled=true 로 updateStatus 미도달)에도 Status.Version 이 일관되게 채워져야 한다.
func TestRSAdopted_SeedsStatusVersion(t *testing.T) {
	s := newTestScheme(t)
	mdb := &mongodbv1alpha1.MongoDB{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "mongodb.keiailab.com/v1alpha1",
			Kind:       "MongoDB",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "adopted-rs", Namespace: "default"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        3,
			ReplicaSetName: "rs0",
			Version:        mongodbv1alpha1.MongoDBVersion{Version: "8.3.3"},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(mdb).
		WithStatusSubresource(mdb).
		Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	if _, _, err := r.reconcileUpgrade(context.Background(), mdb); err != nil {
		t.Fatalf("reconcileUpgrade(adopted): %v", err)
	}

	if mdb.Status.Version == "" {
		t.Fatal("RS Status.Version 이 seed 되지 않음 — adopt 클러스터 seed 누락")
	}
	if mdb.Status.Version != "8.3.3" {
		t.Errorf("Status.Version = %q, want 8.3.3", mdb.Status.Version)
	}
}
