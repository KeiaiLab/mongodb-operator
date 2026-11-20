/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Member exporter — spec.monitoring.members = Enabled 가 shard · cfg 파드에
// localhost exporter 사이드카를 더하는지, Disabled · 비움은 렌더가 바이트
// 동일한지 잠근다.

package resources

import (
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func containerNamed(sts *appsv1.StatefulSet, name string) *corev1.Container {
	for i := range sts.Spec.Template.Spec.Containers {
		if sts.Spec.Template.Spec.Containers[i].Name == name {
			return &sts.Spec.Template.Spec.Containers[i]
		}
	}
	return nil
}

func withMemberMonitoring(sh *mongodbv1alpha1.MongoDBSharded, mode mongodbv1alpha1.MemberMonitoring) *mongodbv1alpha1.MongoDBSharded {
	sh.Spec.Monitoring = &mongodbv1alpha1.MonitoringSpec{Members: mode}
	return sh
}

func requireMemberExporter(t *testing.T, sts *appsv1.StatefulSet, cr, key string) {
	t.Helper()
	c := containerNamed(sts, "exporter")
	require.NotNil(t, c)
	require.Equal(t, "mongodb", sts.Spec.Template.Spec.Containers[0].Name)
	require.Equal(t, exporterImage, c.Image)
	require.Equal(t, []string{
		"--collector.replicasetstatus",
		"--collector.diagnosticdata",
		"--collector.dbstats",
		"--compatible-mode",
	}, c.Args)
	require.Equal(t, []corev1.ContainerPort{{Name: "metrics", ContainerPort: 9216}}, c.Ports)
	require.Equal(t, buildDefaultContainerSecurityContext(), c.SecurityContext)

	require.Len(t, c.Env, 1)
	ref := c.Env[0].ValueFrom.SecretKeyRef
	require.Equal(t, "MONGODB_URI", c.Env[0].Name)
	require.Equal(t, cr+"-member-exporter-uri", ref.Name)
	require.Equal(t, key, ref.Key)
}

// TestMemberExporter_DisabledKeepsGolden — Disabled · monitoring 만 켬은 바이트 동일.
func TestMemberExporter_DisabledKeepsGolden(t *testing.T) {
	want := string(shardStatefulSetJSON(t, goldenShardSharded()))

	sh := withMemberMonitoring(goldenShardSharded(), mongodbv1alpha1.MemberMonitoringDisabled)
	require.Equal(t, want, string(shardStatefulSetJSON(t, sh)))

	on := goldenShardSharded()
	on.Spec.Monitoring = &mongodbv1alpha1.MonitoringSpec{Enabled: true}
	require.Equal(t, want, string(shardStatefulSetJSON(t, on)))
	require.Equal(t, BuildConfigServerStatefulSet(goldenShardSharded()), BuildConfigServerStatefulSet(on))
}

// TestMemberExporter_Shard — shard 는 키 shard(localhost:27018).
func TestMemberExporter_Shard(t *testing.T) {
	sh := withMemberMonitoring(shardedWithAuth(), mongodbv1alpha1.MemberMonitoringEnabled)

	requireMemberExporter(t, BuildShardStatefulSet(sh, 2), sh.Name, "shard")
}

// TestMemberExporter_ConfigServer — cfg 는 키 configsvr(localhost:27019).
func TestMemberExporter_ConfigServer(t *testing.T) {
	sh := withMemberMonitoring(shardedWithAuth(), mongodbv1alpha1.MemberMonitoringEnabled)

	requireMemberExporter(t, BuildConfigServerStatefulSet(sh), sh.Name, "configsvr")
}

// TestMemberExporter_URISecret — localhost directConnection URI, 비밀번호는 퍼센트 인코딩.
func TestMemberExporter_URISecret(t *testing.T) {
	sh := shardedWithAuth()
	s := BuildMemberExporterSecret(sh, "p@ss:/w")

	require.Equal(t, sh.Name+"-member-exporter-uri", s.Name)
	require.Equal(t, sh.Namespace, s.Namespace)
	require.Equal(t, map[string][]byte{
		"shard":     []byte("mongodb://admin:p%40ss%3A%2Fw@localhost:27018/?authSource=admin&directConnection=true"),
		"configsvr": []byte("mongodb://admin:p%40ss%3A%2Fw@localhost:27019/?authSource=admin&directConnection=true"),
	}, s.Data)
}
