/*
Copyright 2026 Keiailab.
*/

package resources

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// defaultedTopologySpread — MongoDB ReplicaSet 의 HA defaults. valkey-operator
// PR #48 + postgres-operator (동일 PR) 패턴 cross-operator 이식.
//
// members >= 2 + user TSC 미명시 시 zone + node 2-축 spread 자동 inject.
// MaxSkew=1 + ScheduleAnyway (single-zone cluster 호환).
//
// members=1 (Standalone PoC) 시 미주입.
func defaultedTopologySpread(
	user []corev1.TopologySpreadConstraint,
	members int32,
	selector map[string]string,
) []corev1.TopologySpreadConstraint {
	if len(user) > 0 {
		return user
	}
	if members < 2 {
		return nil
	}
	labelSelector := &metav1.LabelSelector{MatchLabels: selector}
	return []corev1.TopologySpreadConstraint{
		{
			MaxSkew:           1,
			TopologyKey:       "topology.kubernetes.io/zone",
			WhenUnsatisfiable: corev1.ScheduleAnyway,
			LabelSelector:     labelSelector,
		},
		{
			MaxSkew:           1,
			TopologyKey:       "kubernetes.io/hostname",
			WhenUnsatisfiable: corev1.ScheduleAnyway,
			LabelSelector:     labelSelector,
		},
	}
}
