package controller

import (
	"testing"
	"time"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestParseValidationInterval_Default(t *testing.T) {
	d := parseValidationInterval(nil)
	if d != defaultValidationInterval {
		t.Errorf("expected %v, got %v", defaultValidationInterval, d)
	}
}

func TestParseValidationInterval_Empty(t *testing.T) {
	d := parseValidationInterval(&mongodbv1alpha1.UpgradeStrategySpec{})
	if d != defaultValidationInterval {
		t.Errorf("expected %v, got %v", defaultValidationInterval, d)
	}
}

func TestParseValidationInterval_Custom(t *testing.T) {
	d := parseValidationInterval(&mongodbv1alpha1.UpgradeStrategySpec{
		ValidationInterval: "120s",
	})
	if d != 120*time.Second {
		t.Errorf("expected 120s, got %v", d)
	}
}

func TestParseValidationInterval_InvalidFallback(t *testing.T) {
	d := parseValidationInterval(&mongodbv1alpha1.UpgradeStrategySpec{
		ValidationInterval: "invalid",
	})
	if d != defaultValidationInterval {
		t.Errorf("expected default %v on invalid input, got %v", defaultValidationInterval, d)
	}
}

func TestSetUpgradeCondition_New(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{}
	setUpgradeCondition(mdb, "UpgradeComplete", "True", "Upgraded", "done")
	if len(mdb.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(mdb.Status.Conditions))
	}
	if mdb.Status.Conditions[0].Type != "UpgradeComplete" {
		t.Errorf("expected UpgradeComplete, got %s", mdb.Status.Conditions[0].Type)
	}
}

func TestSetUpgradeCondition_Update(t *testing.T) {
	mdb := &mongodbv1alpha1.MongoDB{}
	setUpgradeCondition(mdb, "UpgradeComplete", "True", "Upgraded", "v1")
	setUpgradeCondition(mdb, "UpgradeComplete", "True", "Upgraded", "v2")
	if len(mdb.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition after update, got %d", len(mdb.Status.Conditions))
	}
	if mdb.Status.Conditions[0].Message != "v2" {
		t.Errorf("expected message v2, got %s", mdb.Status.Conditions[0].Message)
	}
}

func TestUpgradePhaseConstants(t *testing.T) {
	phases := []string{UpgradePhaseBackingUp, UpgradePhaseUpgrading, UpgradePhaseValidating, UpgradePhaseRollingBack}
	for _, p := range phases {
		if p == "" {
			t.Error("upgrade phase constant should not be empty")
		}
	}
}

// TestFCVMajorMinor — FCV 자동 commit(갭5)이 버전에서 major.minor만 추출하는지 검증.
func TestFCVMajorMinor(t *testing.T) {
	cases := []struct{ in, want string }{
		{"8.2.1", "8.2"},
		{"8.0", "8.0"},
		{"8.2.13", "8.2"},
		{"8", ""}, // major만 → FCV 도출 불가
		{"", ""},  // 빈 값
	}
	for _, c := range cases {
		if got := fcvMajorMinor(c.in); got != c.want {
			t.Errorf("fcvMajorMinor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
