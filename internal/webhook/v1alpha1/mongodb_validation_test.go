// validateMongoDBSpec / validateMongoDBShardedSpec 회귀 가드 (iteration 45 신규
// webhook server 부트스트랩 step 1).

package v1alpha1

import (
	"strings"
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestValidateMongoDBSpec_VersionWhitelist(t *testing.T) {
	t.Parallel()
	t.Run("supported 8.3.1 → ok", func(t *testing.T) {
		t.Parallel()
		m := &mongodbv1alpha1.MongoDB{}
		m.Spec.Members = 3
		m.Spec.Version.Version = "8.3.1"
		errs := validateMongoDBSpec(m)
		for _, e := range errs {
			if strings.Contains(e.Error(), "version") {
				t.Errorf("8.3.1 should pass, got %v", e)
			}
		}
	})
	t.Run("unsupported 7.0.5 → error", func(t *testing.T) {
		t.Parallel()
		m := &mongodbv1alpha1.MongoDB{}
		m.Spec.Members = 3
		m.Spec.Version.Version = "7.0.5"
		errs := validateMongoDBSpec(m)
		var hasVer bool
		for _, e := range errs {
			if strings.Contains(e.Error(), "version") {
				hasVer = true
			}
		}
		if !hasVer {
			t.Error("7.0.5 should be rejected")
		}
	})
}

func TestValidateMongoDBSpec_QuorumMembers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		members int32
		wantErr bool
		desc    string
	}{
		{0, false, "0 (unset, CRD enforces required)"},
		{1, false, "single-instance dev OK"},
		{2, true, "even 2 — split-brain risk"},
		{3, false, "odd 3 — quorum"},
		{4, true, "even 4 — split-brain"},
		{5, false, "odd 5 — quorum"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			m := &mongodbv1alpha1.MongoDB{}
			m.Spec.Members = tc.members
			m.Spec.Version.Version = "8.3"
			errs := validateMongoDBSpec(m)
			var hasMemErr bool
			for _, e := range errs {
				if strings.Contains(e.Error(), "members") {
					hasMemErr = true
				}
			}
			if tc.wantErr && !hasMemErr {
				t.Errorf("members=%d should be rejected", tc.members)
			}
			if !tc.wantErr && hasMemErr {
				t.Errorf("members=%d should be accepted", tc.members)
			}
		})
	}
}

func TestValidateMongoDBShardedSpec_LargeShardCount(t *testing.T) {
	t.Parallel()
	m := &mongodbv1alpha1.MongoDBSharded{}
	m.Spec.Version.Version = "8.3"
	m.Spec.Shards.Count = 100
	m.Spec.Shards.MembersPerShard = 3
	errs := validateMongoDBShardedSpec(m)
	var hasErr bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "shards.count") {
			hasErr = true
		}
	}
	if !hasErr {
		t.Error("shards.count=100 should be rejected (>64)")
	}
}

func TestValidateMongoDBShardedSpec_MembersPerShardEven(t *testing.T) {
	t.Parallel()
	m := &mongodbv1alpha1.MongoDBSharded{}
	m.Spec.Version.Version = "8.3"
	m.Spec.Shards.Count = 3
	m.Spec.Shards.MembersPerShard = 4 // even
	errs := validateMongoDBShardedSpec(m)
	var hasErr bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "membersPerShard") {
			hasErr = true
		}
	}
	if !hasErr {
		t.Error("membersPerShard=4 should be rejected (split-brain)")
	}
}
