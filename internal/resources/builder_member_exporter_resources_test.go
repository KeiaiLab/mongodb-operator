/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Member exporter resources: spec.monitoring.exporter.resources overrides the
// built-in values one entry at a time. Unset keeps the render byte-identical.

package resources

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func withExporterResources(requests, limits corev1.ResourceList) *mongodbv1alpha1.MongoDBSharded {
	sh := withMemberMonitoring(shardedWithAuth(), mongodbv1alpha1.MemberMonitoringEnabled)
	sh.Spec.Monitoring.Exporter = &mongodbv1alpha1.ExporterSpec{
		Resources: mongodbv1alpha1.ResourcesSpec{Requests: requests, Limits: limits},
	}
	return sh
}

func quantities(list corev1.ResourceList) map[string]string {
	out := map[string]string{}
	for name, q := range list {
		out[string(name)] = q.String()
	}
	return out
}

func requireExporterResources(t *testing.T, sts *appsv1.StatefulSet, requests, limits map[string]string) {
	t.Helper()
	c := containerNamed(sts, "exporter")
	require.NotNil(t, c)
	require.Equal(t, requests, quantities(c.Resources.Requests))
	require.Equal(t, limits, quantities(c.Resources.Limits))
}

func memberSetsJSON(t *testing.T, sh *mongodbv1alpha1.MongoDBSharded) string {
	t.Helper()
	cfg, err := json.Marshal(BuildConfigServerStatefulSet(sh))
	require.NoError(t, err)
	return string(shardStatefulSetJSON(t, sh)) + string(cfg)
}

// Unset, an empty exporter block and the API-defaulted image all render the
// built-in values, byte for byte.
func TestMemberExporterResources_Default(t *testing.T) {
	bare := withMemberMonitoring(shardedWithAuth(), mongodbv1alpha1.MemberMonitoringEnabled)
	want := memberSetsJSON(t, bare)

	requireExporterResources(t, BuildShardStatefulSet(bare, 0),
		map[string]string{"cpu": "50m", "memory": "64Mi"},
		map[string]string{"cpu": "200m", "memory": "256Mi"})

	require.Equal(t, want, memberSetsJSON(t, withExporterResources(nil, nil)))
	require.Equal(t, want, memberSetsJSON(t, withExporterResources(corev1.ResourceList{}, corev1.ResourceList{})))

	defaulted := withExporterResources(nil, nil)
	defaulted.Spec.Monitoring.Exporter.Image = "percona/mongodb_exporter:0.53.0"
	require.Equal(t, want, memberSetsJSON(t, defaulted))
}

// One entry set: the other three keep the built-in values. Shard and config
// server pods follow the same block.
func TestMemberExporterResources_PartialKeepsRest(t *testing.T) {
	sh := withExporterResources(nil, corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")})

	for _, sts := range []*appsv1.StatefulSet{BuildShardStatefulSet(sh, 1), BuildConfigServerStatefulSet(sh)} {
		requireExporterResources(t, sts,
			map[string]string{"cpu": "50m", "memory": "64Mi"},
			map[string]string{"cpu": "1", "memory": "256Mi"})
	}

	sh = withExporterResources(corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")}, nil)
	requireExporterResources(t, BuildShardStatefulSet(sh, 0),
		map[string]string{"cpu": "50m", "memory": "128Mi"},
		map[string]string{"cpu": "200m", "memory": "256Mi"})
}

func TestMemberExporterResources_AllSet(t *testing.T) {
	sh := withExporterResources(
		corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("96Mi")},
		corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("512Mi")},
	)

	requireExporterResources(t, BuildShardStatefulSet(sh, 0),
		map[string]string{"cpu": "100m", "memory": "96Mi"},
		map[string]string{"cpu": "2", "memory": "512Mi"})
}

// A zero limit removes that limit; the other limit and both requests stay.
func TestMemberExporterResources_ZeroLimitRemoves(t *testing.T) {
	sh := withExporterResources(nil, corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("0")})

	requireExporterResources(t, BuildShardStatefulSet(sh, 0),
		map[string]string{"cpu": "50m", "memory": "64Mi"},
		map[string]string{"memory": "256Mi"})
}

// A limit below the request lowers the request to it, so the pod template
// stays valid whichever side was left at its built-in value.
func TestMemberExporterResources_RequestCappedByLimit(t *testing.T) {
	sh := withExporterResources(nil, corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("30m")})
	requireExporterResources(t, BuildShardStatefulSet(sh, 0),
		map[string]string{"cpu": "30m", "memory": "64Mi"},
		map[string]string{"cpu": "30m", "memory": "256Mi"})

	sh = withExporterResources(corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")}, nil)
	requireExporterResources(t, BuildShardStatefulSet(sh, 0),
		map[string]string{"cpu": "50m", "memory": "256Mi"},
		map[string]string{"cpu": "200m", "memory": "256Mi"})
}

// Resource names without a built-in value pass through.
func TestMemberExporterResources_OtherNames(t *testing.T) {
	sh := withExporterResources(nil, corev1.ResourceList{corev1.ResourceEphemeralStorage: resource.MustParse("1Gi")})

	requireExporterResources(t, BuildShardStatefulSet(sh, 0),
		map[string]string{"cpu": "50m", "memory": "64Mi"},
		map[string]string{"cpu": "200m", "memory": "256Mi", "ephemeral-storage": "1Gi"})
}

// The block alone adds no sidecar, and an override does not leak into the
// next build or back into the spec.
func TestMemberExporterResources_NoSideEffects(t *testing.T) {
	off := goldenShardSharded()
	//lint:ignore SA1019 the member exporter fields of MonitoringSpec are read.
	off.Spec.Monitoring = &mongodbv1alpha1.MonitoringSpec{ //nolint:staticcheck
		Exporter: &mongodbv1alpha1.ExporterSpec{Resources: mongodbv1alpha1.ResourcesSpec{
			Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
		}},
	}
	require.Equal(t, string(shardStatefulSetJSON(t, goldenShardSharded())), string(shardStatefulSetJSON(t, off)))

	bare := withMemberMonitoring(shardedWithAuth(), mongodbv1alpha1.MemberMonitoringEnabled)
	want := memberSetsJSON(t, bare)
	sh := withExporterResources(nil, corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("30m")})
	BuildShardStatefulSet(sh, 0)

	require.Equal(t, want, memberSetsJSON(t, bare))
	require.Empty(t, sh.Spec.Monitoring.Exporter.Resources.Requests)
	require.Equal(t, map[string]string{"cpu": "30m"}, quantities(sh.Spec.Monitoring.Exporter.Resources.Limits))
}
