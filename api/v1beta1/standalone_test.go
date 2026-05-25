/*
Copyright 2026 Keiailab.
*/

// standalone_test.go — G-11 (cycle 19) IsStandaloneMode 회귀 가드.

package v1beta1

import "testing"

func TestIsStandaloneMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		members int32
		want    bool
	}{
		{"single member standalone", 1, true},
		{"3 members rs", 3, false},
		{"5 members rs", 5, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &MongoDBSpec{Members: tc.members}
			if got := s.IsStandaloneMode(); got != tc.want {
				t.Errorf("IsStandaloneMode(members=%d) = %v, want %v", tc.members, got, tc.want)
			}
		})
	}
}
