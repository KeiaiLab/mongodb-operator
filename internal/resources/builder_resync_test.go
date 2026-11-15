/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Resync marker — spec.shards.resync / spec.configServer.resync = Marker 가
// 표지 init container 와 선택적 ConfigMap volume 을 더하는지, Disabled · 비움은
// 렌더가 바이트 동일한지 잠근다.

package resources

import (
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func initNamed(sts *appsv1.StatefulSet, name string) *corev1.Container {
	for i := range sts.Spec.Template.Spec.InitContainers {
		if sts.Spec.Template.Spec.InitContainers[i].Name == name {
			return &sts.Spec.Template.Spec.InitContainers[i]
		}
	}
	return nil
}

func volumeNamed(sts *appsv1.StatefulSet, name string) *corev1.Volume {
	for i := range sts.Spec.Template.Spec.Volumes {
		if sts.Spec.Template.Spec.Volumes[i].Name == name {
			return &sts.Spec.Template.Spec.Volumes[i]
		}
	}
	return nil
}

func envValue(c *corev1.Container, name string) *corev1.EnvVar {
	for i := range c.Env {
		if c.Env[i].Name == name {
			return &c.Env[i]
		}
	}
	return nil
}

func requireResyncInit(t *testing.T, sts *appsv1.StatefulSet, cr, dbpath string) {
	t.Helper()
	c := initNamed(sts, "resync-marker")
	require.NotNil(t, c)
	require.Equal(t, "copy-keyfile", sts.Spec.Template.Spec.InitContainers[0].Name)
	require.Equal(t, c, &sts.Spec.Template.Spec.InitContainers[1])
	require.Equal(t, keyfileInitImage, c.Image)
	require.Equal(t, []string{"sh", "-c", buildResyncScript()}, c.Command)
	require.Equal(t, buildKeyfileInitContainerSecurityContext(), c.SecurityContext)

	require.Equal(t, dbpath, envValue(c, "DBPATH").Value)
	require.Equal(t, "/resync", envValue(c, "RESYNC_DIR").Value)
	require.Equal(t, "metadata.name", envValue(c, "POD_NAME").ValueFrom.FieldRef.FieldPath)
	require.ElementsMatch(t, []corev1.VolumeMount{
		{Name: "data", MountPath: dbpath},
		{Name: "resync-marker", MountPath: "/resync", ReadOnly: true},
	}, c.VolumeMounts)

	v := volumeNamed(sts, "resync-marker")
	require.NotNil(t, v)
	require.NotNil(t, v.ConfigMap)
	require.Equal(t, cr+"-resync", v.ConfigMap.Name)
	require.True(t, *v.ConfigMap.Optional)
}

// TestResync_DisabledKeepsGolden — Disabled 는 칸 없음과 바이트 동일(롤링 0).
func TestResync_DisabledKeepsGolden(t *testing.T) {
	sh := goldenShardSharded()
	sh.Spec.Shards.Resync = mongodbv1alpha1.ResyncDisabled
	require.Equal(t, string(shardStatefulSetJSON(t, goldenShardSharded())), string(shardStatefulSetJSON(t, sh)))

	cfg := goldenShardSharded()
	cfg.Spec.ConfigServer.Resync = mongodbv1alpha1.ResyncDisabled
	require.Equal(t, BuildConfigServerStatefulSet(goldenShardSharded()), BuildConfigServerStatefulSet(cfg))
	require.Nil(t, initNamed(BuildConfigServerStatefulSet(cfg), "resync-marker"))
}

// TestResync_MarkerShard — shard 는 /data/db 를 비운다(mongot search-index subPath 포함).
func TestResync_MarkerShard(t *testing.T) {
	sh := withMongot(shardedWithAuth(), "shard-0")
	sh.Spec.Shards.Resync = mongodbv1alpha1.ResyncMarker
	sts := BuildShardStatefulSet(sh, 0)

	requireResyncInit(t, sts, sh.Name, "/data/db")
	require.Nil(t, initNamed(BuildConfigServerStatefulSet(sh), "resync-marker"))
}

// TestResync_MarkerConfigServer — cfg 는 /data/configdb 를 비운다.
func TestResync_MarkerConfigServer(t *testing.T) {
	sh := shardedWithAuth()
	sh.Spec.ConfigServer.Resync = mongodbv1alpha1.ResyncMarker

	requireResyncInit(t, BuildConfigServerStatefulSet(sh), sh.Name, "/data/configdb")
	require.Nil(t, initNamed(BuildShardStatefulSet(sh, 0), "resync-marker"))
}
