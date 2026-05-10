/*
Copyright 2026 Keiailab.
*/

package resources

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestDefaultedTopologySpread_user_provided_preserved(t *testing.T) {
	user := []corev1.TopologySpreadConstraint{{TopologyKey: "rack"}}
	got := defaultedTopologySpread(user, 3, map[string]string{"app": "x"})
	if len(got) != 1 || got[0].TopologyKey != "rack" {
		t.Errorf("user TSC overridden: %v", got)
	}
}

func TestDefaultedTopologySpread_members_1_no_inject(t *testing.T) {
	got := defaultedTopologySpread(nil, 1, map[string]string{"app": "x"})
	if got != nil {
		t.Errorf("members=1 → 미주입 expected, got %v", got)
	}
}

func TestDefaultedTopologySpread_members_ge_2_injects_2_axes(t *testing.T) {
	got := defaultedTopologySpread(nil, 3, map[string]string{"app": "x"})
	if len(got) != 2 {
		t.Fatalf("expected 2 default TSCs, got %d", len(got))
	}
	if got[0].TopologyKey != "topology.kubernetes.io/zone" || got[1].TopologyKey != "kubernetes.io/hostname" {
		t.Errorf("TSC keys: %q, %q", got[0].TopologyKey, got[1].TopologyKey)
	}
	for _, c := range got {
		if c.MaxSkew != 1 || c.WhenUnsatisfiable != corev1.ScheduleAnyway {
			t.Errorf("TSC: %v", c)
		}
	}
}
