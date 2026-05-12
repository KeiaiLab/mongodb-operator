/*
Copyright 2026 Keiailab.
*/

// mongodbfederation_controller.go — F33-F37 (cycle 5) skeleton reconciler.
//
// cycle 5 acceptance: CRD watch + reconcile loop + region status update.
// 실 cross-cluster kubeconfig consumer (remote API call) 는 cycle 8 강화.

package controller

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	federationPhasePending       = "Pending"
	federationPhaseBootstrapping = "Bootstrapping"
	federationPhaseSynced        = "Synced"
	federationPhaseDegraded      = "Degraded"
)

// MongoDBFederationReconciler reconciles a MongoDBFederation object.
type MongoDBFederationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbfederations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbfederations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbfederations/finalizers,verbs=update

func (r *MongoDBFederationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("federation", req.NamespacedName)

	fed := &mongodbv1alpha1.MongoDBFederation{}
	if err := r.Get(ctx, req.NamespacedName, fed); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Phase 결정: regions 의 LastSyncTime 가 nil 인 region 이 있으면 Bootstrapping.
	// 모두 Synced 이면 Synced. 하나라도 Failed 이면 Degraded.
	fed.Status.Phase = computeFederationPhase(fed)
	if fed.Status.RegionStatuses == nil {
		fed.Status.RegionStatuses = make([]mongodbv1alpha1.FederationRegionStatus, 0, len(fed.Spec.Regions))
	}

	// 신규 region 은 Pending 상태로 status 에 ensure.
	for _, region := range fed.Spec.Regions {
		if !hasRegionStatus(fed.Status.RegionStatuses, region.Name) {
			fed.Status.RegionStatuses = append(fed.Status.RegionStatuses, mongodbv1alpha1.FederationRegionStatus{
				Name:         region.Name,
				Phase:        federationPhasePending,
				LastSyncTime: &metav1.Time{Time: metav1.Now().Time},
			})
		}
	}

	if err := r.Status().Update(ctx, fed); err != nil {
		logger.V(1).Info("status update failed (may be transient)", "err", err)
	}

	logger.V(1).Info("federation reconciled (skeleton — cycle 8 강화 예정)",
		"regions", len(fed.Spec.Regions),
		"phase", fed.Status.Phase)
	return ctrl.Result{}, nil
}

func hasRegionStatus(statuses []mongodbv1alpha1.FederationRegionStatus, name string) bool {
	for _, s := range statuses {
		if s.Name == name {
			return true
		}
	}
	return false
}

func computeFederationPhase(fed *mongodbv1alpha1.MongoDBFederation) string {
	if len(fed.Status.RegionStatuses) == 0 {
		return federationPhaseBootstrapping
	}
	syncedCount, failedCount := 0, 0
	for _, s := range fed.Status.RegionStatuses {
		switch s.Phase {
		case federationPhaseSynced:
			syncedCount++
		case "Failed":
			failedCount++
		}
	}
	if failedCount > 0 {
		return federationPhaseDegraded
	}
	if syncedCount == len(fed.Status.RegionStatuses) {
		return federationPhaseSynced
	}
	return federationPhaseBootstrapping
}

// SetupWithManager registers the federation reconciler.
func (r *MongoDBFederationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDBFederation{}).
		Named("mongodb-federation").
		Complete(r)
}
