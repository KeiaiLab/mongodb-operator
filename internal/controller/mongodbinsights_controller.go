/*
Copyright 2026 Keiailab.
*/

// mongodbinsights_controller.go — F51-F55 (cycle 7) Insights skeleton.
//
// 본 cycle 의 acceptance: CRD watch + phase 전이 + recommendation slot.
// 실 system.profile 분석 + index 추천 엔진은 cycle 9 강화.

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
	insightsPhasePending   = "Pending"
	insightsPhaseAnalyzing = "Analyzing"
	insightsPhaseReady     = "Ready"
)

// MongoDBInsightsReconciler — F51 skeleton.
type MongoDBInsightsReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbinsights,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbinsights/status,verbs=get;update;patch

func (r *MongoDBInsightsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("insights", req.NamespacedName)

	in := &mongodbv1alpha1.MongoDBInsights{}
	if err := r.Get(ctx, req.NamespacedName, in); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !in.Spec.Enabled {
		logger.V(1).Info("insights disabled, noop")
		return ctrl.Result{}, nil
	}

	// 첫 reconcile: Pending → Analyzing 전이 + LastAnalysisTime stamp.
	now := metav1.Now()
	switch in.Status.Phase {
	case "", insightsPhasePending:
		in.Status.Phase = insightsPhaseAnalyzing
		in.Status.LastAnalysisTime = &now
	case insightsPhaseAnalyzing:
		// 실제 분석은 cycle 9 강화 — 본 cycle 은 skeleton 으로 Ready 전이.
		in.Status.Phase = insightsPhaseReady
		in.Status.LastAnalysisTime = &now
	case insightsPhaseReady:
		// 다음 cycle 대기 (analysisInterval).
	}

	if err := r.Status().Update(ctx, in); err != nil {
		logger.V(1).Info("status update may be transient", "err", err)
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers the reconciler.
func (r *MongoDBInsightsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDBInsights{}).
		Named("mongodb-insights").
		Complete(r)
}
