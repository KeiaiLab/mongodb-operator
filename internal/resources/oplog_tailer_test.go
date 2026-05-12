/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// oplog_tailer_test.go — F02 (cycle 1) PITR oplog tailer sidecar 회귀 가드.

package resources

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestIsOplogTailerEnabled(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		spec *mongodbv1alpha1.BackupSpec
		want bool
	}{
		{"nil spec", nil, false},
		{"backup disabled", &mongodbv1alpha1.BackupSpec{Enabled: false, PITREnabled: true, OplogRetentionHours: 24}, false},
		{"PITR disabled", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: false, OplogRetentionHours: 24}, false},
		{"retention 0", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: true, OplogRetentionHours: 0}, false},
		{"retention negative", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: true, OplogRetentionHours: -1}, false},
		{"all enabled", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: true, OplogRetentionHours: 24}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsOplogTailerEnabled(tc.spec); got != tc.want {
				t.Errorf("IsOplogTailerEnabled(%v) = %v, want %v", tc.spec, got, tc.want)
			}
		})
	}
}

func TestBuildOplogTailerSidecar_BaseSpec(t *testing.T) {
	t.Parallel()
	version := mongodbv1alpha1.MongoDBVersion{Version: "8.2"}
	c := BuildOplogTailerSidecar(version, 27017, true)

	assert.Equal(t, "oplog-tailer", c.Name)
	require.NotEmpty(t, c.Command, "command must be set (sh -c script)")
	assert.Equal(t, "sh", c.Command[0])
	assert.Equal(t, "-c", c.Command[1])

	// Script 가 의도된 핵심 명령을 모두 포함하는지 검증 (snapshot-lite).
	script := strings.Join(c.Command, " ")
	for _, fragment := range []string{
		"mongodump",
		"--db=local",
		"--collection=oplog.rs",
		"--archive=",
		oplogStagingMount,
		"sleep 30",     // batch seconds 30 기본
		"--port 27017", // 인자로 받은 port
	} {
		assert.Contains(t, script, fragment, "oplog tailer script must contain %q", fragment)
	}

	// Volume mount 정합 — staging + admin-credentials (when requested)
	mountNames := map[string]string{}
	for _, m := range c.VolumeMounts {
		mountNames[m.Name] = m.MountPath
	}
	assert.Equal(t, oplogStagingMount, mountNames[oplogStagingVolume])
	assert.Equal(t, "/etc/mongodb-admin", mountNames["admin-credentials"])

	// SecurityContext 가 default (non-root) 와 일치
	require.NotNil(t, c.SecurityContext)
	assert.False(t, *c.SecurityContext.AllowPrivilegeEscalation)
}

func TestBuildOplogTailerSidecar_NoAdminSecret(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(mongodbv1alpha1.MongoDBVersion{Version: "8.0"}, 27018, false)
	for _, m := range c.VolumeMounts {
		assert.NotEqual(t, "admin-credentials", m.Name, "admin secret mount must be omitted")
	}
}

func TestBuildOplogTailerSidecar_PortOverride(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(mongodbv1alpha1.MongoDBVersion{Version: "8.0"}, 27019, false)
	script := strings.Join(c.Command, " ")
	assert.Contains(t, script, "--port 27019", "config server port must be propagated")
}

func TestBuildOplogTailerSidecar_PortZeroFallback(t *testing.T) {
	t.Parallel()
	// 0 또는 음수 port 가 들어오면 기본값(mongoDBPort=27017) 사용.
	c := BuildOplogTailerSidecar(mongodbv1alpha1.MongoDBVersion{Version: "8.2"}, 0, false)
	script := strings.Join(c.Command, " ")
	assert.Contains(t, script, "--port 27017", "zero port must fall back to mongoDBPort")
}

func TestBuildOplogStagingVolume_EmptyDirWithLimit(t *testing.T) {
	t.Parallel()
	v := BuildOplogStagingVolume()
	assert.Equal(t, oplogStagingVolume, v.Name)
	require.NotNil(t, v.EmptyDir, "must be EmptyDir (not PVC)")
	require.NotNil(t, v.EmptyDir.SizeLimit)
	// 4Gi limit 검증 (oplog batch staging 한도)
	q := v.EmptyDir.SizeLimit
	assert.Equal(t, int64(4*1024*1024*1024), q.Value())
}

func TestIntString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{27017, "27017"},
		{-5, "-5"},
		{1234567, "1234567"},
	}
	for _, tc := range cases {
		got := intString(tc.in)
		if got != tc.want {
			t.Errorf("intString(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
	_ = corev1.Container{} // import 유지
}
