/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package topology

import "testing"

// TestTopologyPackageCompiles 는 topology 패키지가 test-unit 게이트에 실제로
// 포함되어 컴파일·실행됨을 증명한다 (Makefile test-unit recipe 가 패키지를
// enumerate 하므로 누락 시 영구 미실행되는 함정 방지).
func TestTopologyPackageCompiles(t *testing.T) {
	var plan ZonePlacementPlan
	if plan.DryRun {
		t.Errorf("ZonePlacementPlan zero-value DryRun 은 false 기대, got true")
	}
	var v PreflightVerdict
	if v.Safe {
		t.Errorf("PreflightVerdict zero-value Safe 는 false 기대, got true")
	}
}
