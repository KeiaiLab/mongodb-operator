/*
Copyright 2026 Keiailab.
*/

package resources

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestResourcePreset_KnownNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		cpuRequest string
		memRequest string
	}{
		{"nano", "100m", "128Mi"},
		{"micro", "250m", "256Mi"},
		{"small", "500m", "512Mi"},
		{"medium", "1", "1Gi"},
		{"large", "2", "2Gi"},
		{"xlarge", "4", "4Gi"},
		{"2xlarge", "8", "8Gi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ResourcePreset(tc.name)
			cpu := got.Requests[corev1.ResourceCPU]
			mem := got.Requests[corev1.ResourceMemory]
			wantCPU := resource.MustParse(tc.cpuRequest)
			wantMem := resource.MustParse(tc.memRequest)
			if cpu.Cmp(wantCPU) != 0 {
				t.Errorf("%s cpu request: got %s want %s", tc.name, cpu.String(), wantCPU.String())
			}
			if mem.Cmp(wantMem) != 0 {
				t.Errorf("%s mem request: got %s want %s", tc.name, mem.String(), wantMem.String())
			}
			// limits 가 requests 보다 작지 않은지 정합 검증
			cpuLim := got.Limits[corev1.ResourceCPU]
			memLim := got.Limits[corev1.ResourceMemory]
			if cpuLim.Cmp(cpu) < 0 {
				t.Errorf("%s cpu limit < request", tc.name)
			}
			if memLim.Cmp(mem) < 0 {
				t.Errorf("%s mem limit < request", tc.name)
			}
		})
	}
}

func TestResourcePreset_NoneEmpty(t *testing.T) {
	t.Parallel()
	for _, n := range []string{"", "none", "unknown-preset"} {
		got := ResourcePreset(n)
		if len(got.Requests) != 0 || len(got.Limits) != 0 {
			t.Errorf("%s must return empty ResourceRequirements", n)
		}
	}
}

func TestIsValidPreset(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", "none", "nano", "micro", "small", "medium", "large", "xlarge", "2xlarge"} {
		if !IsValidPreset(p) {
			t.Errorf("%q must be valid preset", p)
		}
	}
	for _, p := range []string{"unknown", "huge", "X-Large"} {
		if IsValidPreset(p) {
			t.Errorf("%q must NOT be valid preset", p)
		}
	}
}
