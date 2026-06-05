/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// federation_validation_test.go — Phase 5.2 ValidateFederationRegions 회귀 가드.

package controller

import (
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestValidateFederationRegions(t *testing.T) {
	t.Parallel()
	r := func(name, prio string) mongodbv1alpha1.FederationRegion {
		return mongodbv1alpha1.FederationRegion{Name: name, Priority: prio}
	}
	cases := []struct {
		name    string
		regions []mongodbv1alpha1.FederationRegion
		wantErr bool
	}{
		{"정상 2개 region", []mongodbv1alpha1.FederationRegion{r("a", "1.0"), r("b", "2.0")}, false},
		{"정상 primary+secondary(0)", []mongodbv1alpha1.FederationRegion{r("a", "1.0"), r("b", "0")}, false},
		{"빈 priority(default 1.0) 유효", []mongodbv1alpha1.FederationRegion{r("a", ""), r("b", "1.0")}, false},
		{"경계 priority 1000 허용", []mongodbv1alpha1.FederationRegion{r("a", "1000"), r("b", "1.0")}, false},
		{"region 1개 → 거부", []mongodbv1alpha1.FederationRegion{r("a", "1.0")}, true},
		{"region 0개 → 거부", []mongodbv1alpha1.FederationRegion{}, true},
		{"name 중복 → 거부", []mongodbv1alpha1.FederationRegion{r("a", "1.0"), r("a", "2.0")}, true},
		{"priority 숫자 아님 → 거부", []mongodbv1alpha1.FederationRegion{r("a", "abc"), r("b", "1.0")}, true},
		{"priority trailing garbage → 거부", []mongodbv1alpha1.FederationRegion{r("a", "1.5x"), r("b", "1.0")}, true},
		{"priority 음수 → 거부", []mongodbv1alpha1.FederationRegion{r("a", "-1"), r("b", "1.0")}, true},
		{"priority 1000 초과 → 거부", []mongodbv1alpha1.FederationRegion{r("a", "1000.1"), r("b", "1.0")}, true},
		{"모두 priority 0 → primary 없음 거부", []mongodbv1alpha1.FederationRegion{r("a", "0"), r("b", "0")}, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateFederationRegions(tc.regions)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateFederationRegions = %v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
