/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// mongot_data_memory_test.go — mongot data 를 메모리 emptyDir 로 옮기는 annotation 회귀 가드.
// 값이 양의 quantity 일 때만 옮기고, 없거나 틀리면 mongod data PVC subPath 그대로(무롤링).
package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const mongotDataMemoryLimit = "2Gi"

func mongotDataMount(t *testing.T, spec corev1.PodSpec) corev1.VolumeMount {
	t.Helper()
	for _, c := range spec.Containers {
		if c.Name != "mongot" {
			continue
		}
		for _, m := range c.VolumeMounts {
			if m.MountPath == mongotBasePath {
				return m
			}
		}
	}
	t.Fatal("mongot data mount 부재")
	return corev1.VolumeMount{}
}

func assertMongotDataInMemory(t *testing.T, spec corev1.PodSpec) {
	t.Helper()
	mount := mongotDataMount(t, spec)
	assert.Equal(t, mongotDataMemoryVolume, mount.Name)
	assert.Empty(t, mount.SubPath, "메모리 볼륨은 통째로 mount")

	var source *corev1.EmptyDirVolumeSource
	for _, v := range spec.Volumes {
		if v.Name == mongotDataMemoryVolume {
			source = v.EmptyDir
		}
	}
	require.NotNil(t, source, "mongot-data emptyDir 부재")
	assert.Equal(t, corev1.StorageMediumMemory, source.Medium)
	require.NotNil(t, source.SizeLimit)
	assert.Equal(t, mongotDataMemoryLimit, source.SizeLimit.String())
}

func searchReplicaSet(annotations map[string]string) *mongodbv1alpha1.MongoDB {
	return &mongodbv1alpha1.MongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "ks", Namespace: "data", Annotations: annotations},
		Spec: mongodbv1alpha1.MongoDBSpec{
			Members:        3,
			ReplicaSetName: "rs0",
			Version:        mongodbv1alpha1.MongoDBVersion{Version: "8.3"},
			Storage:        mongodbv1alpha1.StorageSpec{Size: resource.MustParse("1Gi")},
		},
	}
}

// TestShardedMongot_DataMemory — annotation 이 양의 quantity → 모든 shard 의 mongot data 가
// 메모리 emptyDir(sizeLimit = 값)이고 mongod data PVC 를 mount 하지 않는다.
func TestShardedMongot_DataMemory(t *testing.T) {
	sh := newTestSharded()
	sh.Annotations = mongotAnnotations()
	sh.Annotations[MongotDataMemoryAnnotation] = mongotDataMemoryLimit

	for i := int32(0); i < sh.Spec.Shards.Count; i++ {
		assertMongotDataInMemory(t, BuildShardStatefulSet(sh, i).Spec.Template.Spec)
	}
}

// TestShardedMongot_DataMemoryUnsetOrInvalid — 값이 없거나 양의 quantity 가 아니면 pod spec 이
// annotation 없는 판과 같다(무롤링 · 영속 기본으로 fail-safe).
func TestShardedMongot_DataMemoryUnsetOrInvalid(t *testing.T) {
	base := newTestSharded()
	base.Annotations = mongotAnnotations()
	want := BuildShardStatefulSet(base, 0).Spec.Template.Spec
	assert.Equal(t, mongodDataVolume, mongotDataMount(t, want).Name)

	for _, value := range []string{"", "lots", "0", "-1Gi"} {
		sh := newTestSharded()
		sh.Annotations = mongotAnnotations()
		sh.Annotations[MongotDataMemoryAnnotation] = value
		assert.Equal(t, want, BuildShardStatefulSet(sh, 0).Spec.Template.Spec, "value %q", value)
	}
}

// TestMongod_SearchSidecar_DataMemory — RS 경로도 같은 annotation 을 따른다.
func TestMongod_SearchSidecar_DataMemory(t *testing.T) {
	annotations := map[string]string{
		MongotSidecarImageAnnotation: shardedMongotImage,
		MongotSyncSecretAnnotation:   "ks-sync",
		MongotTLSModeAnnotation:      "disabled",
	}
	persistent := BuildReplicaSetStatefulSet(searchReplicaSet(annotations)).Spec.Template.Spec
	assert.Equal(t, mongodDataVolume, mongotDataMount(t, persistent).Name)

	annotations[MongotDataMemoryAnnotation] = mongotDataMemoryLimit
	assertMongotDataInMemory(t, BuildReplicaSetStatefulSet(searchReplicaSet(annotations)).Spec.Template.Spec)
}
