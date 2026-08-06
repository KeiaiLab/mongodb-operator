/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// pvc_autoexpand_test.go — reconcilePVCAutoExpansion 배선 회귀 가드.
//
// fake client + fake usage reader(seam)로 네트워크/실 mongod 없이 배선 로직만
// 검증한다. 사용률 임계 판정(PlanPVCExpansion)은 autopilot_test.go 가, 실 PVC
// patch(grow-only/allowVolumeExpansion)는 keiailab-commons pkg/pvc 가 담당한다.
package controller

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// fakePVCUsageReader 는 usagePercent seam 의 테스트 대역.
type fakePVCUsageReader struct {
	pct    int32
	ok     bool
	err    error
	called bool
}

func (f *fakePVCUsageReader) usagePercent(_ context.Context, _ *mongodbv1alpha1.MongoDB) (int32, bool, error) {
	f.called = true
	return f.pct, f.ok, f.err
}

func expandableSC(name string) *storagev1.StorageClass {
	allow := true
	return &storagev1.StorageClass{
		ObjectMeta:           metav1.ObjectMeta{Name: name},
		AllowVolumeExpansion: &allow,
	}
}

// dataPVC 는 data-<sts>-<ordinal> 규칙의 Bound PVC 를 만든다.
func dataPVC(name, ns, scName string, reqGi, capGi int64, conds ...corev1.PersistentVolumeClaimConditionType) *corev1.PersistentVolumeClaim {
	sc := scName
	p := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &sc,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", reqGi))},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase:    corev1.ClaimBound,
			Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", capGi))},
		},
	}
	for _, c := range conds {
		p.Status.Conditions = append(p.Status.Conditions,
			corev1.PersistentVolumeClaimCondition{Type: c, Status: corev1.ConditionTrue})
	}
	return p
}

func mdbWithAutoHealing(name, ns string, spec *mongodbv1alpha1.AutoHealingSpec) *mongodbv1alpha1.MongoDB {
	return &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:     3,
			AutoHealing: spec,
		},
	}
}

// pvcRequestGi 는 fake client 에서 PVC 요청 용량(GiB)을 읽는다.
func pvcRequestGi(t *testing.T, cl client.Client, ns, name string) int64 {
	t.Helper()
	p := &corev1.PersistentVolumeClaim{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: ns}, p); err != nil {
		t.Fatalf("get pvc %s: %v", name, err)
	}
	q := p.Spec.Resources.Requests[corev1.ResourceStorage]
	return q.Value() >> 30
}

func TestReconcilePVCAutoExpansion_ExpandsWhenAboveThreshold(t *testing.T) {
	s := newTestScheme(t)
	mdb := mdbWithAutoHealing("rs", "ns", &mongodbv1alpha1.AutoHealingSpec{
		Enabled: true, PVCExpansionUsagePercent: 85, PVCExpansionIncrementGi: 10,
	})
	pvc := dataPVC("data-rs-0", "ns", "expandable", 10, 10)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(mdb, pvc, expandableSC("expandable")).Build()

	reader := &fakePVCUsageReader{pct: 92, ok: true}
	r := &MongoDBReconciler{Client: cl, Scheme: s, PVCUsage: reader}

	if err := r.reconcilePVCAutoExpansion(context.Background(), mdb); err != nil {
		t.Fatalf("reconcilePVCAutoExpansion: %v", err)
	}
	if !reader.called {
		t.Fatalf("usage reader must be called when enabled")
	}
	if got := pvcRequestGi(t, cl, "ns", "data-rs-0"); got != 20 {
		t.Fatalf("PVC must grow 10Gi→20Gi (current+increment), got %dGi", got)
	}
}

func TestReconcilePVCAutoExpansion_DisabledNoop(t *testing.T) {
	s := newTestScheme(t)
	// AutoHealing nil → 완전 no-op (측정조차 하지 않음).
	mdb := mdbWithAutoHealing("rs", "ns", nil)
	pvc := dataPVC("data-rs-0", "ns", "expandable", 10, 10)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(mdb, pvc, expandableSC("expandable")).Build()

	reader := &fakePVCUsageReader{pct: 99, ok: true}
	r := &MongoDBReconciler{Client: cl, Scheme: s, PVCUsage: reader}

	if err := r.reconcilePVCAutoExpansion(context.Background(), mdb); err != nil {
		t.Fatalf("reconcilePVCAutoExpansion: %v", err)
	}
	if reader.called {
		t.Fatalf("disabled spec must not measure usage")
	}
	if got := pvcRequestGi(t, cl, "ns", "data-rs-0"); got != 10 {
		t.Fatalf("disabled must not expand, got %dGi", got)
	}
}

func TestReconcilePVCAutoExpansion_BelowThresholdNoop(t *testing.T) {
	s := newTestScheme(t)
	mdb := mdbWithAutoHealing("rs", "ns", &mongodbv1alpha1.AutoHealingSpec{
		Enabled: true, PVCExpansionUsagePercent: 85, PVCExpansionIncrementGi: 10,
	})
	pvc := dataPVC("data-rs-0", "ns", "expandable", 10, 10)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(mdb, pvc, expandableSC("expandable")).Build()

	reader := &fakePVCUsageReader{pct: 50, ok: true}
	r := &MongoDBReconciler{Client: cl, Scheme: s, PVCUsage: reader}

	if err := r.reconcilePVCAutoExpansion(context.Background(), mdb); err != nil {
		t.Fatalf("reconcilePVCAutoExpansion: %v", err)
	}
	if got := pvcRequestGi(t, cl, "ns", "data-rs-0"); got != 10 {
		t.Fatalf("usage below threshold must not expand, got %dGi", got)
	}
}

func TestReconcilePVCAutoExpansion_ResizeInFlightSkips(t *testing.T) {
	s := newTestScheme(t)
	mdb := mdbWithAutoHealing("rs", "ns", &mongodbv1alpha1.AutoHealingSpec{
		Enabled: true, PVCExpansionUsagePercent: 85, PVCExpansionIncrementGi: 10,
	})
	// 요청 20Gi > 용량 10Gi = 이전 확장이 아직 진행 중 → 중복 확장 차단.
	pvc := dataPVC("data-rs-0", "ns", "expandable", 20, 10)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(mdb, pvc, expandableSC("expandable")).Build()

	reader := &fakePVCUsageReader{pct: 99, ok: true}
	r := &MongoDBReconciler{Client: cl, Scheme: s, PVCUsage: reader}

	if err := r.reconcilePVCAutoExpansion(context.Background(), mdb); err != nil {
		t.Fatalf("reconcilePVCAutoExpansion: %v", err)
	}
	if reader.called {
		t.Fatalf("resize-in-flight must skip before measuring usage (runaway 차단)")
	}
	if got := pvcRequestGi(t, cl, "ns", "data-rs-0"); got != 20 {
		t.Fatalf("resize-in-flight must not stack expansion, got %dGi (want 20)", got)
	}
}

func TestReconcilePVCAutoExpansion_UsageUnavailableNoop(t *testing.T) {
	s := newTestScheme(t)
	mdb := mdbWithAutoHealing("rs", "ns", &mongodbv1alpha1.AutoHealingSpec{
		Enabled: true, PVCExpansionUsagePercent: 85, PVCExpansionIncrementGi: 10,
	})
	pvc := dataPVC("data-rs-0", "ns", "expandable", 10, 10)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(mdb, pvc, expandableSC("expandable")).Build()

	// ok=false + err → best-effort skip (에러 전파 없음, PVC 불변).
	reader := &fakePVCUsageReader{ok: false, err: fmt.Errorf("mongod unreachable")}
	r := &MongoDBReconciler{Client: cl, Scheme: s, PVCUsage: reader}

	if err := r.reconcilePVCAutoExpansion(context.Background(), mdb); err != nil {
		t.Fatalf("usage unavailable must be best-effort (no error), got: %v", err)
	}
	if got := pvcRequestGi(t, cl, "ns", "data-rs-0"); got != 10 {
		t.Fatalf("usage unavailable must not expand, got %dGi", got)
	}
}

func TestCurrentDataPVCSizeGi_MaxAndResizeDetection(t *testing.T) {
	s := newTestScheme(t)
	mdb := mdbWithAutoHealing("rs", "ns", &mongodbv1alpha1.AutoHealingSpec{Enabled: true})
	// data-rs-0=10Gi(bound), data-rs-1=15Gi(bound) → max 15. 관련 없는 PVC 무시.
	p0 := dataPVC("data-rs-0", "ns", "expandable", 10, 10)
	p1 := dataPVC("data-rs-1", "ns", "expandable", 15, 15)
	other := dataPVC("data-other-0", "ns", "expandable", 99, 99)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mdb, p0, p1, other).Build()
	r := &MongoDBReconciler{Client: cl, Scheme: s}

	gi, resizing, found, err := r.currentDataPVCSizeGi(context.Background(), mdb)
	if err != nil {
		t.Fatalf("currentDataPVCSizeGi: %v", err)
	}
	if !found {
		t.Fatalf("data PVC must be found")
	}
	if gi != 15 {
		t.Fatalf("max data PVC size must be 15Gi, got %d", gi)
	}
	if resizing {
		t.Fatalf("no resize in flight expected")
	}

	// resize 컨디션 있는 경우.
	p2 := dataPVC("data-rs-0", "ns", "expandable", 10, 10, corev1.PersistentVolumeClaimFileSystemResizePending)
	cl2 := fake.NewClientBuilder().WithScheme(s).WithObjects(mdb, p2).Build()
	r2 := &MongoDBReconciler{Client: cl2, Scheme: s}
	_, resizing2, _, err := r2.currentDataPVCSizeGi(context.Background(), mdb)
	if err != nil {
		t.Fatalf("currentDataPVCSizeGi(2): %v", err)
	}
	if !resizing2 {
		t.Fatalf("FileSystemResizePending condition must set resizing=true")
	}
}
