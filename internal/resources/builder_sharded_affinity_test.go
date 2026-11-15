/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Sharded pod affinity — spec.shards.pod.affinity / spec.configServer.pod.affinity
// 가 operator 기본 preferred spread 와 합쳐지는지 잠근다.

package resources

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	hostnameKey  = "kubernetes.io/hostname"
	componentKey = "app.kubernetes.io/component"
	localNodeKey = "keiailab.dev/mongo-local"
)

// perRSHostAntiAffinity — 한 항목이 RS 별 required host anti-affinity 가 된다
// (matchLabelKeys 가 component = shard-N · configsvr 로 나눈다).
func perRSHostAntiAffinity(instance string) *corev1.Affinity {
	return &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{
				Weight: 100,
				Preference: corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key: localNodeKey, Operator: corev1.NodeSelectorOpDoesNotExist,
				}}},
			}},
		},
		PodAntiAffinity: &corev1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app.kubernetes.io/instance": instance},
				},
				MatchLabelKeys: []string{componentKey},
				TopologyKey:    hostnameKey,
			}},
		},
	}
}

func requireMerged(t *testing.T, got, user *corev1.Affinity, instance string) {
	t.Helper()
	def := buildDefaultAffinity(instance)
	require.Equal(t, user.NodeAffinity, got.NodeAffinity)
	require.Equal(t, user.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
		got.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution)
	require.Equal(t, def.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
		got.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution)
}

func TestShardStatefulSet_MergesPodAffinity(t *testing.T) {
	sh := shardedWithAuth()
	user := perRSHostAntiAffinity(sh.Name)
	sh.Spec.Shards.Pod = &mongodbv1alpha1.PodSpec{Affinity: user}

	got := BuildShardStatefulSet(sh, 1).Spec.Template.Spec.Affinity
	requireMerged(t, got, user, sh.Name)
}

func TestConfigServerStatefulSet_MergesPodAffinity(t *testing.T) {
	sh := shardedWithAuth()
	user := perRSHostAntiAffinity(sh.Name)
	sh.Spec.ConfigServer.Pod = &mongodbv1alpha1.PodSpec{Affinity: user}

	got := BuildConfigServerStatefulSet(sh).Spec.Template.Spec.Affinity
	requireMerged(t, got, user, sh.Name)
}

// 사용자 preferred 항은 기본 spread 뒤에 붙고, podAffinity 는 그대로 간다.
func TestShardStatefulSet_AppendsPreferredAndPodAffinity(t *testing.T) {
	sh := shardedWithAuth()
	extra := corev1.WeightedPodAffinityTerm{
		Weight: 10,
		PodAffinityTerm: corev1.PodAffinityTerm{
			LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"x": "y"}},
			TopologyKey:   "topology.kubernetes.io/zone",
		},
	}
	podAff := &corev1.PodAffinity{
		PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{extra},
	}
	sh.Spec.Shards.Pod = &mongodbv1alpha1.PodSpec{Affinity: &corev1.Affinity{
		PodAffinity:     podAff,
		PodAntiAffinity: &corev1.PodAntiAffinity{PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{extra}},
	}}

	got := BuildShardStatefulSet(sh, 0).Spec.Template.Spec.Affinity
	pref := got.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	require.Len(t, pref, 2)
	require.Equal(t, buildDefaultAffinity(sh.Name).PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0], pref[0])
	require.Equal(t, extra, pref[1])
	require.Equal(t, podAff, got.PodAffinity)
}

// 합침이 CR 의 affinity 를 바꾸지 않는다.
func TestShardStatefulSet_AffinityMergeLeavesSpec(t *testing.T) {
	sh := shardedWithAuth()
	user := perRSHostAntiAffinity(sh.Name)
	sh.Spec.Shards.Pod = &mongodbv1alpha1.PodSpec{Affinity: user}
	before := user.DeepCopy()

	got := BuildShardStatefulSet(sh, 0).Spec.Template.Spec.Affinity
	got.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].TopologyKey = "mutated"
	got.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = nil

	require.Equal(t, before, sh.Spec.Shards.Pod.Affinity)
}

// affinity 없음(pod nil · pod.affinity nil) = 기본 affinity 그대로(롤링 0).
func TestShardedStatefulSets_NilAffinityIsDefault(t *testing.T) {
	sh := shardedWithAuth()
	def := buildDefaultAffinity(sh.Name)
	require.Equal(t, def, BuildShardStatefulSet(sh, 0).Spec.Template.Spec.Affinity)
	require.Equal(t, def, BuildConfigServerStatefulSet(sh).Spec.Template.Spec.Affinity)

	sh.Spec.Shards.Pod = &mongodbv1alpha1.PodSpec{}
	sh.Spec.ConfigServer.Pod = &mongodbv1alpha1.PodSpec{}
	require.Equal(t, def, BuildShardStatefulSet(sh, 0).Spec.Template.Spec.Affinity)
	require.Equal(t, def, BuildConfigServerStatefulSet(sh).Spec.Template.Spec.Affinity)
}
