/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// mongodbinsights_controller.go — ROADMAP §3.2 cycle 9 강화.
//
// Codex review (RFC-0045) #5 근본 fix 적용 후: controller 는 *수집 + 분석*
// dispatcher 역할만. system.profile 수집은 insights.ProfileFetcher 에 위임.
// mongo-driver / bson / k8s Secret 직접 의존 제거.

package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/insights"
)

const (
	insightsPhasePending   = "Pending"
	insightsPhaseAnalyzing = "Analyzing"
	insightsPhaseReady     = "Ready"
	insightsPhaseFailed    = "Failed"

	insightsFailedRequeue = 5 * time.Minute
)

// MongoDBInsightsReconciler reconciles MongoDBInsights CR.
//
// Codex re-review (RFC-0045) #1 critical fix: controller-level FetcherFactory
// hook 을 *완전 제거*. 이전 FetcherFactory 는 ProfileFetcher 전체를 교체 가능
// → ClusterRef 조회 + Secret 로드 + mongo connect + collect + convert 5
// 책임 *모두* 우회 가능 (Codex 첫 review #5 와 동일한 함정 재발). 본 refactor
// 는 controller 가 *항상* MongoProfileFetcher 를 사용하도록 강제. 테스트는
// 분리: ① analyzer unit (synthetic ProfileDoc) ② fetcher unit (nil-guard +
// FakeProfileFetcher interface 검증) ③ envtest controller suite (k8s lookup
// 통합 — mongo connect 실패는 Failed phase 로 valid).
type MongoDBInsightsReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbinsights,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbinsights/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mongodb.keiailab.com,resources=mongodbs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list

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

	now := metav1.Now()
	switch in.Status.Phase {
	case "", insightsPhasePending:
		in.Status.Phase = insightsPhaseAnalyzing
		in.Status.LastAnalysisTime = &now
		if err := r.Status().Update(ctx, in); err != nil {
			logger.V(1).Info("Pending→Analyzing status update transient", "err", err)
		}
		return ctrl.Result{Requeue: true}, nil

	case insightsPhaseAnalyzing:
		recs, sampled, err := r.runAnalysis(ctx, in)
		if err != nil {
			logger.Info("analysis failed, will retry", "err", err)
			MetricInsightsAnalysisTotal.WithLabelValues(in.Namespace, in.Name, "error").Inc()
			in.Status.Phase = insightsPhaseFailed
			in.Status.LastAnalysisTime = &now
			setInsightsCondition(&in.Status.Conditions, metav1.Condition{
				Type:               "AnalysisHealthy",
				Status:             metav1.ConditionFalse,
				Reason:             "AnalysisError",
				Message:            err.Error(),
				LastTransitionTime: now,
			})
			if statusErr := r.Status().Update(ctx, in); statusErr != nil {
				logger.V(1).Info("Failed status update transient", "err", statusErr)
			}
			return ctrl.Result{RequeueAfter: insightsFailedRequeue}, nil
		}
		// 성공 path 메트릭 (cycle 9 P2 ROADMAP §3.2).
		MetricInsightsAnalysisTotal.WithLabelValues(in.Namespace, in.Name, "success").Inc()
		MetricInsightsSampledTotal.WithLabelValues(in.Namespace, in.Name).Add(float64(sampled))
		// 이전 cycle 의 active recommendations 카운트 reset 후 재기록 (gauge semantics).
		MetricInsightsRecommendations.DeletePartialMatch(prometheus.Labels{
			"namespace": in.Namespace, "name": in.Name,
		})
		for _, rec := range recs {
			MetricInsightsRecommendations.WithLabelValues(
				in.Namespace, in.Name, rec.Type, rec.Severity,
			).Inc()
		}
		in.Status.Phase = insightsPhaseReady
		in.Status.LastAnalysisTime = &now
		in.Status.Recommendations = recs
		in.Status.SlowQueriesSampled = sampled
		// Auto Pilot advisory (ROADMAP §3.2 / Level V): 권장 기반 조치 계획을
		// 표면화 (DryRun 기본 — 비가역 운영 자동 실행 없음).
		in.Status.AutoPilotActions = buildAutoPilotActions(recs, in.Spec.AutoIndex, in.Spec.AutoQueryHint)
		setInsightsCondition(&in.Status.Conditions, metav1.Condition{
			Type:               "AnalysisHealthy",
			Status:             metav1.ConditionTrue,
			Reason:             "AnalysisSucceeded",
			Message:            fmt.Sprintf("%d recommendations from %d profile docs", len(recs), sampled),
			LastTransitionTime: now,
		})
		if err := r.Status().Update(ctx, in); err != nil {
			logger.V(1).Info("Ready status update transient", "err", err)
		}
		return ctrl.Result{RequeueAfter: parseAnalysisInterval(in.Spec.AnalysisInterval)}, nil

	case insightsPhaseReady, insightsPhaseFailed:
		// 다음 cycle 시 재진입을 위해 Analyzing 으로 되돌림.
		in.Status.Phase = insightsPhaseAnalyzing
		if err := r.Status().Update(ctx, in); err != nil {
			logger.V(1).Info("re-enter Analyzing status update transient", "err", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// runAnalysis — fetcher.Fetch + insights.Analyze 2-step.
func (r *MongoDBInsightsReconciler) runAnalysis(ctx context.Context, in *mongodbv1alpha1.MongoDBInsights) ([]mongodbv1alpha1.Recommendation, int32, error) {
	fetcher := &insights.MongoProfileFetcher{
		K8sClient: r.Client,
		Insights:  in,
	}
	sampleSize := in.Spec.SampleSize
	if sampleSize <= 0 {
		sampleSize = 500
	}
	docs, err := fetcher.Fetch(ctx, sampleSize)
	if err != nil {
		return nil, 0, err
	}
	recs := insights.Analyze(docs, in.Spec.SlowQueryThresholdMs)

	// UnusedIndex 분석 (ROADMAP §3.2). index stats 수집 실패는 *비치명* —
	// profile 기반 recommendation 은 유지 ($indexStats 권한 부족 / 부분 실패 흡수).
	if stats, statsErr := fetcher.FetchIndexStats(ctx); statsErr != nil {
		log.FromContext(ctx).V(1).Info("index stats fetch 실패 (비치명, profile 분석 유지)", "err", statsErr)
	} else {
		recs = append(recs, insights.AnalyzeIndexUsage(stats)...)
	}

	return recs, int32(len(docs)), nil
}

func parseAnalysisInterval(s string) time.Duration {
	if s == "" {
		return 15 * time.Minute
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 15 * time.Minute
	}
	return d
}

// setInsightsCondition — Conditions 슬라이스의 동일 Type 항목 갱신 또는 append.
// k8s meta/v1 의 SetStatusCondition 유사. 외부 의존 최소화 위해 inline.
func setInsightsCondition(conds *[]metav1.Condition, c metav1.Condition) {
	for i, existing := range *conds {
		if existing.Type == c.Type {
			if existing.Status == c.Status {
				c.LastTransitionTime = existing.LastTransitionTime
			}
			(*conds)[i] = c
			return
		}
	}
	*conds = append(*conds, c)
}

// SetupWithManager registers the reconciler.
func (r *MongoDBInsightsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1alpha1.MongoDBInsights{}).
		Named("mongodb-insights").
		Complete(r)
}
