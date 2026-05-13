/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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

func TestValidateTLSSpec_OmitEmptyTrap(t *testing.T) {
	t.Parallel()
	t.Run("nil TLS → ok", func(t *testing.T) {
		t.Parallel()
		errs := validateTLSSpec(nil, nil)
		if len(errs) > 0 {
			t.Errorf("nil TLS should pass, got %v", errs)
		}
	})
	t.Run("certManager 활성 + name 비움 → reject", func(t *testing.T) {
		t.Parallel()
		tls := &mongodbv1alpha1.TLSSpec{
			Enabled: true,
			CertManager: &mongodbv1alpha1.CertManagerSpec{
				IssuerRef: mongodbv1alpha1.CertIssuerRef{Name: ""},
			},
		}
		errs := validateTLSSpec(nil, tls)
		var hasErr bool
		for _, e := range errs {
			if strings.Contains(e.Error(), "issuerRef.name") {
				hasErr = true
			}
		}
		if !hasErr {
			t.Error("empty issuerRef.name with certManager set should reject")
		}
	})
	t.Run("customCert 활성 + secretName 비움 → reject", func(t *testing.T) {
		t.Parallel()
		tls := &mongodbv1alpha1.TLSSpec{
			Enabled:    true,
			CustomCert: &mongodbv1alpha1.CustomCertSpec{SecretName: ""},
		}
		errs := validateTLSSpec(nil, tls)
		var hasErr bool
		for _, e := range errs {
			if strings.Contains(e.Error(), "secretName") {
				hasErr = true
			}
		}
		if !hasErr {
			t.Error("empty customCert.secretName should reject")
		}
	})
	t.Run("TLS Enabled + 둘 다 nil (self-signed) → ok", func(t *testing.T) {
		t.Parallel()
		tls := &mongodbv1alpha1.TLSSpec{Enabled: true}
		errs := validateTLSSpec(nil, tls)
		if len(errs) > 0 {
			t.Errorf("self-signed mode (둘 다 nil) should pass, got %v", errs)
		}
	})
}

func TestValidateBackupSpec_OmitEmptyTrap(t *testing.T) {
	t.Parallel()
	t.Run("Backup nil → ok", func(t *testing.T) {
		t.Parallel()
		errs := validateBackupSpec(nil, nil)
		if len(errs) > 0 {
			t.Errorf("nil Backup should pass, got %v", errs)
		}
	})
	t.Run("Backup enabled + schedule 비움 → reject", func(t *testing.T) {
		t.Parallel()
		b := &mongodbv1alpha1.BackupSpec{
			Enabled:  true,
			Schedule: "", // 비어있음
			Storage: mongodbv1alpha1.BackupStorageSpec{
				Type: "s3",
				S3: &mongodbv1alpha1.S3StorageSpec{
					Bucket:         "my-bucket",
					CredentialsRef: corev1.LocalObjectReference{Name: "creds"},
				},
			},
		}
		errs := validateBackupSpec(nil, b)
		var hasSchedule bool
		for _, e := range errs {
			if strings.Contains(e.Error(), "schedule") {
				hasSchedule = true
			}
		}
		if !hasSchedule {
			t.Error("backup.enabled=true + schedule='' should reject")
		}
	})
	t.Run("Backup 비활성 → ok (검증 skip)", func(t *testing.T) {
		t.Parallel()
		b := &mongodbv1alpha1.BackupSpec{Enabled: false}
		errs := validateBackupSpec(nil, b)
		if len(errs) > 0 {
			t.Errorf("disabled backup should skip, got %v", errs)
		}
	})
	t.Run("s3 활성 + bucket 비움 → reject", func(t *testing.T) {
		t.Parallel()
		b := &mongodbv1alpha1.BackupSpec{
			Enabled: true,
			Storage: mongodbv1alpha1.BackupStorageSpec{
				Type: "s3",
				S3: &mongodbv1alpha1.S3StorageSpec{
					Bucket:         "",
					CredentialsRef: corev1.LocalObjectReference{Name: "creds"},
				},
			},
		}
		errs := validateBackupSpec(nil, b)
		var hasErr bool
		for _, e := range errs {
			if strings.Contains(e.Error(), "bucket") {
				hasErr = true
			}
		}
		if !hasErr {
			t.Error("empty s3.bucket should reject")
		}
	})
	t.Run("s3 활성 + credentialsRef.name 비움 → reject", func(t *testing.T) {
		t.Parallel()
		b := &mongodbv1alpha1.BackupSpec{
			Enabled: true,
			Storage: mongodbv1alpha1.BackupStorageSpec{
				Type: "s3",
				S3: &mongodbv1alpha1.S3StorageSpec{
					Bucket:         "my-bucket",
					CredentialsRef: corev1.LocalObjectReference{Name: ""},
				},
			},
		}
		errs := validateBackupSpec(nil, b)
		var hasErr bool
		for _, e := range errs {
			if strings.Contains(e.Error(), "credentialsRef.name") {
				hasErr = true
			}
		}
		if !hasErr {
			t.Error("empty s3.credentialsRef.name should reject")
		}
	})
}

func TestValidateMongoDBSpec_AuthSecretRef_Required(t *testing.T) {
	t.Parallel()
	t.Run("empty name → reject", func(t *testing.T) {
		t.Parallel()
		m := &mongodbv1alpha1.MongoDB{}
		m.Spec.Members = 3
		m.Spec.Version.Version = "8.3"
		// AdminCredentialsSecretRef.Name 의도적 비움
		errs := validateMongoDBSpec(m)
		var hasErr bool
		for _, e := range errs {
			if strings.Contains(e.Error(), "adminCredentialsSecretRef.name") {
				hasErr = true
			}
		}
		if !hasErr {
			t.Error("empty secret name should be rejected")
		}
	})
	t.Run("non-empty name → ok", func(t *testing.T) {
		t.Parallel()
		m := &mongodbv1alpha1.MongoDB{}
		m.Spec.Members = 3
		m.Spec.Version.Version = "8.3"
		m.Spec.Auth.AdminCredentialsSecretRef.Name = "my-secret"
		errs := validateMongoDBSpec(m)
		for _, e := range errs {
			if strings.Contains(e.Error(), "adminCredentialsSecretRef") {
				t.Errorf("non-empty secret name should pass, got %v", e)
			}
		}
	})
}

func TestValidateMongoDBShardedSpec_AuthSecretRef_Required(t *testing.T) {
	t.Parallel()
	m := &mongodbv1alpha1.MongoDBSharded{}
	m.Spec.Version.Version = "8.3"
	m.Spec.Shards.Count = 3
	m.Spec.Shards.MembersPerShard = 3
	// AdminCredentialsSecretRef.Name 비움
	errs := validateMongoDBShardedSpec(m)
	var hasErr bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "adminCredentialsSecretRef.name") {
			hasErr = true
		}
	}
	if !hasErr {
		t.Error("sharded empty secret name should be rejected")
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

// TestValidateShardArbiter — ROADMAP 4.2-a Sharded Arbiter 필드 검증.
//
// Arbiter helper 단위 + validateMongoDBShardedSpec 통합 2-layer 테스트.
func TestValidateShardArbiter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		arb     *mongodbv1alpha1.ShardArbiterSpec
		members int32
		wantErr bool
		errSub  string
	}{
		{"nil arbiter → ok", nil, 3, false, ""},
		{
			"disabled → ok (replicas ignored)",
			&mongodbv1alpha1.ShardArbiterSpec{Enabled: false, Replicas: 0},
			3, false, "",
		},
		{
			"enabled + replicas=0 → reject (모순)",
			&mongodbv1alpha1.ShardArbiterSpec{Enabled: true, Replicas: 0},
			2, true, "replicas must be >= 1",
		},
		{
			"enabled + replicas=2 → reject (MongoDB max 1)",
			&mongodbv1alpha1.ShardArbiterSpec{Enabled: true, Replicas: 2},
			2, true, "at most 1 arbiter",
		},
		{
			"enabled + replicas=1 + members=2 → ok (PSA, total vote 3)",
			&mongodbv1alpha1.ShardArbiterSpec{Enabled: true, Replicas: 1},
			2, false, "",
		},
		{
			"enabled + replicas=1 + members=3 → reject (total vote 4, 짝수)",
			&mongodbv1alpha1.ShardArbiterSpec{Enabled: true, Replicas: 1},
			3, true, "odd quorum",
		},
		{
			"enabled + replicas=1 + members=4 → ok (total vote 5)",
			&mongodbv1alpha1.ShardArbiterSpec{Enabled: true, Replicas: 1},
			4, false, "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := validateShardArbiter(nil, tc.arb, tc.members)
			if tc.wantErr && len(errs) == 0 {
				t.Fatalf("expected error containing %q, got nil", tc.errSub)
			}
			if !tc.wantErr && len(errs) > 0 {
				t.Fatalf("expected no error, got %v", errs)
			}
			if tc.wantErr {
				var found bool
				for _, e := range errs {
					if strings.Contains(e.Error(), tc.errSub) {
						found = true
					}
				}
				if !found {
					t.Errorf("expected %q in errors, got %v", tc.errSub, errs)
				}
			}
		})
	}
}

// TestValidateMongoDBShardedSpec_ArbiterIntegration — CR 단위 통합 — 모순된
// arbiter 설정이 validateMongoDBShardedSpec 까지 전파되는지 확인.
func TestValidateMongoDBShardedSpec_ArbiterIntegration(t *testing.T) {
	t.Parallel()
	m := &mongodbv1alpha1.MongoDBSharded{}
	m.Spec.Version.Version = "8.3"
	m.Spec.Shards.Count = 3
	m.Spec.Shards.MembersPerShard = 3
	m.Spec.Shards.Arbiter = &mongodbv1alpha1.ShardArbiterSpec{
		Enabled:  true,
		Replicas: 1, // total vote = 4 (짝수) → reject
	}
	errs := validateMongoDBShardedSpec(m)
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "odd quorum") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected odd-quorum error, got %v", errs)
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
	m.Spec.Auth.AdminCredentialsSecretRef.Name = "admin-secret"
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

// TypeAssertionFailure tests 제거 — controller-runtime v0.23+ generic API 가
// ValidateCreate(*MongoDB) signature 강제하여 *컴파일 단계* 에서 잘못된 GVK
// 차단. ADR-0017 Type A (CRD/compile-time guarantee 로 unreachable). 즉
// non-generic API (v0.22.4) 시절의 *runtime panic 가드* 가 *compile-time
// guarantee* 로 격상되어 test 자체 컴파일 불가.

func TestMongoDBCustomValidator_ValidateUpdate_HappyPath(t *testing.T) {
	t.Parallel()
	v := &MongoDBCustomValidator{}
	old := &mongodbv1alpha1.MongoDB{}
	old.Spec.Members = 3
	old.Spec.Version.Version = "8.2"
	old.Spec.Auth.AdminCredentialsSecretRef.Name = "admin-secret"
	new := &mongodbv1alpha1.MongoDB{}
	new.Spec.Members = 3
	new.Spec.Version.Version = "8.3"
	new.Spec.Auth.AdminCredentialsSecretRef.Name = "admin-secret"
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
	m.Spec.Auth.AdminCredentialsSecretRef.Name = "admin-secret"
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
	old.Spec.Auth.AdminCredentialsSecretRef.Name = "admin-secret"
	new := &mongodbv1alpha1.MongoDBSharded{}
	new.Spec.Version.Version = "8.3"
	new.Spec.Shards.Count = 3
	new.Spec.Shards.MembersPerShard = 3
	new.Spec.Auth.AdminCredentialsSecretRef.Name = "admin-secret"
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
