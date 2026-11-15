/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	corev1 "k8s.io/api/core/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// buildPodAffinity merges the CR pod affinity into the operator default.
//
//	default : preferred host spread over the whole instance (weight 100)
//	user    : nodeAffinity, podAffinity          -> taken as given
//	          podAntiAffinity.required           -> taken as given (e.g. a
//	                                                per-RS host rule via
//	                                                matchLabelKeys)
//	          podAntiAffinity.preferred          -> appended after the default
//
// No user affinity returns the default unchanged, so existing pods do not roll.
func buildPodAffinity(instanceName string, pod *mongodbv1alpha1.PodSpec) *corev1.Affinity {
	merged := buildDefaultAffinity(instanceName)
	if pod == nil || pod.Affinity == nil {
		return merged
	}
	user := pod.Affinity.DeepCopy()

	merged.NodeAffinity = user.NodeAffinity
	merged.PodAffinity = user.PodAffinity
	if user.PodAntiAffinity == nil {
		return merged
	}

	anti := merged.PodAntiAffinity
	anti.RequiredDuringSchedulingIgnoredDuringExecution = user.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	anti.PreferredDuringSchedulingIgnoredDuringExecution = append(
		anti.PreferredDuringSchedulingIgnoredDuringExecution,
		user.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution...)
	return merged
}
