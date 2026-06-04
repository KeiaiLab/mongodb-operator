/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mtls_phase.go — Phase 5.4-c2: mTLS rolling 전환 reconcile 배선.
// security.ResolveMTLSStep (안전 단계 결정, 단위 테스트) 를 reconcile 루프에 연결.

package controller

import (
	"context"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	securitypkg "github.com/keiailab/mongodb-operator/internal/security"
)

// reconcileMTLSPhase advances Status.MTLSPhase one safe step toward
// spec.tls.mtls.mode and mutates the in-memory effective clusterAuthMode so the
// StatefulSet built later this reconcile uses the resolved step (not a jump to
// the final mode). The StatefulSet watch re-triggers reconcile when each phase
// finishes rolling out, advancing the transition keyFile→sendKeyFile→sendX509→x509.
//
// No-op when mTLS is disabled (default off → zero impact on existing clusters).
// Must run BEFORE reconcileStatefulSet so the mutated mode reaches the builder.
func (r *MongoDBReconciler) reconcileMTLSPhase(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	if mdb.Spec.TLS == nil || mdb.Spec.TLS.MTLS == nil || !mdb.Spec.TLS.MTLS.Enabled {
		if mdb.Status.MTLSPhase != "" {
			mdb.Status.MTLSPhase = ""
			return updateStatusWithRetry(ctx, r.Client, mdb, func() { mdb.Status.MTLSPhase = "" })
		}
		return nil
	}

	desired := mdb.Spec.TLS.MTLS.Mode
	if desired == "" {
		desired = "x509"
	}

	// 현재 phase 가 전부 rollout 됐는지 — 아직이면 ResolveMTLSStep 이 현 단계 유지(점프 금지).
	// StatefulSet 미존재(최초 생성) 등 에러는 not-ready 로 처리 → 안전 시작 단계부터.
	rolloutReady, err := r.areAllPodsReady(ctx, mdb)
	if err != nil {
		rolloutReady = false
	}

	apply, _ := securitypkg.ResolveMTLSStep(desired, mdb.Status.MTLSPhase, rolloutReady)

	// Status.MTLSPhase 갱신: updateStatusWithRetry 는 첫 Update 전에 mutate 를 적용하지
	// 않으므로(conflict 시에만) 호출 전 직접 set (setInitializing 패턴 정합).
	if mdb.Status.MTLSPhase != apply {
		mdb.Status.MTLSPhase = apply
		if err := updateStatusWithRetry(ctx, r.Client, mdb, func() { mdb.Status.MTLSPhase = apply }); err != nil {
			return err
		}
	}

	// effective mode 는 status Update *이후* 설정한다 — Status().Update 가 in-memory
	// Spec 을 stored(desired) 로 되돌릴 수 있어, 그 전에 설정하면 build 가 desired 로
	// 점프해버린다 (TDD 로 포착). 이 값은 in-memory only (stored spec 은 불변).
	mdb.Spec.TLS.MTLS.Mode = apply
	return nil
}
