/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// oplog_uploader_sidecar_test.go — F03 PITR oplog S3 uploader sidecar 회귀 가드.
// BuildOplogUploaderSidecar 가 staging EmptyDir → S3 업로드 loop 컨테이너를
// 정합 생성하는지 검증 (image / S3 env / credential / script fragment).

package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestIsOplogUploaderEnabled(t *testing.T) {
	t.Parallel()
	s3Storage := mongodbv1alpha1.BackupStorageSpec{
		Type: "s3",
		S3:   &mongodbv1alpha1.S3StorageSpec{Bucket: "b", CredentialsRef: corev1.LocalObjectReference{Name: "creds"}},
	}
	pvcStorage := mongodbv1alpha1.BackupStorageSpec{Type: "pvc"}
	cases := []struct {
		name string
		spec *mongodbv1alpha1.BackupSpec
		want bool
	}{
		{"nil spec", nil, false},
		{"PITR disabled", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: false, OplogRetentionHours: 24, Storage: s3Storage}, false},
		{"s3 + pitr → uploader 필요", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: true, OplogRetentionHours: 24, Storage: s3Storage}, true},
		{"pvc + pitr → uploader 불필요", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: true, OplogRetentionHours: 24, Storage: pvcStorage}, false},
		{"s3 type 이나 S3 nil", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: true, OplogRetentionHours: 24, Storage: mongodbv1alpha1.BackupStorageSpec{Type: "s3"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsOplogUploaderEnabled(tc.spec); got != tc.want {
				t.Errorf("IsOplogUploaderEnabled(%v) = %v, want %v", tc.spec, got, tc.want)
			}
		})
	}
}

func TestBuildOplogUploaderSidecar_S3UploadLoop(t *testing.T) {
	t.Parallel()
	spec := &mongodbv1alpha1.BackupSpec{
		Enabled:             true,
		PITREnabled:         true,
		OplogRetentionHours: 24,
		Storage: mongodbv1alpha1.BackupStorageSpec{
			Type: "s3",
			S3: &mongodbv1alpha1.S3StorageSpec{
				Bucket:         "mybucket",
				Endpoint:       "https://rgw.keiailab.local",
				Region:         "us-east-1",
				Prefix:         "mongo/",
				CredentialsRef: corev1.LocalObjectReference{Name: "s3-creds"},
			},
		},
	}
	c := BuildOplogUploaderSidecar(spec)

	assert.Equal(t, "oplog-uploader", c.Name)
	assert.Equal(t, oplogUploaderImage, c.Image)
	require.Len(t, c.Command, 3, "sh -c <script> 3-element command")
	assert.Equal(t, "/bin/sh", c.Command[0])
	assert.Equal(t, "-c", c.Command[1])

	// Script 핵심 명령 (snapshot-lite) — aws s3 cp + staging glob + 성공 시 rm.
	script := c.Command[2]
	for _, fragment := range []string{
		"aws s3 cp",
		oplogStagingMount + "/oplog-*.bson",
		"s3://${S3_BUCKET}/${S3_PREFIX}oplog/",
		"--endpoint-url=",
		"rm -f",
		"sleep 30", // oplogUploadIntervalSeconds 기본
	} {
		assert.Contains(t, script, fragment, "uploader script must contain %q", fragment)
	}

	// S3 env 정합 (BuildBackupJob 패턴 동일)
	envMap := map[string]string{}
	envSecretKey := map[string]string{}
	for _, e := range c.Env {
		envMap[e.Name] = e.Value
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			envSecretKey[e.Name] = e.ValueFrom.SecretKeyRef.Key
		}
	}
	assert.Equal(t, "mybucket", envMap["S3_BUCKET"])
	assert.Equal(t, "https://rgw.keiailab.local", envMap["S3_ENDPOINT"])
	assert.Equal(t, "us-east-1", envMap["S3_REGION"])
	assert.Equal(t, "us-east-1", envMap["AWS_DEFAULT_REGION"], "aws cli region (S3_REGION 커스텀 env 미인식 보완)")
	assert.Equal(t, "mongo/", envMap["S3_PREFIX"])
	assert.Equal(t, "/tmp", envMap["HOME"], "aws cli temp HOME")
	assert.Equal(t, "access-key", envSecretKey["AWS_ACCESS_KEY_ID"])
	assert.Equal(t, "secret-key", envSecretKey["AWS_SECRET_ACCESS_KEY"])

	// Staging volume mount (tailer 와 동일 EmptyDir 공유)
	require.Len(t, c.VolumeMounts, 1)
	assert.Equal(t, oplogStagingVolume, c.VolumeMounts[0].Name)
	assert.Equal(t, oplogStagingMount, c.VolumeMounts[0].MountPath)

	// SecurityContext non-root (tailer 와 동일)
	require.NotNil(t, c.SecurityContext)
	assert.False(t, *c.SecurityContext.AllowPrivilegeEscalation)
}

func TestBuildOplogUploaderSidecar_InsecureSkipTLS(t *testing.T) {
	t.Parallel()
	spec := &mongodbv1alpha1.BackupSpec{
		Enabled: true, PITREnabled: true, OplogRetentionHours: 24,
		Storage: mongodbv1alpha1.BackupStorageSpec{
			Type: "s3",
			S3: &mongodbv1alpha1.S3StorageSpec{
				Bucket:          "b",
				CredentialsRef:  corev1.LocalObjectReference{Name: "creds"},
				InsecureSkipTLS: true,
			},
		},
	}
	c := BuildOplogUploaderSidecar(spec)
	assert.Contains(t, c.Command[2], "--no-verify-ssl", "InsecureSkipTLS=true 시 aws s3 cp 에 --no-verify-ssl")
}

func TestBuildOplogUploaderSidecar_TLSVerifyByDefault(t *testing.T) {
	t.Parallel()
	spec := &mongodbv1alpha1.BackupSpec{
		Enabled: true, PITREnabled: true, OplogRetentionHours: 24,
		Storage: mongodbv1alpha1.BackupStorageSpec{
			Type: "s3",
			S3: &mongodbv1alpha1.S3StorageSpec{
				Bucket:         "b",
				CredentialsRef: corev1.LocalObjectReference{Name: "creds"},
			},
		},
	}
	c := BuildOplogUploaderSidecar(spec)
	assert.NotContains(t, c.Command[2], "--no-verify-ssl", "기본 InsecureSkipTLS=false 시 TLS 검증 유지")
}

// Region 미설정 시 dummy us-east-1 fallback — 라이브 RGW e2e 에서 발견한
// aws cli InvalidLocationConstraint 회귀 가드 (2026-06-04).
func TestBuildOplogUploaderSidecar_RegionDefaultsToUsEast1(t *testing.T) {
	t.Parallel()
	spec := &mongodbv1alpha1.BackupSpec{
		Enabled: true, PITREnabled: true, OplogRetentionHours: 24,
		Storage: mongodbv1alpha1.BackupStorageSpec{
			Type: "s3",
			S3: &mongodbv1alpha1.S3StorageSpec{
				Bucket:         "b",
				CredentialsRef: corev1.LocalObjectReference{Name: "creds"},
				// Region 미설정 (빈 문자열)
			},
		},
	}
	c := BuildOplogUploaderSidecar(spec)
	found := false
	for _, e := range c.Env {
		if e.Name == "AWS_DEFAULT_REGION" {
			assert.Equal(t, "us-east-1", e.Value, "빈 Region 시 dummy us-east-1")
			found = true
		}
	}
	require.True(t, found, "AWS_DEFAULT_REGION env 가 존재해야 한다")
}
