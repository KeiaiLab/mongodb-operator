/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mongodbsharded_mtls_phase.go — Phase 5.4-c2 sharded sister: MongoDBSharded 의
// mTLS rolling 전환 reconcile 배선. MongoDB(RS) 의 mtls_phase.go 와 동일 패턴.

package controller

import (
	"context"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	securitypkg "github.com/keiailab/mongodb-operator/internal/security"
)

// reconcileMTLSPhase advances Status.MTLSPhase one safe step toward
// spec.tls.mtls.mode for a sharded cluster, mutating the in-memory effective
// clusterAuthMode so the config server / shard / mongos StatefulSets built later
// this reconcile use the resolved step. rolloutReady requires ALL components
// (cfg + shards + mongos) ready — a single component lagging holds the phase
// (never jump). No-op when mTLS disabled (default off → zero impact).
// Must run BEFORE the component reconcilers so the mutated mode reaches builders.
func (r *MongoDBShardedReconciler) reconcileMTLSPhase(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	if mdbsh.Spec.TLS == nil || mdbsh.Spec.TLS.MTLS == nil || !mdbsh.Spec.TLS.MTLS.Enabled {
		if mdbsh.Status.MTLSPhase != "" {
			mdbsh.Status.MTLSPhase = ""
			return updateStatusWithRetry(ctx, r.Client, mdbsh, func() { mdbsh.Status.MTLSPhase = "" })
		}
		return nil
	}

	desired := mdbsh.Spec.TLS.MTLS.Mode
	if desired == "" {
		desired = "x509"
	}

	// 전 컴포넌트 rollout 완료 시에만 다음 단계 — 하나라도 미완이면 현 단계 유지.
	rolloutReady := r.isConfigServerReady(ctx, mdbsh) &&
		r.areShardsReady(ctx, mdbsh) &&
		r.isMongosReady(ctx, mdbsh)

	apply, _ := securitypkg.ResolveMTLSStep(desired, mdbsh.Status.MTLSPhase, rolloutReady)

	// Status.MTLSPhase 갱신을 effective mode 설정보다 *먼저* (Status().Update 가
	// in-memory Spec 을 stored 로 되돌려 점프시키는 버그 회피 — RS 5.4-c2 에서 포착).
	if mdbsh.Status.MTLSPhase != apply {
		mdbsh.Status.MTLSPhase = apply
		if err := updateStatusWithRetry(ctx, r.Client, mdbsh, func() { mdbsh.Status.MTLSPhase = apply }); err != nil {
			return err
		}
	}
	mdbsh.Spec.TLS.MTLS.Mode = apply // in-memory only — 이번 reconcile 의 builder 가 사용
	return nil
}
