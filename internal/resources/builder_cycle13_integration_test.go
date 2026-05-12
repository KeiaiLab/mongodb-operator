/*
Copyright 2026 Keiailab.
*/

// builder_cycle13_integration_test.go — cycle 13 builder 통합 회귀 가드.
//
// PodSpec extension + auth/encryption/audit args 가 실제 StatefulSet 에
// 반영되는지 검증. 본 test 가 PASS 하면 "API 만 정의" 가 아닌 *실 builder
// 통합* 이 완료된 것.

package resources

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func newTestMongoDB() *mongodbv1alpha1.MongoDB {
	return &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rs", Namespace: "default"},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members: 3,
			Version: mongodbv1alpha1.MongoDBVersion{Version: "8.2"},
			Storage: mongodbv1alpha1.StorageSpec{Size: resource.MustParse("1Gi")},
			Auth: mongodbv1alpha1.AuthSpec{
				Mechanism:                 "SCRAM-SHA-256",
				AdminCredentialsSecretRef: corev1.LocalObjectReference{Name: "admin"},
			},
		},
	}
}

func TestBuilderIntegration_LDAPArgs(t *testing.T) {
	t.Parallel()
	mdb := newTestMongoDB()
	mdb.Spec.Auth.LDAP = &mongodbv1alpha1.LDAPSpec{
		Servers: "ldap.example.com:389", BindMethod: "simple", TLS: true,
		AuthorizationQueryTemplate: "{USER}?memberOf?base",
	}
	sts := BuildReplicaSetStatefulSet(mdb)
	args := strings.Join(sts.Spec.Template.Spec.Containers[0].Args, " ")
	for _, w := range []string{"--ldapServers=ldap.example.com:389", "--ldapBindMethod=simple", "--ldapTransportSecurity=tls"} {
		if !strings.Contains(args, w) {
			t.Errorf("LDAP args missing %q in mongod args: %s", w, args)
		}
	}
}

func TestBuilderIntegration_OIDCSetParameter(t *testing.T) {
	t.Parallel()
	mdb := newTestMongoDB()
	mdb.Spec.Auth.OIDC = &mongodbv1alpha1.OIDCSpec{
		IssuerURL: "https://keycloak.example.com/realms/mongodb",
		ClientID:  "mongodb-prod",
	}
	sts := BuildReplicaSetStatefulSet(mdb)
	args := strings.Join(sts.Spec.Template.Spec.Containers[0].Args, " ")
	if !strings.Contains(args, "oidcIdentityProviders=") {
		t.Errorf("OIDC setParameter must be in args: %s", args)
	}
	if !strings.Contains(args, "keycloak.example.com") {
		t.Errorf("OIDC issuer must propagate: %s", args)
	}
}

func TestBuilderIntegration_EncryptionArgs(t *testing.T) {
	t.Parallel()
	mdb := newTestMongoDB()
	mdb.Spec.Storage.Encryption = &mongodbv1alpha1.EncryptionSpec{
		Enabled: true, KeyProvider: "secret", CipherMode: "AES256-GCM",
	}
	sts := BuildReplicaSetStatefulSet(mdb)
	args := strings.Join(sts.Spec.Template.Spec.Containers[0].Args, " ")
	for _, w := range []string{"--enableEncryption", "--encryptionCipherMode=AES256-GCM", "/etc/mongodb-encryption/keyfile"} {
		if !strings.Contains(args, w) {
			t.Errorf("encryption args missing %q: %s", w, args)
		}
	}
}

func TestBuilderIntegration_AuditArgs(t *testing.T) {
	t.Parallel()
	mdb := newTestMongoDB()
	mdb.Spec.AuditLog = &mongodbv1alpha1.AuditLogSpec{Enabled: true, Destination: "file", Format: "JSON"}
	sts := BuildReplicaSetStatefulSet(mdb)
	args := strings.Join(sts.Spec.Template.Spec.Containers[0].Args, " ")
	for _, w := range []string{"--auditDestination=file", "--auditFormat=JSON", "/var/log/mongodb-audit/audit.json"} {
		if !strings.Contains(args, w) {
			t.Errorf("audit args missing %q: %s", w, args)
		}
	}
}

func TestBuilderIntegration_Sidecars(t *testing.T) {
	t.Parallel()
	mdb := newTestMongoDB()
	mdb.Spec.Pod = &mongodbv1alpha1.PodSpec{
		Sidecars: []corev1.Container{
			{Name: "fluent-bit", Image: "fluent/fluent-bit:3.0", Command: []string{"/fluent-bit/bin/fluent-bit"}},
		},
	}
	sts := BuildReplicaSetStatefulSet(mdb)
	found := false
	for _, c := range sts.Spec.Template.Spec.Containers {
		if c.Name == "fluent-bit" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("sidecar 'fluent-bit' must appear in container list")
	}
}

func TestBuilderIntegration_ExtraVolumes_ExtraEnv(t *testing.T) {
	t.Parallel()
	mdb := newTestMongoDB()
	mdb.Spec.Pod = &mongodbv1alpha1.PodSpec{
		ExtraVolumes: []corev1.Volume{
			{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		},
		ExtraVolumeMounts: []corev1.VolumeMount{
			{Name: "tmp", MountPath: "/tmp/app"},
		},
		ExtraEnvVars: []corev1.EnvVar{
			{Name: "MY_CUSTOM_ENV", Value: "hello"},
		},
	}
	sts := BuildReplicaSetStatefulSet(mdb)
	// extra volume present
	volFound := false
	for _, v := range sts.Spec.Template.Spec.Volumes {
		if v.Name == "tmp" {
			volFound = true
			break
		}
	}
	if !volFound {
		t.Errorf("ExtraVolumes 'tmp' must appear in pod volumes")
	}
	// extra mount on mongod
	mountFound := false
	for _, m := range sts.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == "tmp" && m.MountPath == "/tmp/app" {
			mountFound = true
			break
		}
	}
	if !mountFound {
		t.Errorf("ExtraVolumeMounts 'tmp' must appear on mongod container")
	}
	// extra env on mongod
	envFound := false
	for _, e := range sts.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "MY_CUSTOM_ENV" && e.Value == "hello" {
			envFound = true
			break
		}
	}
	if !envFound {
		t.Errorf("ExtraEnvVars 'MY_CUSTOM_ENV' must appear on mongod container env")
	}
}

func TestBuilderIntegration_VolumePermissionsInit(t *testing.T) {
	t.Parallel()
	mdb := newTestMongoDB()
	mdb.Spec.Pod = &mongodbv1alpha1.PodSpec{
		VolumePermissions: &mongodbv1alpha1.VolumePermissionsSpec{Enabled: true},
	}
	sts := BuildReplicaSetStatefulSet(mdb)
	found := false
	for _, c := range sts.Spec.Template.Spec.InitContainers {
		if c.Name == "volume-permissions" {
			found = true
			if !strings.Contains(strings.Join(c.Command, " "), "chown") {
				t.Errorf("volume-permissions init must run chown")
			}
			break
		}
	}
	if !found {
		t.Errorf("volume-permissions init container must appear when enabled")
	}
}

func TestBuilderIntegration_InitScriptsVolume(t *testing.T) {
	t.Parallel()
	mdb := newTestMongoDB()
	mdb.Spec.Pod = &mongodbv1alpha1.PodSpec{
		InitScripts: &mongodbv1alpha1.InitScriptsSpec{
			ConfigMapRef: &corev1.LocalObjectReference{Name: "my-init-cm"},
		},
	}
	sts := BuildReplicaSetStatefulSet(mdb)
	volFound := false
	for _, v := range sts.Spec.Template.Spec.Volumes {
		if v.Name == "init-scripts" && v.ConfigMap != nil && v.ConfigMap.Name == "my-init-cm" {
			volFound = true
			break
		}
	}
	if !volFound {
		t.Errorf("init-scripts volume from ConfigMap must appear")
	}
	mountFound := false
	for _, m := range sts.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == "init-scripts" && m.MountPath == "/docker-entrypoint-initdb.d" {
			mountFound = true
			break
		}
	}
	if !mountFound {
		t.Errorf("init-scripts must be mounted at /docker-entrypoint-initdb.d")
	}
}

func TestBuilderIntegration_ResourcesPreset_AppliedWhenEmpty(t *testing.T) {
	t.Parallel()
	mdb := newTestMongoDB()
	mdb.Spec.Pod = &mongodbv1alpha1.PodSpec{ResourcesPreset: "medium"}
	// Spec.Resources 비어있어야 preset 적용
	sts := BuildReplicaSetStatefulSet(mdb)
	c := sts.Spec.Template.Spec.Containers[0]
	cpu := c.Resources.Requests[corev1.ResourceCPU]
	mem := c.Resources.Requests[corev1.ResourceMemory]
	wantCPU := resource.MustParse("1")
	wantMem := resource.MustParse("1Gi")
	if cpu.Cmp(wantCPU) != 0 || mem.Cmp(wantMem) != 0 {
		t.Errorf("preset 'medium' not applied: cpu=%s mem=%s", cpu.String(), mem.String())
	}
}

func TestBuilderIntegration_ResourcesPreset_IgnoredWhenResourcesSet(t *testing.T) {
	t.Parallel()
	mdb := newTestMongoDB()
	mdb.Spec.Resources = mongodbv1alpha1.ResourcesSpec{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("128Mi")},
	}
	mdb.Spec.Pod = &mongodbv1alpha1.PodSpec{ResourcesPreset: "large"}
	sts := BuildReplicaSetStatefulSet(mdb)
	cpu := sts.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
	want := resource.MustParse("100m")
	if cpu.Cmp(want) != 0 {
		t.Errorf("user Resources must override preset: got %s want %s", cpu.String(), want.String())
	}
}

func TestBuilderIntegration_LifecycleHooks_PreStopOnly(t *testing.T) {
	t.Parallel()
	mdb := newTestMongoDB()
	mdb.Spec.Pod = &mongodbv1alpha1.PodSpec{
		LifecycleHooks: &corev1.Lifecycle{
			PreStop: &corev1.LifecycleHandler{
				Exec: &corev1.ExecAction{Command: []string{"/bin/sh", "-c", "echo shutting-down"}},
			},
		},
	}
	sts := BuildReplicaSetStatefulSet(mdb)
	c := sts.Spec.Template.Spec.Containers[0]
	if c.Lifecycle == nil || c.Lifecycle.PreStop == nil {
		t.Fatalf("preStop must be set")
	}
	// operator postStart 가 user postStart 보다 우선 — 사용자 postStart 무시.
	if c.Lifecycle.PostStart == nil {
		t.Errorf("operator admin bootstrap postStart must remain (not overwritten)")
	}
}
