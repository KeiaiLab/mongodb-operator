/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// mongodContainer is the mongod container name in shard and cfg pods.
const mongodContainer = "mongodb"

// replicaSetUp is the wait gate before the config server or a shard step.
//
//	Ready members == want                      -> open
//	MemberState and the RS not initiated yet   -> open when every mongod runs
//	otherwise                                  -> closed
//
// A MemberState member is neither PRIMARY nor SECONDARY before rs.initiate,
// so it cannot turn Ready until the operator initiates the RS; waiting for
// Ready there would never end.
func (r *MongoDBShardedReconciler) replicaSetUp(ctx context.Context, sts *appsv1.StatefulSet, want int32, mode mongodbv1alpha1.ReadinessMode, initialized bool) bool {
	if sts.Status.ReadyReplicas == want {
		return true
	}
	if mode != mongodbv1alpha1.ReadinessMemberState || initialized {
		return false
	}
	return r.mongodRunning(ctx, sts, want)
}

// mongodRunning reports whether want pods of the set run their mongod container.
func (r *MongoDBShardedReconciler) mongodRunning(ctx context.Context, sts *appsv1.StatefulSet, want int32) bool {
	if sts.Spec.Selector == nil {
		return false
	}

	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(sts.Namespace), client.MatchingLabels(sts.Spec.Selector.MatchLabels)); err != nil {
		return false
	}

	var running int32
	for i := range pods.Items {
		if podRunsMongod(&pods.Items[i]) {
			running++
		}
	}
	return running == want
}

func podRunsMongod(pod *corev1.Pod) bool {
	if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == mongodContainer {
			return cs.State.Running != nil
		}
	}
	return false
}
