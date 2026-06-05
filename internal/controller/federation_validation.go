/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// federation_validation.go — Phase 5.2 federation spec 검증 (순수 함수).
// reconcile/webhook 이 호출하는 region 구성 사전 검증. ValidateZoneName /
// ValidatePITRTarget sister.
package controller

import (
	"fmt"
	"strconv"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// ValidateFederationRegions checks a federation region set is well-formed:
// at least 2 regions, unique names, parseable priorities within [0, 1000], and
// at least one viable primary (priority > 0). Returns the first violation
// (fast-fail, webhook-friendly). Pure function — no side effects.
func ValidateFederationRegions(regions []mongodbv1alpha1.FederationRegion) error {
	if len(regions) < 2 {
		return fmt.Errorf("federation 은 최소 2개 region 필요 (현재 %d개)", len(regions))
	}

	seen := make(map[string]struct{}, len(regions))
	viablePrimary := 0
	for _, r := range regions {
		if _, dup := seen[r.Name]; dup {
			return fmt.Errorf("region name %q 중복", r.Name)
		}
		seen[r.Name] = struct{}{}

		// 빈 priority 는 CRD default 1.0 — 명시값만 엄격 검증.
		if r.Priority != "" {
			v, err := parseStrictPriority(r.Priority)
			if err != nil {
				return fmt.Errorf("region %q priority %q: %w", r.Name, r.Priority, err)
			}
			if v > 0 {
				viablePrimary++
			}
		} else {
			viablePrimary++ // default 1.0 > 0
		}
	}

	if viablePrimary == 0 {
		return fmt.Errorf("primary 가능 region 0개 — 최소 1개 region 의 priority 가 0 보다 커야 함")
	}
	return nil
}

// parseStrictPriority parses a priority string strictly (unlike
// parseRegionPriority which defaults on error) and bounds it to [0, 1000].
// strconv.ParseFloat rejects trailing garbage ("1.5abc"), unlike Sscanf.
func parseStrictPriority(p string) (float64, error) {
	v, err := strconv.ParseFloat(p, 64)
	if err != nil {
		return 0, fmt.Errorf("숫자 형식 아님")
	}
	if v < 0 || v > 1000 {
		return 0, fmt.Errorf("범위 밖 [0~1000] (값 %.1f)", v)
	}
	return v, nil
}
