/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// federation_failover_test.go — Phase 5.2 DecideFederationPrimary 회귀 가드.
// envtest 불요 (region/status 입력 → primary 결정 순수함수).

package controller

import (
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func fedRegion(name, priority string) mongodbv1alpha1.FederationRegion {
	return mongodbv1alpha1.FederationRegion{Name: name, Priority: priority}
}
func fedStatus(name, phase string) mongodbv1alpha1.FederationRegionStatus {
	return mongodbv1alpha1.FederationRegionStatus{Name: name, Phase: phase}
}

func TestDecideFederationPrimary(t *testing.T) {
	t.Parallel()
	S, F, D := federationPhaseSynced, federationPhaseFailed, federationPhaseDegraded

	cases := []struct {
		name        string
		regions     []mongodbv1alpha1.FederationRegion
		statuses    []mongodbv1alpha1.FederationRegionStatus
		wantPrimary string
	}{
		{
			"높은 priority 선출",
			[]mongodbv1alpha1.FederationRegion{fedRegion("a", "1.0"), fedRegion("b", "2.0")},
			[]mongodbv1alpha1.FederationRegionStatus{fedStatus("a", S), fedStatus("b", S)},
			"b",
		},
		{
			"동점 → name 사전순",
			[]mongodbv1alpha1.FederationRegion{fedRegion("z", "2.0"), fedRegion("a", "2.0")},
			[]mongodbv1alpha1.FederationRegionStatus{fedStatus("z", S), fedStatus("a", S)},
			"a",
		},
		{
			"Synced 만 후보 (다른 건 Failed)",
			[]mongodbv1alpha1.FederationRegion{fedRegion("a", "5.0"), fedRegion("b", "1.0")},
			[]mongodbv1alpha1.FederationRegionStatus{fedStatus("a", F), fedStatus("b", S)},
			"b",
		},
		{
			"priority 0 은 primary 제외",
			[]mongodbv1alpha1.FederationRegion{fedRegion("a", "0"), fedRegion("b", "1.0")},
			[]mongodbv1alpha1.FederationRegionStatus{fedStatus("a", S), fedStatus("b", S)},
			"b",
		},
		{
			"모든 region Degraded → primary 없음",
			[]mongodbv1alpha1.FederationRegion{fedRegion("a", "1.0"), fedRegion("b", "2.0")},
			[]mongodbv1alpha1.FederationRegionStatus{fedStatus("a", D), fedStatus("b", D)},
			"",
		},
		{
			"모든 Synced 가 priority 0 → primary 없음",
			[]mongodbv1alpha1.FederationRegion{fedRegion("a", "0"), fedRegion("b", "0.0")},
			[]mongodbv1alpha1.FederationRegionStatus{fedStatus("a", S), fedStatus("b", S)},
			"",
		},
		{
			"빈 priority → default 1.0 (후보)",
			[]mongodbv1alpha1.FederationRegion{fedRegion("a", ""), fedRegion("b", "0")},
			[]mongodbv1alpha1.FederationRegionStatus{fedStatus("a", S), fedStatus("b", S)},
			"a",
		},
		{
			"파싱 불가 priority → default 1.0",
			[]mongodbv1alpha1.FederationRegion{fedRegion("a", "abc"), fedRegion("b", "0")},
			[]mongodbv1alpha1.FederationRegionStatus{fedStatus("a", S), fedStatus("b", S)},
			"a",
		},
		{
			"단일 region Synced",
			[]mongodbv1alpha1.FederationRegion{fedRegion("solo", "1.0")},
			[]mongodbv1alpha1.FederationRegionStatus{fedStatus("solo", S)},
			"solo",
		},
		{
			"status 없는 region skip",
			[]mongodbv1alpha1.FederationRegion{fedRegion("a", "9.0"), fedRegion("b", "1.0")},
			[]mongodbv1alpha1.FederationRegionStatus{fedStatus("b", S)}, // a 의 status 부재
			"b",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DecideFederationPrimary(tc.regions, tc.statuses)
			if got.PrimaryRegion != tc.wantPrimary {
				t.Errorf("DecideFederationPrimary = %q (eligible=%v, reason=%q), want primary %q",
					got.PrimaryRegion, got.EligibleRegions, got.Reason, tc.wantPrimary)
			}
		})
	}
}

func TestDecideFederationPrimary_EligibleSorted(t *testing.T) {
	t.Parallel()
	regions := []mongodbv1alpha1.FederationRegion{fedRegion("z", "1.0"), fedRegion("a", "0"), fedRegion("m", "2.0")}
	statuses := []mongodbv1alpha1.FederationRegionStatus{
		fedStatus("z", federationPhaseSynced), fedStatus("a", federationPhaseSynced), fedStatus("m", federationPhaseSynced),
	}
	got := DecideFederationPrimary(regions, statuses)
	want := []string{"a", "m", "z"} // 전부 Synced → name 사전순 (priority 0 포함)
	if len(got.EligibleRegions) != len(want) {
		t.Fatalf("eligible = %v, want %v", got.EligibleRegions, want)
	}
	for i := range want {
		if got.EligibleRegions[i] != want[i] {
			t.Errorf("eligible[%d] = %q, want %q", i, got.EligibleRegions[i], want[i])
		}
	}
	if got.PrimaryRegion != "m" {
		t.Errorf("primary = %q, want m (priority 2.0 최고)", got.PrimaryRegion)
	}
}
