/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// mongodbinsights_controller.go — ROADMAP §3.2 cycle 9 강화.
//
// system.profile 실 분석 엔진 통합. 분석 로직 자체는 internal/insights.Analyze
// (순수 함수) 로 분리되어 unit test 로 회귀 가드. 본 controller 는 *클러스터
// connect + profile 수집 + analyze 호출 + Status 갱신* 만 담당.

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/insights"
	mongoclient "github.com/keiailab/mongodb-operator/internal/mongodb"
)

const (
	insightsPhasePending   = "Pending"
	insightsPhaseAnalyzing = "Analyzing"
	insightsPhaseReady     = "Ready"
	insightsPhaseFailed    = "Failed"

	// 분석 실패 시 backoff. AnalysisInterval 파싱 실패해도 최소 보장.
	insightsFailedRequeue = 5 * time.Minute

	// 분석 대상에서 제외할 시스템 DB.
	insightsSkipDBAdmin  = "admin"
	insightsSkipDBLocal  = "local"
	insightsSkipDBConfig = "config"
)

// MongoDBInsightsReconciler reconciles MongoDBInsights CR.
type MongoDBInsightsReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// AnalyzeOverride — 테스트/주입용 hook. nil 이면 실 mongo 호출 사용.
	// 시그니처: ctx + insights CR → (recommendations, sampledCount, err).
	AnalyzeOverride func(ctx context.Context, in *mongodbv1alpha1.MongoDBInsights) ([]mongodbv1alpha1.Recommendation, int32, error)
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
			in.Status.Phase = insightsPhaseFailed
			in.Status.LastAnalysisTime = &now
			meta_setCondition(&in.Status.Conditions, metav1.Condition{
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
		in.Status.Phase = insightsPhaseReady
		in.Status.LastAnalysisTime = &now
		in.Status.Recommendations = recs
		in.Status.SlowQueriesSampled = sampled
		meta_setCondition(&in.Status.Conditions, metav1.Condition{
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

// runAnalysis — AnalyzeOverride 우선, 없으면 실 mongo 호출.
func (r *MongoDBInsightsReconciler) runAnalysis(ctx context.Context, in *mongodbv1alpha1.MongoDBInsights) ([]mongodbv1alpha1.Recommendation, int32, error) {
	if r.AnalyzeOverride != nil {
		return r.AnalyzeOverride(ctx, in)
	}
	return r.analyzeFromCluster(ctx, in)
}

// analyzeFromCluster — 실 cluster 의 system.profile 수집 + Analyze.
func (r *MongoDBInsightsReconciler) analyzeFromCluster(ctx context.Context, in *mongodbv1alpha1.MongoDBInsights) ([]mongodbv1alpha1.Recommendation, int32, error) {
	if in.Spec.ClusterRef.Kind != "MongoDB" {
		// MongoDBSharded 는 후속 sub-task.
		return nil, 0, fmt.Errorf("unsupported ClusterRef.Kind %q (only MongoDB in this cycle)", in.Spec.ClusterRef.Kind)
	}

	mdb := &mongodbv1alpha1.MongoDB{}
	if err := r.Get(ctx, types.NamespacedName{Name: in.Spec.ClusterRef.Name, Namespace: in.Namespace}, mdb); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, 0, fmt.Errorf("ClusterRef MongoDB/%s not found", in.Spec.ClusterRef.Name)
		}
		return nil, 0, fmt.Errorf("get MongoDB: %w", err)
	}

	password, err := r.loadAnalysisCredentials(ctx, in, mdb)
	if err != nil {
		return nil, 0, err
	}

	host := fmt.Sprintf("%s-headless.%s.svc.cluster.local:27017", mdb.Name, mdb.Namespace)
	cli, err := mongoclient.NewClient(ctx, mongoclient.ConnectOpts{
		Hosts:      []string{host},
		Username:   "admin",
		Password:   password,
		AuthDB:     "admin",
		ReplicaSet: mdb.Name,
		Timeout:    10 * time.Second,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("connect %s: %w", host, err)
	}
	defer func() { _ = cli.Disconnect(ctx) }()

	sampleSize := in.Spec.SampleSize
	if sampleSize <= 0 {
		sampleSize = 500
	}
	docs, err := collectProfileDocs(ctx, cli, sampleSize)
	if err != nil {
		return nil, 0, fmt.Errorf("collect profile: %w", err)
	}

	recs := insights.Analyze(docs, in.Spec.SlowQueryThresholdMs)
	return recs, int32(len(docs)), nil
}

// loadAnalysisCredentials — Spec.AnalysisCredentialsSecretRef 우선,
// 미설정 시 cluster 의 Auth.AdminCredentialsSecretRef 재사용.
func (r *MongoDBInsightsReconciler) loadAnalysisCredentials(ctx context.Context, in *mongodbv1alpha1.MongoDBInsights, mdb *mongodbv1alpha1.MongoDB) (string, error) {
	var name string
	if in.Spec.AnalysisCredentialsSecretRef != nil && in.Spec.AnalysisCredentialsSecretRef.Name != "" {
		name = in.Spec.AnalysisCredentialsSecretRef.Name
	} else {
		name = mdb.Spec.Auth.AdminCredentialsSecretRef.Name
	}
	if name == "" {
		return "", fmt.Errorf("no credentials secret resolvable for analysis")
	}
	sec := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: in.Namespace}, sec); err != nil {
		return "", fmt.Errorf("get secret %s: %w", name, err)
	}
	pw, ok := sec.Data["password"]
	if !ok {
		return "", fmt.Errorf("secret %s missing 'password' key", name)
	}
	return string(pw), nil
}

// collectProfileDocs — listDatabases → 각 DB 의 system.profile 에서
// limit=sampleSize 만큼 최신 문서 수집 (admin/local/config 제외).
func collectProfileDocs(ctx context.Context, cli *mongo.Client, sampleSize int32) ([]insights.ProfileDoc, error) {
	dbNames, err := cli.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("listDatabases: %w", err)
	}

	perDBLimit := int64(sampleSize)
	if len(dbNames) > 0 {
		// 균등 분배 — 최소 10 보장.
		share := int64(sampleSize) / int64(len(dbNames))
		if share < 10 {
			share = 10
		}
		perDBLimit = share
	}

	var all []insights.ProfileDoc
	for _, dbName := range dbNames {
		switch dbName {
		case insightsSkipDBAdmin, insightsSkipDBLocal, insightsSkipDBConfig:
			continue
		}
		coll := cli.Database(dbName).Collection("system.profile")
		cursor, err := coll.Find(ctx, bson.D{}, nil)
		if err != nil {
			// profiling 비활성 DB 는 system.profile 없음 — skip.
			continue
		}
		var raw []bson.M
		if err := cursor.All(ctx, &raw); err != nil {
			_ = cursor.Close(ctx)
			continue
		}
		_ = cursor.Close(ctx)
		for i, m := range raw {
			if int64(i) >= perDBLimit {
				break
			}
			all = append(all, convertProfile(m))
		}
	}
	return all, nil
}

// convertProfile — bson.M (system.profile row) → ProfileDoc.
func convertProfile(m bson.M) insights.ProfileDoc {
	d := insights.ProfileDoc{}
	if v, ok := m["op"].(string); ok {
		d.Op = v
	}
	if v, ok := m["ns"].(string); ok {
		d.NS = v
	}
	if v, ok := m["millis"].(int32); ok {
		d.Millis = v
	} else if v, ok := m["millis"].(int64); ok {
		d.Millis = int32(v)
	}
	if v, ok := m["planSummary"].(string); ok {
		d.PlanSummary = v
	}
	if v, ok := m["filter"].(bson.M); ok {
		d.Filter = bsonMToMap(v)
	} else if v, ok := m["command"].(bson.M); ok {
		// command op 의 경우 filter 가 command.filter 안.
		if f, ok := v["filter"].(bson.M); ok {
			d.Filter = bsonMToMap(f)
		}
	}
	if v, ok := m["sort"].(bson.M); ok {
		d.Sort = bsonMToMap(v)
	}
	d.DocsExamined = readInt64(m, "docsExamined")
	d.NReturned = readInt64(m, "nreturned")
	d.KeysExamined = readInt64(m, "keysExamined")
	return d
}

func bsonMToMap(m bson.M) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func readInt64(m bson.M, key string) int64 {
	switch v := m[key].(type) {
	case int32:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
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

// meta_setCondition — Conditions 슬라이스의 동일 Type 항목 갱신 또는 append.
// k8s meta/v1 의 SetStatusCondition 유사. 본 controller 외부 의존 최소화 위해
// inline 작성.
func meta_setCondition(conds *[]metav1.Condition, c metav1.Condition) {
	for i, existing := range *conds {
		if existing.Type == c.Type {
			// Status 동일 → 기존 LastTransitionTime 유지. 다르면 새 시각 그대로.
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
