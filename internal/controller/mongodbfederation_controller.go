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
	federationPhaseFailed        = "Failed"
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

	// 신규 region 은 Pending 상태로 status 에 ensure + cycle 17 cross-cluster
	// propagation 실행. propagateRegionMongoDB 가 *remote K8s API 에 실제* GET 호출.
	for _, region := range fed.Spec.Regions {
		idx := indexOfRegion(fed.Status.RegionStatuses, region.Name)
		if idx < 0 {
			fed.Status.RegionStatuses = append(fed.Status.RegionStatuses, mongodbv1alpha1.FederationRegionStatus{
				Name:         region.Name,
				Phase:        federationPhasePending,
				LastSyncTime: &metav1.Time{Time: metav1.Now().Time},
			})
			idx = len(fed.Status.RegionStatuses) - 1
		}
		synced, server, err := r.propagateRegionMongoDB(ctx, fed, region)
		now := metav1.Time{Time: metav1.Now().Time}
		fed.Status.RegionStatuses[idx].LastSyncTime = &now
		if synced {
			fed.Status.RegionStatuses[idx].Phase = federationPhaseSynced
			fed.Status.RegionStatuses[idx].Error = ""
			logger.V(1).Info("region synced", "region", region.Name, "server", server)
		} else if err != nil {
			fed.Status.RegionStatuses[idx].Phase = federationPhaseFailed
			fed.Status.RegionStatuses[idx].Error = err.Error()
		}
	}

	// Phase 재산출 (region status 갱신 후).
	fed.Status.Phase = computeFederationPhase(fed)

	if err := r.Status().Update(ctx, fed); err != nil {
		// status update 실패를 silent 유실하지 않고 error 로 반환해 재큐 + 재시도.
		logger.Error(err, "federation status update failed")
		return ctrl.Result{}, err
	}

	logger.V(1).Info("federation reconciled (cycle 17 cross-cluster propagation)",
		"regions", len(fed.Spec.Regions),
		"phase", fed.Status.Phase)
	return ctrl.Result{}, nil
}

// indexOfRegion — region name 의 index 또는 -1.
func indexOfRegion(statuses []mongodbv1alpha1.FederationRegionStatus, name string) int {
	for i, s := range statuses {
		if s.Name == name {
			return i
		}
	}
	return -1
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
		case federationPhaseFailed:
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
