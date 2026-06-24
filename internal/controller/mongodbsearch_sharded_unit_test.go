/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mongodbsearch_sharded_unit_test.go — PR4: Sharded search reconcile 회귀 가드. fake client
// (실 mongo 불요 — Phase!=Running 으로 두어 mongos sync user 생성 skip). shard 별 mongot ConfigMap
// (port 27018) + MongoDBSharded annotation 주입을 결정론 검증.
package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	mongodbv1beta1 "github.com/keiailab/mongodb-operator/api/v1beta1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

func newShardedForSearch(name string) *mongodbv1alpha1.MongoDBSharded {
	return &mongodbv1alpha1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: mongodbv1alpha1.MongoDBShardedSpec{
			Version:      mongodbv1alpha1.MongoDBVersion{Version: "8.2"},
			ConfigServer: mongodbv1alpha1.ConfigServerSpec{Members: 3, Storage: mongodbv1alpha1.StorageSpec{Size: resource.MustParse("1Gi")}},
			Shards:       mongodbv1alpha1.ShardSpec{Count: 3, MembersPerShard: 3, Storage: mongodbv1alpha1.StorageSpec{Size: resource.MustParse("1Gi")}},
			Mongos:       mongodbv1alpha1.MongosSpec{Replicas: 2},
			Auth:         mongodbv1alpha1.AuthSpec{AdminCredentialsSecretRef: corev1.LocalObjectReference{Name: name + "-admin"}},
		},
		// Status.Phase 미설정 → not Running(mongos sync user 생성 skip, ConfigMap/annotate 는 진행)
	}
}

func reconcileSearch(t *testing.T, r *MongoDBSearchReconciler, name string) {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: "default"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

// TestReconcileSharded_CreatesPerShardConfigMaps — Sharded source → shard 마다 mongot ConfigMap +
// MongoDBSharded annotation. shard STS builder 가 annotation 을 읽어 sidecar 주입(별도 검증).
func TestReconcileSharded_CreatesPerShardConfigMaps(t *testing.T) {
	s := newTestScheme(t)
	const name = "ksh"
	mdbsh := newShardedForSearch(name)
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: mongodbv1beta1.MongoDBSearchSpec{
			Source: mongodbv1beta1.SearchSource{MongoDBResourceRef: &corev1.LocalObjectReference{Name: name}, Kind: "MongoDBSharded"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdbsh, search).WithStatusSubresource(mdbsh, search).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}

	reconcileSearch(t, r, name)

	// shard 0,1,2 ConfigMap 생성 확인.
	for i := 0; i < 3; i++ {
		shardName := name + "-shard-" + string(rune('0'+i))
		cm := &corev1.ConfigMap{}
		if err := cl.Get(context.Background(), types.NamespacedName{Name: resources.MongotConfigMapName(shardName), Namespace: "default"}, cm); err != nil {
			t.Fatalf("shard-%d mongot ConfigMap 미생성: %v", i, err)
		}
		// port 27018 사용 확인(shard mongod).
		if !strings.Contains(cm.Data["config.yml"], `localhost:27018`) {
			t.Errorf("shard-%d ConfigMap 이 27018 미사용:\n%s", i, cm.Data["config.yml"])
		}
	}

	// MongoDBSharded annotation 확인.
	got := &mongodbv1alpha1.MongoDBSharded{}
	_ = cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, got)
	if got.Annotations[resources.MongotSidecarImageAnnotation] == "" {
		t.Error("MongoDBSharded sidecar image annotation 미설정")
	}
}

// shardedWithSidecarObjs — cluster Running + Count 개 shard STS + 각 shard 의 mongot pod(ready 지정).
// SyncUserSecretRef 제공으로 ensureSyncMongoUserSharded no-op(mongos 연결 시도 차단).
func shardedWithSidecarObjs(name, ns string, mongotReady bool) []client.Object {
	mdbsh := newShardedForSearch(name)
	mdbsh.Namespace = ns
	mdbsh.Status.Phase = mongodbv1alpha1.ShardedPhaseRunning
	syncSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-sync", Namespace: ns},
		Data:       map[string][]byte{"username": []byte("search-sync"), "password": []byte("x")},
	}
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: mongodbv1beta1.MongoDBSearchSpec{
			Source:            mongodbv1beta1.SearchSource{MongoDBResourceRef: &corev1.LocalObjectReference{Name: name}, Kind: "MongoDBSharded"},
			SyncUserSecretRef: &corev1.LocalObjectReference{Name: name + "-sync"},
		},
	}
	objs := []client.Object{mdbsh, search, syncSecret}
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		shardName := fmt.Sprintf("%s-shard-%d", name, i)
		sts := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: shardName, Namespace: ns},
			Spec:       appsv1.StatefulSetSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": shardName}}},
		}
		objs = append(objs, sts, podWithMongot(shardName+"-0", ns, shardName, true, mongotReady))
	}
	return objs
}

// TestReconcileSharded_ReadyWhenSidecarsReady — cluster Running + 모든 shard mongot pod ready →
// Phase=Ready + ReadyReplicas=shard 수(이슈1/2 가드: sidecar 실제 readiness 집계).
func TestReconcileSharded_ReadyWhenSidecarsReady(t *testing.T) {
	s := newTestScheme(t)
	const name, ns = "kshr", "default"
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(shardedWithSidecarObjs(name, ns, true)...).
		WithStatusSubresource(&mongodbv1beta1.MongoDBSearch{}, &mongodbv1alpha1.MongoDBSharded{}).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}

	reconcileSearch(t, r, name)
	got := &mongodbv1beta1.MongoDBSearch{}
	_ = cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: ns}, got)
	if got.Status.Phase != searchPhaseReady {
		t.Errorf("Phase=%q want Ready(모든 shard mongot ready)", got.Status.Phase)
	}
	if got.Status.ReadyReplicas != 3 {
		t.Errorf("ReadyReplicas=%d want 3(shard 수)", got.Status.ReadyReplicas)
	}
}

// TestReconcileSharded_NotReadyWhenSidecarsNotReady — cluster Running 이지만 mongot pod not ready →
// Phase != Ready(이슈1 가드: 조기 Ready 승격 시 SearchIndex 가 mongot 미준비 상태에서 인덱스 조기 시도).
func TestReconcileSharded_NotReadyWhenSidecarsNotReady(t *testing.T) {
	s := newTestScheme(t)
	const name, ns = "kshd", "default"
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(shardedWithSidecarObjs(name, ns, false)...).
		WithStatusSubresource(&mongodbv1beta1.MongoDBSearch{}, &mongodbv1alpha1.MongoDBSharded{}).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}

	reconcileSearch(t, r, name)
	got := &mongodbv1beta1.MongoDBSearch{}
	_ = cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: ns}, got)
	if got.Status.Phase == searchPhaseReady {
		t.Error("Phase=Ready 인데 mongot sidecar 미준비 — 조기 승격(SearchIndex 조기 인덱스 시도 유발)")
	}
	if got.Status.ReadyReplicas != 0 {
		t.Errorf("ReadyReplicas=%d want 0(ready sidecar 없음)", got.Status.ReadyReplicas)
	}
}

// TestReconcileSharded_NotFound — source MongoDBSharded 부재 → pending(no-op, 에러 없음).
func TestReconcileSharded_NotFound(t *testing.T) {
	s := newTestScheme(t)
	const name = "ksh2"
	search := &mongodbv1beta1.MongoDBSearch{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: mongodbv1beta1.MongoDBSearchSpec{
			Source: mongodbv1beta1.SearchSource{MongoDBResourceRef: &corev1.LocalObjectReference{Name: name}, Kind: "MongoDBSharded"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(search).WithStatusSubresource(search).Build()
	r := &MongoDBSearchReconciler{Client: cl, Scheme: s}

	reconcileSearch(t, r, name)
	got := &mongodbv1beta1.MongoDBSearch{}
	_ = cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, got)
	if got.Status.Phase != searchPhasePending {
		t.Errorf("Phase=%q want Pending(source 부재)", got.Status.Phase)
	}
}
