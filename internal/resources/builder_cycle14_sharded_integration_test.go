/*
Copyright 2026 Keiailab.
*/

// builder_cycle14_sharded_integration_test.go — cycle 14 sharded builder 통합 회귀 가드.

package resources

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func newTestSharded() *mongodbv1alpha1.MongoDBSharded {
	return &mongodbv1alpha1.MongoDBSharded{
		ObjectMeta: metav1.ObjectMeta{Name: "test-sh", Namespace: "default"},
		Spec: mongodbv1alpha1.MongoDBShardedSpec{
			Version: mongodbv1alpha1.MongoDBVersion{Version: "8.2"},
			ConfigServer: mongodbv1alpha1.ConfigServerSpec{
				Members: 3,
				Storage: mongodbv1alpha1.StorageSpec{Size: resource.MustParse("1Gi")},
			},
			Shards: mongodbv1alpha1.ShardSpec{
				Count: 2, MembersPerShard: 3,
				Storage: mongodbv1alpha1.StorageSpec{Size: resource.MustParse("1Gi")},
			},
			Mongos: mongodbv1alpha1.MongosSpec{Replicas: 2},
			Auth: mongodbv1alpha1.AuthSpec{
				Mechanism:                 "SCRAM-SHA-256",
				AdminCredentialsSecretRef: corev1.LocalObjectReference{Name: "admin"},
			},
		},
	}
}

func TestShardedIntegration_ConfigServer_LDAPArgs(t *testing.T) {
	t.Parallel()
	sh := newTestSharded()
	sh.Spec.Auth.LDAP = &mongodbv1alpha1.LDAPSpec{Servers: "ldap.example.com:389", TLS: true}
	sts := BuildConfigServerStatefulSet(sh)
	args := strings.Join(sts.Spec.Template.Spec.Containers[0].Args, " ")
	if !strings.Contains(args, "--ldapServers=ldap.example.com:389") {
		t.Errorf("ConfigServer must include LDAP args: %s", args)
	}
}

func TestShardedIntegration_Shard_EncryptionArgs(t *testing.T) {
	t.Parallel()
	sh := newTestSharded()
	sh.Spec.Shards.Storage.Encryption = &mongodbv1alpha1.EncryptionSpec{
		Enabled: true, KeyProvider: "secret",
	}
	sts := BuildShardStatefulSet(sh, 0)
	args := strings.Join(sts.Spec.Template.Spec.Containers[0].Args, " ")
	if !strings.Contains(args, "--enableEncryption") {
		t.Errorf("Shard must include encryption args: %s", args)
	}
}

func TestShardedIntegration_Mongos_AuditArgs(t *testing.T) {
	t.Parallel()
	sh := newTestSharded()
	sh.Spec.AuditLog = &mongodbv1alpha1.AuditLogSpec{Enabled: true, Destination: "file"}
	dep := BuildMongosDeployment(sh)
	args := strings.Join(dep.Spec.Template.Spec.Containers[0].Args, " ")
	if !strings.Contains(args, "--auditDestination=file") {
		t.Errorf("Mongos must include audit args: %s", args)
	}
}

func TestShardedIntegration_ConfigServer_Sidecars(t *testing.T) {
	t.Parallel()
	sh := newTestSharded()
	sh.Spec.ConfigServer.Pod = &mongodbv1alpha1.PodSpec{
		Sidecars: []corev1.Container{{Name: "fluent-bit", Image: "fluent/fluent-bit:3.0"}},
	}
	sts := BuildConfigServerStatefulSet(sh)
	found := false
	for _, c := range sts.Spec.Template.Spec.Containers {
		if c.Name == "fluent-bit" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ConfigServer sidecar must appear")
	}
}

func TestShardedIntegration_Shard_VolumePermissions(t *testing.T) {
	t.Parallel()
	sh := newTestSharded()
	sh.Spec.Shards.Pod = &mongodbv1alpha1.PodSpec{
		VolumePermissions: &mongodbv1alpha1.VolumePermissionsSpec{Enabled: true},
	}
	sts := BuildShardStatefulSet(sh, 0)
	found := false
	for _, c := range sts.Spec.Template.Spec.InitContainers {
		if c.Name == "volume-permissions" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Shard must include volume-permissions init container")
	}
}

func TestShardedIntegration_Mongos_ExtraEnv(t *testing.T) {
	t.Parallel()
	sh := newTestSharded()
	sh.Spec.Mongos.Pod = &mongodbv1alpha1.PodSpec{
		ExtraEnvVars: []corev1.EnvVar{{Name: "MY_MONGOS_ENV", Value: "v"}},
	}
	dep := BuildMongosDeployment(sh)
	found := false
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "MY_MONGOS_ENV" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Mongos ExtraEnvVars must propagate")
	}
}

func TestShardedIntegration_ConfigServer_ResourcesPreset(t *testing.T) {
	t.Parallel()
	sh := newTestSharded()
	sh.Spec.ConfigServer.Pod = &mongodbv1alpha1.PodSpec{ResourcesPreset: "small"}
	sts := BuildConfigServerStatefulSet(sh)
	cpu := sts.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
	want := resource.MustParse("500m")
	if cpu.Cmp(want) != 0 {
		t.Errorf("ConfigServer preset 'small' cpu: got %s want %s", cpu.String(), want.String())
	}
}

func TestShardedIntegration_Shard_OIDCSetParameter(t *testing.T) {
	t.Parallel()
	sh := newTestSharded()
	sh.Spec.Auth.OIDC = &mongodbv1alpha1.OIDCSpec{
		IssuerURL: "https://idp.example.com", ClientID: "mdb",
	}
	sts := BuildShardStatefulSet(sh, 0)
	args := strings.Join(sts.Spec.Template.Spec.Containers[0].Args, " ")
	if !strings.Contains(args, "oidcIdentityProviders=") {
		t.Errorf("Shard OIDC setParameter missing: %s", args)
	}
}
