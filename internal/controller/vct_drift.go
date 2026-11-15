/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package controller

import (
	"context"
	"fmt"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonsreconcile "github.com/keiailab/keiailab-commons/pkg/reconcile"
	commonsstatus "github.com/keiailab/keiailab-commons/pkg/status"
)

const (
	conditionVCTRecreated = "VolumeClaimTemplatesRecreated"
	reasonVCTRecreate     = "VolumeClaimTemplateRecreate"
	reasonStorageDrift    = "StorageDrift"
)

// vctState tells the caller whether to apply the StatefulSet this round.
type vctState int

const (
	// vctApply: no drift (or no live object) — apply as usual.
	vctApply vctState = iota
	// vctWait: the live object was just orphan-deleted or is still
	// terminating — skip apply, the next reconcile creates it.
	vctWait
)

// vctSync is one StatefulSet's volumeClaimTemplate check.
type vctSync struct {
	client   client.Client
	recorder events.EventRecorder
	owner    commonsreconcile.Statusable
	desired  *appsv1.StatefulSet
	preserve bool
}

// syncVCT recreates the live StatefulSet when its volumeClaimTemplates
// differ from desired in storage class or size. volumeClaimTemplates are
// immutable, so the only way is delete + create:
//
//	live VCT == desired         -> vctApply
//	class or size differs       -> delete (orphan) -> vctWait -> next round creates
//	name / accessModes / count  -> error (fail-closed, nothing deleted)
//	preserved replicas != CR    -> error (recreate would lose them)
//
// Orphan propagation keeps pods and PVCs; the new StatefulSet adopts them.
// Existing PVCs keep their class — only new members get the new template.
func syncVCT(ctx context.Context, s vctSync) (vctState, error) {
	live := &appsv1.StatefulSet{}
	err := s.client.Get(ctx, client.ObjectKeyFromObject(s.desired), live)
	if apierrors.IsNotFound(err) {
		return vctApply, nil
	}
	if err != nil {
		return vctApply, err
	}
	if live.DeletionTimestamp != nil {
		return vctWait, nil
	}

	drift, err := vctDrift(live.Spec.VolumeClaimTemplates, s.desired.Spec.VolumeClaimTemplates)
	if err != nil {
		return vctApply, fmt.Errorf("statefulset %s: %w", live.Name, err)
	}
	if drift == "" {
		return vctApply, nil
	}
	if s.preserve && !equalReplicas(live.Spec.Replicas, s.desired.Spec.Replicas) {
		return vctApply, fmt.Errorf("statefulset %s: %s, but preserved replicas %d differ from spec %d — not recreating",
			live.Name, drift, replicasOf(live.Spec.Replicas), replicasOf(s.desired.Spec.Replicas))
	}

	return vctWait, s.recreate(ctx, live, drift)
}

func (s vctSync) recreate(ctx context.Context, live *appsv1.StatefulSet, drift string) error {
	err := s.client.Delete(ctx, live,
		client.PropagationPolicy(metav1.DeletePropagationOrphan),
		client.Preconditions{UID: &live.UID, ResourceVersion: &live.ResourceVersion})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("orphan-delete statefulset %s: %w", live.Name, err)
	}

	msg := fmt.Sprintf("statefulset %s recreated (orphan, pods and PVCs kept): %s", live.Name, drift)
	log.FromContext(ctx).Info(msg)
	if s.recorder != nil {
		s.recorder.Eventf(s.owner, nil, corev1.EventTypeNormal, reasonVCTRecreate, reasonVCTRecreate, "%s", msg)
	}

	setCondition := func() {
		meta.SetStatusCondition(s.owner.GetConditions(), metav1.Condition{
			Type:    conditionVCTRecreated,
			Status:  metav1.ConditionTrue,
			Reason:  reasonStorageDrift,
			Message: msg,
		})
	}
	setCondition()
	return commonsstatus.UpdateWithRetry(ctx, s.client, s.owner, setCondition)
}

// vctDrift compares templates by name. It returns a description of the
// class / size drift ("" = none) or an error when the shape differs.
func vctDrift(live, desired []corev1.PersistentVolumeClaim) (string, error) {
	if len(live) != len(desired) {
		return "", fmt.Errorf("volumeClaimTemplates count %d differs from %d — refusing to recreate", len(live), len(desired))
	}

	drift := ""
	for i := range desired {
		want := &desired[i]
		got := findVCT(live, want.Name)
		if got == nil {
			return "", fmt.Errorf("volumeClaimTemplate %q missing on live object — refusing to recreate", want.Name)
		}
		if !slices.Equal(got.Spec.AccessModes, want.Spec.AccessModes) {
			return "", fmt.Errorf("volumeClaimTemplate %q accessModes %v differ from %v — refusing to recreate",
				want.Name, got.Spec.AccessModes, want.Spec.AccessModes)
		}
		drift += templateDrift(got, want)
	}
	return drift, nil
}

func templateDrift(got, want *corev1.PersistentVolumeClaim) string {
	drift := ""
	gotClass, wantClass := classOf(got), classOf(want)
	if gotClass != wantClass {
		drift += fmt.Sprintf("%s storageClassName %s -> %s; ", want.Name, gotClass, wantClass)
	}
	gotSize := got.Spec.Resources.Requests[corev1.ResourceStorage]
	wantSize := want.Spec.Resources.Requests[corev1.ResourceStorage]
	if gotSize.Cmp(wantSize) != 0 {
		drift += fmt.Sprintf("%s size %s -> %s; ", want.Name, gotSize.String(), wantSize.String())
	}
	return drift
}

func findVCT(templates []corev1.PersistentVolumeClaim, name string) *corev1.PersistentVolumeClaim {
	for i := range templates {
		if templates[i].Name == name {
			return &templates[i]
		}
	}
	return nil
}

// classOf renders nil (cluster default) and "" (no class) distinctly.
func classOf(pvc *corev1.PersistentVolumeClaim) string {
	if pvc.Spec.StorageClassName == nil {
		return "<default>"
	}
	return fmt.Sprintf("%q", *pvc.Spec.StorageClassName)
}

func equalReplicas(a, b *int32) bool {
	return replicasOf(a) == replicasOf(b)
}

// replicasOf follows the API default of 1 for a nil replicas field.
func replicasOf(r *int32) int32 {
	if r == nil {
		return 1
	}
	return *r
}
