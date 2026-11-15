/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// VolumeClaimTemplate drift — STS 의 VCT storageClassName · 크기가 CR 과 다르면
// STS 를 orphan 으로 지우고 다시 만든다(pod · PVC 무접촉). fake client 만 쓴다.

package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

const (
	vctName        = "vct"
	vctNS          = "default"
	vctOldClass    = "ceph-rbd"
	vctNewClass    = "local-nvme"
	vctOldSize     = "1Ti"
	vctNewSize     = "600Gi"
	vctShard0      = vctName + "-shard-0"
	vctCfg         = vctName + "-cfg"
	vctCondition   = "VolumeClaimTemplatesRecreated"
	vctEventReason = "VolumeClaimTemplateRecreate"
)

type vctProbe struct {
	deletes []client.DeleteOptions
}

func vctSharded(class, size string) *mongodbv1alpha1.MongoDBSharded {
	mdbsh := newAdoptedSharded(vctName, "9.0.8")
	mdbsh.UID = "sharded-uid"
	mdbsh.Spec.Shards.Count = 1
	mdbsh.Spec.Shards.Storage = mongodbv1alpha1.StorageSpec{StorageClassName: class, Size: resource.MustParse(size)}
	mdbsh.Spec.ConfigServer.Storage = mongodbv1alpha1.StorageSpec{StorageClassName: class, Size: resource.MustParse("20Gi")}
	return mdbsh
}

func liveSTS(sts *appsv1.StatefulSet) *appsv1.StatefulSet {
	sts.UID = types.UID(sts.Name + "-uid")
	sts.CreationTimestamp = metav1.Now()
	return sts
}

// memberObjects 는 STS 가 만든 pod · PVC 를 흉내 낸다(controllerRef = STS).
func memberObjects(sts *appsv1.StatefulSet) []client.Object {
	ref := metav1.OwnerReference{
		APIVersion: "apps/v1", Kind: "StatefulSet", Name: sts.Name, UID: sts.UID, Controller: ptrBool(true),
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: sts.Name + "-0", Namespace: vctNS, OwnerReferences: []metav1.OwnerReference{ref},
	}}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name: "data-" + sts.Name + "-0", Namespace: vctNS,
	}}
	return []client.Object{pod, pvc}
}

func newVCTClient(t *testing.T, probe *vctProbe, objs ...client.Object) client.Client {
	t.Helper()
	funcs := interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, ok := obj.(*appsv1.StatefulSet); ok {
				o := client.DeleteOptions{}
				o.ApplyOptions(opts)
				probe.deletes = append(probe.deletes, o)
			}
			return c.Delete(ctx, obj, opts...)
		},
	}
	return fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&mongodbv1alpha1.MongoDBSharded{}, &mongodbv1alpha1.MongoDB{}).
		WithInterceptorFuncs(funcs).
		Build()
}

func getVCTSet(t *testing.T, cl client.Client, name string) (*appsv1.StatefulSet, bool) {
	t.Helper()
	sts := &appsv1.StatefulSet{}
	err := cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: vctNS}, sts)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("get statefulset %s: %v", name, err)
	}
	return sts, true
}

func requireVCT(t *testing.T, sts *appsv1.StatefulSet, class, size string) {
	t.Helper()
	vct := sts.Spec.VolumeClaimTemplates[0]
	if vct.Spec.StorageClassName == nil || *vct.Spec.StorageClassName != class {
		t.Fatalf("vct class = %v, want %s", vct.Spec.StorageClassName, class)
	}
	got := vct.Spec.Resources.Requests[corev1.ResourceStorage]
	if got.Cmp(resource.MustParse(size)) != 0 {
		t.Fatalf("vct size = %s, want %s", got.String(), size)
	}
}

func requireOrphanDelete(t *testing.T, probe *vctProbe) {
	t.Helper()
	if len(probe.deletes) != 1 {
		t.Fatalf("statefulset deletes = %d, want 1", len(probe.deletes))
	}
	pp := probe.deletes[0].PropagationPolicy
	if pp == nil || *pp != metav1.DeletePropagationOrphan {
		t.Fatalf("propagation = %v, want Orphan", pp)
	}
	if probe.deletes[0].Preconditions == nil || probe.deletes[0].Preconditions.UID == nil {
		t.Fatalf("delete without UID precondition")
	}
}

func requireMembersKept(t *testing.T, cl client.Client, members []client.Object) {
	t.Helper()
	for _, m := range members {
		cp := m.DeepCopyObject().(client.Object)
		if err := cl.Get(context.Background(), client.ObjectKeyFromObject(m), cp); err != nil {
			t.Fatalf("%T %s gone: %v", m, m.GetName(), err)
		}
	}
}

func TestShardVCTClassDrift_RecreatesOrphan(t *testing.T) {
	old := vctSharded(vctOldClass, vctOldSize)
	live := liveSTS(resources.BuildShardStatefulSet(old, 0))
	members := memberObjects(live)
	mdbsh := vctSharded(vctNewClass, vctNewSize)
	probe := &vctProbe{}
	cl := newVCTClient(t, probe, append([]client.Object{mdbsh, live}, members...)...)
	rec := events.NewFakeRecorder(8)
	r := &MongoDBShardedReconciler{Client: cl, Scheme: cl.Scheme(), Recorder: rec}

	// 1 회: orphan 삭제만, 같은 reconcile 에서 다시 만들지 않는다.
	if err := r.reconcileShard(context.Background(), mdbsh, 0); err != nil {
		t.Fatalf("reconcileShard: %v", err)
	}
	if _, ok := getVCTSet(t, cl, vctShard0); ok {
		t.Fatalf("statefulset still present after drift")
	}
	requireOrphanDelete(t, probe)
	requireMembersKept(t, cl, members)

	select {
	case ev := <-rec.Events:
		if !strings.Contains(ev, vctEventReason) || !strings.Contains(ev, vctNewClass) {
			t.Fatalf("event = %q", ev)
		}
	default:
		t.Fatalf("no event recorded")
	}
	stored := &mongodbv1alpha1.MongoDBSharded{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(mdbsh), stored); err != nil {
		t.Fatal(err)
	}
	if c := meta.FindStatusCondition(stored.Status.Conditions, vctCondition); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("condition %s = %+v", vctCondition, c)
	}

	// 2 회: 새 VCT 로 다시 만든다.
	if err := r.reconcileShard(context.Background(), mdbsh, 0); err != nil {
		t.Fatalf("reconcileShard (recreate): %v", err)
	}
	sts, ok := getVCTSet(t, cl, vctShard0)
	if !ok {
		t.Fatalf("statefulset not recreated")
	}
	requireVCT(t, sts, vctNewClass, vctNewSize)
	requireMembersKept(t, cl, members)
	if len(probe.deletes) != 1 {
		t.Fatalf("deletes = %d after recreate, want 1", len(probe.deletes))
	}
}

func TestShardVCTSizeDrift_Recreates(t *testing.T) {
	live := liveSTS(resources.BuildShardStatefulSet(vctSharded(vctNewClass, vctOldSize), 0))
	mdbsh := vctSharded(vctNewClass, vctNewSize)
	probe := &vctProbe{}
	cl := newVCTClient(t, probe, mdbsh, live)
	r := &MongoDBShardedReconciler{Client: cl, Scheme: cl.Scheme()}

	if err := r.reconcileShard(context.Background(), mdbsh, 0); err != nil {
		t.Fatalf("reconcileShard: %v", err)
	}
	requireOrphanDelete(t, probe)
}

func TestConfigServerVCTClassDrift_Recreates(t *testing.T) {
	live := liveSTS(resources.BuildConfigServerStatefulSet(vctSharded(vctOldClass, vctOldSize)))
	mdbsh := vctSharded(vctNewClass, vctNewSize)
	probe := &vctProbe{}
	cl := newVCTClient(t, probe, mdbsh, live)
	r := &MongoDBShardedReconciler{Client: cl, Scheme: cl.Scheme()}

	if err := r.reconcileConfigServer(context.Background(), mdbsh); err != nil {
		t.Fatalf("reconcileConfigServer: %v", err)
	}
	if _, ok := getVCTSet(t, cl, vctCfg); ok {
		t.Fatalf("cfg statefulset still present after drift")
	}
	requireOrphanDelete(t, probe)
}

func TestReplicaSetVCTClassDrift_Recreates(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: vctNS, UID: "rs-uid"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members: 3,
			Version: mongodbv1alpha1.MongoDBVersion{Version: "9.0.8"},
			Storage: mongodbv1alpha1.StorageSpec{StorageClassName: vctOldClass, Size: resource.MustParse("10Gi")},
		},
	}
	live := liveSTS(resources.BuildReplicaSetStatefulSet(mdb.DeepCopy()))
	mdb.Spec.Storage.StorageClassName = vctNewClass
	probe := &vctProbe{}
	cl := newVCTClient(t, probe, mdb, live)
	r := &MongoDBReconciler{Client: cl, Scheme: cl.Scheme()}

	if err := r.reconcileStatefulSet(context.Background(), mdb); err != nil {
		t.Fatalf("reconcileStatefulSet: %v", err)
	}
	requireOrphanDelete(t, probe)
}

func TestShardVCTNoDrift_NoDelete(t *testing.T) {
	mdbsh := vctSharded(vctNewClass, vctNewSize)
	live := liveSTS(resources.BuildShardStatefulSet(mdbsh, 0))
	probe := &vctProbe{}
	cl := newVCTClient(t, probe, mdbsh, live)
	r := &MongoDBShardedReconciler{Client: cl, Scheme: cl.Scheme()}

	if err := r.reconcileShard(context.Background(), mdbsh, 0); err != nil {
		t.Fatalf("reconcileShard: %v", err)
	}
	if len(probe.deletes) != 0 {
		t.Fatalf("deletes = %d, want 0", len(probe.deletes))
	}
	if _, ok := getVCTSet(t, cl, vctShard0); !ok {
		t.Fatalf("statefulset missing")
	}
}

// 모양(accessModes · VCT 이름)이 다르면 재생성하지 않고 거절한다(fail-closed).
func TestShardVCTShapeDrift_Refuses(t *testing.T) {
	cases := map[string]func(*appsv1.StatefulSet){
		"accessModes": func(s *appsv1.StatefulSet) {
			s.Spec.VolumeClaimTemplates[0].Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
		},
		"name": func(s *appsv1.StatefulSet) { s.Spec.VolumeClaimTemplates[0].Name = "datadir" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			live := liveSTS(resources.BuildShardStatefulSet(vctSharded(vctOldClass, vctOldSize), 0))
			mutate(live)
			mdbsh := vctSharded(vctNewClass, vctNewSize)
			probe := &vctProbe{}
			cl := newVCTClient(t, probe, mdbsh, live)
			r := &MongoDBShardedReconciler{Client: cl, Scheme: cl.Scheme()}

			if err := r.reconcileShard(context.Background(), mdbsh, 0); err == nil {
				t.Fatalf("reconcileShard: want refusal error")
			}
			if len(probe.deletes) != 0 {
				t.Fatalf("deletes = %d, want 0", len(probe.deletes))
			}
			if _, ok := getVCTSet(t, cl, vctShard0); !ok {
				t.Fatalf("statefulset deleted on refusal")
			}
		})
	}
}

// 보존 중인 replicas 가 CR 과 다르면 재생성이 그 값을 잃으므로 거절한다.
func TestShardVCTDrift_PreservedReplicasRefuses(t *testing.T) {
	live := liveSTS(resources.BuildShardStatefulSet(vctSharded(vctOldClass, vctOldSize), 0))
	live.Spec.Replicas = ptr32(4)
	mdbsh := vctSharded(vctNewClass, vctNewSize)
	probe := &vctProbe{}
	cl := newVCTClient(t, probe, mdbsh, live)
	r := &MongoDBShardedReconciler{Client: cl, Scheme: cl.Scheme()}

	if err := r.reconcileShard(context.Background(), mdbsh, 0); err == nil {
		t.Fatalf("reconcileShard: want refusal error")
	}
	if len(probe.deletes) != 0 {
		t.Fatalf("deletes = %d, want 0", len(probe.deletes))
	}
}

// orphan 삭제가 끝나기 전(삭제 중)에는 손대지 않고 기다린다.
func TestShardVCT_TerminatingSetIsLeftAlone(t *testing.T) {
	old := vctSharded(vctOldClass, vctOldSize)
	live := liveSTS(resources.BuildShardStatefulSet(old, 0))
	now := metav1.Now()
	live.DeletionTimestamp = &now
	live.Finalizers = []string{metav1.FinalizerOrphanDependents}
	live.Spec.Template.Labels["probe"] = "untouched"
	mdbsh := vctSharded(vctNewClass, vctNewSize)
	probe := &vctProbe{}
	cl := newVCTClient(t, probe, mdbsh, live)
	r := &MongoDBShardedReconciler{Client: cl, Scheme: cl.Scheme()}

	if err := r.reconcileShard(context.Background(), mdbsh, 0); err != nil {
		t.Fatalf("reconcileShard: %v", err)
	}
	sts, ok := getVCTSet(t, cl, vctShard0)
	if !ok {
		t.Fatalf("terminating statefulset vanished")
	}
	if sts.Spec.Template.Labels["probe"] != "untouched" {
		t.Fatalf("terminating statefulset was updated")
	}
	if len(probe.deletes) != 0 {
		t.Fatalf("deletes = %d, want 0", len(probe.deletes))
	}
}
