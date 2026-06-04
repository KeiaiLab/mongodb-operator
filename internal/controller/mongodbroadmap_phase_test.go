/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mongodbroadmap_phase_test.go — 로드맵 신규 컨트롤러 clustergroup 의
// 순수 phase 집계 함수 회귀 가드. envtest 불요 (Status 입력 → phase 출력 결정함수).
// ROADMAP §1.2 [x] 판정의 "회귀 가드" 충족용.
// (federation 의 computeFederationPhase 는 mongodbfederation_unit_test.go 에 기존 커버됨)

package controller

import (
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func clusterGroupWithMemberPhases(phases ...string) *mongodbv1alpha1.MongoDBClusterGroup {
	g := &mongodbv1alpha1.MongoDBClusterGroup{}
	for _, p := range phases {
		g.Status.MemberStatuses = append(g.Status.MemberStatuses,
			mongodbv1alpha1.ClusterGroupMemberStatus{Phase: p})
	}
	return g
}

func TestComputeClusterGroupPhase(t *testing.T) {
	cases := []struct {
		name   string
		phases []string
		want   string
	}{
		{"멤버상태_없으면_Pending", nil, groupPhasePending},
		{"전부_Synced면_Synced", []string{groupPhaseSynced, groupPhaseSynced}, groupPhaseSynced},
		{"하나라도_Failed면_Degraded", []string{groupPhaseSynced, backupPhaseFailed}, groupPhaseDegraded},
		{"일부만_Synced면_Reconciling", []string{groupPhaseSynced, groupPhaseReconciling}, groupPhaseReconciling},
		{"Failed가_Synced보다_우선", []string{backupPhaseFailed, groupPhaseSynced, groupPhaseSynced}, groupPhaseDegraded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeClusterGroupPhase(clusterGroupWithMemberPhases(tc.phases...))
			if got != tc.want {
				t.Errorf("computeClusterGroupPhase(%v) = %q, want %q", tc.phases, got, tc.want)
			}
		})
	}
}

