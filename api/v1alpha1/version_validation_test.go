/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// version_validation_test.go — IsSupportedMongoDBVersion 회귀 가드.
// iteration 9 (Phase 1 M1) 의 1차 산출물. webhook server 는 iteration 10 (M2)
// 부트스트랩 후 본 헬퍼를 admission 단계에서 호출.

package v1alpha1

import "testing"

func TestIsSupportedMongoDBVersion_PatchLevelAccepted(t *testing.T) {
	t.Parallel()
	cases := []string{"8.0.0", "8.0.5", "8.2.0", "8.2.10", "8.3.0", "8.3.1"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			if !IsSupportedMongoDBVersion(v) {
				t.Errorf("IsSupportedMongoDBVersion(%q) = false, want true", v)
			}
		})
	}
}

func TestIsSupportedMongoDBVersion_MajorMinorAccepted(t *testing.T) {
	t.Parallel()
	cases := []string{"8.0", "8.2", "8.3"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			if !IsSupportedMongoDBVersion(v) {
				t.Errorf("IsSupportedMongoDBVersion(%q) = false, want true", v)
			}
		})
	}
}

func TestIsSupportedMongoDBVersion_Rejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v      string
		reason string
	}{
		{"7.0.5", "below LTS baseline"},
		{"7.0", "below LTS baseline"},
		{"9.0", "future version not yet validated"},
		{"9.0.0", "future version"},
		{"8.1", "skipped odd-numbered (dev) release"},
		{"8.4", "future patch line not yet released"},
		{"8", "major-only, ambiguous"},
		{"", "empty"},
		{".5", "leading dot"},
		{"abc", "not semver"},
	}
	for _, tc := range cases {
		t.Run(tc.v, func(t *testing.T) {
			t.Parallel()
			if IsSupportedMongoDBVersion(tc.v) {
				t.Errorf("IsSupportedMongoDBVersion(%q) = true, want false (reason: %s)",
					tc.v, tc.reason)
			}
		})
	}
}

// F11 (cycle 7): IsValidUpgradePath 회귀 가드.
func TestIsValidUpgradePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{"same version", "8.2", "8.2", false},
		{"single minor 8.0 to 8.2", "8.0", "8.2", false},
		{"single minor 8.2 to 8.3", "8.2", "8.3", false},
		{"minor skip 8.0 to 8.3 reject", "8.0", "8.3", true},
		{"downgrade 8.2 to 8.0 reject", "8.2", "8.0", true},
		{"downgrade 8.3 to 8.2 reject", "8.3", "8.2", true},
		{"unsupported from reject", "7.0", "8.0", true},
		{"unsupported to reject", "8.0", "9.0", true},
		{"unparseable", "abc", "8.0", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := IsValidUpgradePath(tc.from, tc.to)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Errorf("IsValidUpgradePath(%q, %q) err=%v, wantErr=%v", tc.from, tc.to, err, tc.wantErr)
			}
		})
	}
}

// Patch upgrade 는 fromMajor=toMajor && fromMinor=toMinor → 차이 0 → allowed.
func TestIsValidUpgradePath_PatchAllowed(t *testing.T) {
	t.Parallel()
	if err := IsValidUpgradePath("8.0.0", "8.0.5"); err != nil {
		t.Errorf("patch upgrade must be allowed: %v", err)
	}
	if err := IsValidUpgradePath("8.2.0", "8.2.10"); err != nil {
		t.Errorf("patch upgrade must be allowed: %v", err)
	}
}

func TestSupportedMongoDBVersions_Snapshot(t *testing.T) {
	t.Parallel()
	want := map[string]bool{"8.0": true, "8.2": true, "8.3": true}
	got := SupportedMongoDBVersions
	if len(got) != len(want) {
		t.Errorf("SupportedMongoDBVersions length = %d, want %d (snapshot drift?)",
			len(got), len(want))
	}
	for _, v := range got {
		if !want[v] {
			t.Errorf("unexpected version in SupportedMongoDBVersions: %q", v)
		}
	}
}
