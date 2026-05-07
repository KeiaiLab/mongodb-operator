// validateMongoDBSpec / validateMongoDBShardedSpec 회귀 가드 (iteration 45 신규
// webhook server 부트스트랩 step 1).
//
// CustomValidator 의 admission entry point (ValidateCreate/Update/Delete) 도
// type-assertion path 포함하여 unit-level 검증 (it46 evidence).

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

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

func TestValidateMongoDBSpec_StorageSize_LowerBound(t *testing.T) {
	t.Parallel()
	cases := []struct {
		size    string
		wantErr bool
		desc    string
	}{
		{"512Mi", true, "below 1Gi — reject"},
		{"1Gi", false, "exactly 1Gi — boundary OK"},
		{"10Gi", false, "10Gi default — OK"},
		{"100Gi", false, "100Gi production — OK"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			m := &mongodbv1alpha1.MongoDB{}
			m.Spec.Members = 3
			m.Spec.Version.Version = "8.3"
			m.Spec.Storage.Size = resource.MustParse(tc.size)
			errs := validateMongoDBSpec(m)
			var hasErr bool
			for _, e := range errs {
				if strings.Contains(e.Error(), "storage.size") {
					hasErr = true
				}
			}
			if tc.wantErr && !hasErr {
				t.Errorf("size=%s should be rejected", tc.size)
			}
			if !tc.wantErr && hasErr {
				t.Errorf("size=%s should be accepted", tc.size)
			}
		})
	}
}

func TestValidateMongoDBShardedSpec_StorageSize_LowerBound(t *testing.T) {
	t.Parallel()
	m := &mongodbv1alpha1.MongoDBSharded{}
	m.Spec.Version.Version = "8.3"
	m.Spec.Shards.Count = 3
	m.Spec.Shards.MembersPerShard = 3
	m.Spec.ConfigServer.Storage.Size = resource.MustParse("256Mi")
	m.Spec.Shards.Storage.Size = resource.MustParse("512Mi")
	errs := validateMongoDBShardedSpec(m)
	var cfgErr, shErr bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "configServer.storage.size") {
			cfgErr = true
		}
		if strings.Contains(e.Error(), "shards.storage.size") {
			shErr = true
		}
	}
	if !cfgErr {
		t.Error("configServer.storage.size=256Mi should be rejected")
	}
	if !shErr {
		t.Error("shards.storage.size=512Mi should be rejected")
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

// CustomValidator 진입점은 admission framework 가 호출. 정상 path / 잘못된 GVK /
// nil obj 의 3 시나리오 가드 — panic 방지 + 안전 reject.

func TestMongoDBCustomValidator_ValidateCreate_HappyPath(t *testing.T) {
	t.Parallel()
	v := &MongoDBCustomValidator{}
	m := &mongodbv1alpha1.MongoDB{}
	m.Spec.Members = 3
	m.Spec.Version.Version = "8.3"
	warns, err := v.ValidateCreate(context.Background(), m)
	if err != nil {
		t.Errorf("valid CR should pass, got %v", err)
	}
	if warns != nil {
		t.Errorf("no warnings expected, got %v", warns)
	}
}

func TestMongoDBCustomValidator_ValidateCreate_RejectInvalid(t *testing.T) {
	t.Parallel()
	v := &MongoDBCustomValidator{}
	m := &mongodbv1alpha1.MongoDB{}
	m.Spec.Members = 4 // even — split-brain
	m.Spec.Version.Version = "8.3"
	_, err := v.ValidateCreate(context.Background(), m)
	if err == nil {
		t.Error("members=4 should be rejected by ValidateCreate")
	}
}

func TestMongoDBCustomValidator_TypeAssertionFailure(t *testing.T) {
	t.Parallel()
	// 잘못된 GVK (corev1.Pod) 가 admission framework bug 로 들어오면 panic 대신
	// apiError 반환. validation 우회 차단.
	v := &MongoDBCustomValidator{}
	pod := &corev1.Pod{}
	_, err := v.ValidateCreate(context.Background(), pod)
	if err == nil {
		t.Error("non-MongoDB obj should be rejected (no panic, safe reject)")
	}
}

func TestMongoDBShardedCustomValidator_TypeAssertionFailure(t *testing.T) {
	t.Parallel()
	v := &MongoDBShardedCustomValidator{}
	pod := &corev1.Pod{}
	_, err := v.ValidateCreate(context.Background(), pod)
	if err == nil {
		t.Error("non-MongoDBSharded obj should be rejected (no panic, safe reject)")
	}
}

func TestMongoDBCustomValidator_ValidateUpdate_HappyPath(t *testing.T) {
	t.Parallel()
	v := &MongoDBCustomValidator{}
	old := &mongodbv1alpha1.MongoDB{}
	old.Spec.Members = 3
	old.Spec.Version.Version = "8.2"
	new := &mongodbv1alpha1.MongoDB{}
	new.Spec.Members = 3
	new.Spec.Version.Version = "8.3"
	_, err := v.ValidateUpdate(context.Background(), old, new)
	if err != nil {
		t.Errorf("valid update should pass, got %v", err)
	}
}

func TestMongoDBCustomValidator_ValidateDelete_AlwaysAllow(t *testing.T) {
	t.Parallel()
	v := &MongoDBCustomValidator{}
	m := &mongodbv1alpha1.MongoDB{}
	m.Spec.Members = 4 // 잘못된 spec 이라도 delete 는 통과해야 (cleanup 가능)
	_, err := v.ValidateDelete(context.Background(), m)
	if err != nil {
		t.Errorf("delete should always pass, got %v", err)
	}
}

func TestMongoDBShardedCustomValidator_ValidateCreate_HappyPath(t *testing.T) {
	t.Parallel()
	v := &MongoDBShardedCustomValidator{}
	m := &mongodbv1alpha1.MongoDBSharded{}
	m.Spec.Version.Version = "8.3"
	m.Spec.Shards.Count = 3
	m.Spec.Shards.MembersPerShard = 3
	_, err := v.ValidateCreate(context.Background(), m)
	if err != nil {
		t.Errorf("valid sharded CR should pass, got %v", err)
	}
}

func TestMongoDBShardedCustomValidator_ValidateUpdate_HappyPath(t *testing.T) {
	t.Parallel()
	v := &MongoDBShardedCustomValidator{}
	old := &mongodbv1alpha1.MongoDBSharded{}
	old.Spec.Version.Version = "8.2"
	old.Spec.Shards.Count = 3
	old.Spec.Shards.MembersPerShard = 3
	new := &mongodbv1alpha1.MongoDBSharded{}
	new.Spec.Version.Version = "8.3"
	new.Spec.Shards.Count = 3
	new.Spec.Shards.MembersPerShard = 3
	_, err := v.ValidateUpdate(context.Background(), old, new)
	if err != nil {
		t.Errorf("valid sharded update should pass, got %v", err)
	}
}

func TestMongoDBShardedCustomValidator_ValidateDelete_AlwaysAllow(t *testing.T) {
	t.Parallel()
	v := &MongoDBShardedCustomValidator{}
	m := &mongodbv1alpha1.MongoDBSharded{}
	_, err := v.ValidateDelete(context.Background(), m)
	if err != nil {
		t.Errorf("sharded delete should always pass, got %v", err)
	}
}

func TestMongoDBShardedCustomValidator_ValidateUpdate_TypeAssertionFailure(t *testing.T) {
	t.Parallel()
	v := &MongoDBShardedCustomValidator{}
	pod := &corev1.Pod{}
	old := &mongodbv1alpha1.MongoDBSharded{}
	_, err := v.ValidateUpdate(context.Background(), old, pod)
	if err == nil {
		t.Error("non-MongoDBSharded newObj should be rejected (no panic)")
	}
}

func TestMongoDBCustomValidator_ApplyMessage_Reject(t *testing.T) {
	t.Parallel()
	// admission denial message 가 운영자에게 *원인 식별 가능* 한 keyword 포함
	// 검증 — UX 회귀 가드.
	cases := []struct {
		desc     string
		spec     func(*mongodbv1alpha1.MongoDB)
		mustHave string
	}{
		{"version reject contains version keyword",
			func(m *mongodbv1alpha1.MongoDB) {
				m.Spec.Version.Version = "7.0.5"
				m.Spec.Members = 3
			}, "version"},
		{"members reject contains members keyword",
			func(m *mongodbv1alpha1.MongoDB) {
				m.Spec.Version.Version = "8.3"
				m.Spec.Members = 4
			}, "members"},
	}
	v := &MongoDBCustomValidator{}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			m := &mongodbv1alpha1.MongoDB{}
			tc.spec(m)
			_, err := v.ValidateCreate(context.Background(), m)
			if err == nil {
				t.Fatalf("should reject")
			}
			if !strings.Contains(err.Error(), tc.mustHave) {
				t.Errorf("denial message should contain %q for UX, got %v", tc.mustHave, err)
			}
		})
	}
}
