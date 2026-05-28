/*
Copyright 2026 Keiailab.
*/

// mongodbclustergroup_controller.go — F56-F60 (cycle 8) skeleton.
//
// cycle 8 acceptance: CRD watch + member status ensure + policy compliance
// 검사. 실 cross-cluster reconcile + 전역 사용자 propagation 은 cycle 9+.

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	groupPhasePending     = "Pending"
	groupPhaseReconciling = "Reconciling"
	groupPhaseSynced      = "Synced"
	groupPhaseDegraded    = "Degraded"
)

// MongoDBClusterGroupReconciler — F56 skeleton.
type MongoDBClusterGroupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbclustergroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbclustergroups/status,verbs=get;update;patch

func (r *MongoDBClusterGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("clustergroup", req.NamespacedName)

	g := &mongodbv1alpha1.MongoDBClusterGroup{}
	if err := r.Get(ctx, req.NamespacedName, g); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Phase 결정 — 매우 단순한 skeleton: 모든 member 가 PolicyCompliant 면 Synced.
	g.Status.Phase = computeClusterGroupPhase(g)

	// 신규 member 는 Pending 상태로 status 에 ensure.
	for _, m := range g.Spec.Members {
		if !hasMemberStatus(g.Status.MemberStatuses, m.Name) {
			g.Status.MemberStatuses = append(g.Status.MemberStatuses, mongodbv1alpha1.ClusterGroupMemberStatus{
				Name:            m.Name,
				Phase:           groupPhasePending,
				PolicyCompliant: false,
			})
		}
	}

	if err := r.Status().Update(ctx, g); err != nil {
		logger.V(1).Info("status update may be transient", "err", err)
	}
	logger.V(1).Info("clustergroup reconciled (skeleton)", "members", len(g.Spec.Members), "phase", g.Status.Phase)
	return ctrl.Result{}, nil
}

func hasMemberStatus(statuses []mongodbv1alpha1.ClusterGroupMemberStatus, name string) bool {
	for _, s := range statuses {
		if s.Name == name {
			return true
		}
	}
	return false
}

func computeClusterGroupPhase(g *mongodbv1alpha1.MongoDBClusterGroup) string {
	if len(g.Status.MemberStatuses) == 0 {
		return groupPhasePending
	}
	syncedCount, failedCount := 0, 0
	for _, s := range g.Status.MemberStatuses {
		switch s.Phase {
		case groupPhaseSynced:
			syncedCount++
		case backupPhaseFailed:
			failedCount++
		}
	}
	if failedCount > 0 {
		return groupPhaseDegraded
	}
	if syncedCount == len(g.Status.MemberStatuses) {
		return groupPhaseSynced
	}
	return groupPhaseReconciling
}

// SetupWithManager registers the reconciler.
func (r *MongoDBClusterGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDBClusterGroup{}).
		Named("mongodb-clustergroup").
		Complete(r)
}
