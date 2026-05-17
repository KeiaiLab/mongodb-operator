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
	"go.mongodb.org/mongo-driver/v2/mongo/options"

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

	creds, err := r.loadAnalysisCredentials(ctx, in, mdb)
	if err != nil {
		return nil, 0, err
	}

	host := fmt.Sprintf("%s-headless.%s.svc.cluster.local:27017", mdb.Name, mdb.Namespace)
	cli, err := mongoclient.NewClient(ctx, mongoclient.ConnectOpts{
		Hosts:      []string{host},
		Username:   creds.Username,
		Password:   creds.Password,
		AuthDB:     creds.AuthDB,
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

// analysisCredentials — mongo connect 자격증명 정합.
type analysisCredentials struct {
	Username string
	Password string
	AuthDB   string
}

// loadAnalysisCredentials — Spec.AnalysisCredentialsSecretRef 우선,
// 미설정 시 cluster 의 Auth.AdminCredentialsSecretRef 재사용.
//
// Codex review (RFC-0045) #5 fix: 이전 구현은 secret 의 password 만 읽고
// username/authDB 를 admin/admin 으로 고정 — AnalysisCredentialsSecretRef 가
// 분리된 read-only user 라는 spec 의도를 무시. 본 fix 는 secret 의 username +
// authDB 필드를 우선 읽고, ref 미설정 시에만 admin/admin fallback.
func (r *MongoDBInsightsReconciler) loadAnalysisCredentials(ctx context.Context, in *mongodbv1alpha1.MongoDBInsights, mdb *mongodbv1alpha1.MongoDB) (analysisCredentials, error) {
	custom := in.Spec.AnalysisCredentialsSecretRef != nil && in.Spec.AnalysisCredentialsSecretRef.Name != ""
	var name string
	if custom {
		name = in.Spec.AnalysisCredentialsSecretRef.Name
	} else {
		name = mdb.Spec.Auth.AdminCredentialsSecretRef.Name
	}
	if name == "" {
		return analysisCredentials{}, fmt.Errorf("no credentials secret resolvable for analysis")
	}
	sec := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: in.Namespace}, sec); err != nil {
		return analysisCredentials{}, fmt.Errorf("get secret %s: %w", name, err)
	}
	pw, ok := sec.Data["password"]
	if !ok {
		return analysisCredentials{}, fmt.Errorf("secret %s missing 'password' key", name)
	}
	creds := analysisCredentials{Password: string(pw)}
	if u, ok := sec.Data["username"]; ok && len(u) > 0 {
		creds.Username = string(u)
	}
	if a, ok := sec.Data["authDB"]; ok && len(a) > 0 {
		creds.AuthDB = string(a)
	} else if a, ok := sec.Data["authSource"]; ok && len(a) > 0 {
		// mongosh 관례: authSource. 양쪽 키 허용.
		creds.AuthDB = string(a)
	}
	// admin fallback — ref 미설정 시에만 (spec 의도 보존).
	if !custom {
		if creds.Username == "" {
			creds.Username = "admin"
		}
		if creds.AuthDB == "" {
			creds.AuthDB = "admin"
		}
	} else {
		if creds.Username == "" {
			return analysisCredentials{}, fmt.Errorf("secret %s missing 'username' key (required for AnalysisCredentialsSecretRef)", name)
		}
		if creds.AuthDB == "" {
			// custom secret 인데 authDB 미설정 시 admin 으로 fallback (mongo 기본).
			creds.AuthDB = "admin"
		}
	}
	return creds, nil
}

// collectProfileDocs — listDatabases → 각 DB 의 system.profile 에서
// 최신 sampleSize 문서 수집 (admin/local/config 제외).
//
// Codex review (RFC-0045) #1 fix: Find 옵션에 SetSort(ts:-1) + SetLimit 적용.
// 이전 구현은 limit 없이 cursor 전체를 메모리에 읽은 뒤 잘라 — 대형 profile
// 컬렉션에서 OOM 위험 + 최신 정렬 미보장. 본 fix 는 server-side sort + limit
// + global cap.
func collectProfileDocs(ctx context.Context, cli *mongo.Client, sampleSize int32) ([]insights.ProfileDoc, error) {
	dbNames, err := cli.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("listDatabases: %w", err)
	}

	// 분석 대상 DB 만 carve out (admin/local/config 제외).
	targetDBs := make([]string, 0, len(dbNames))
	for _, n := range dbNames {
		switch n {
		case insightsSkipDBAdmin, insightsSkipDBLocal, insightsSkipDBConfig:
			continue
		}
		targetDBs = append(targetDBs, n)
	}

	perDBLimit := int64(sampleSize)
	if len(targetDBs) > 0 {
		// 균등 분배 — 최소 10 보장.
		share := int64(sampleSize) / int64(len(targetDBs))
		if share < 10 {
			share = 10
		}
		perDBLimit = share
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "ts", Value: -1}}).
		SetLimit(perDBLimit)

	var all []insights.ProfileDoc
	globalCap := int(sampleSize)
	for _, dbName := range targetDBs {
		coll := cli.Database(dbName).Collection("system.profile")
		cursor, err := coll.Find(ctx, bson.D{}, findOpts)
		if err != nil {
			// profiling 비활성 DB 는 system.profile 없음 — skip.
			continue
		}
		for cursor.Next(ctx) {
			if len(all) >= globalCap {
				break
			}
			var m bson.M
			if err := cursor.Decode(&m); err != nil {
				continue
			}
			all = append(all, convertProfile(m))
		}
		_ = cursor.Close(ctx)
		if len(all) >= globalCap {
			break
		}
	}
	return all, nil
}

// convertProfile — bson.M (system.profile row) → ProfileDoc.
//
// Codex review (RFC-0045) #2 fix: BSON 의 실제 variance 대응.
// - filter shape 후보: top-level `filter` | `command.filter` |
//   `command.q` (legacy) | `command.pipeline[0].$match` (aggregation)
// - sort shape 후보: top-level `sort` | `command.sort`
// - 값 타입 후보: bson.M | bson.D | map[string]any (driver 버전·decode 모드별)
// - 배열 타입 후보: bson.A | []any | []interface{}
func convertProfile(m bson.M) insights.ProfileDoc {
	d := insights.ProfileDoc{}
	if v, ok := m["op"].(string); ok {
		d.Op = v
	}
	if v, ok := m["ns"].(string); ok {
		d.NS = v
	}
	d.Millis = int32(readInt64Any(m["millis"]))
	if v, ok := m["planSummary"].(string); ok {
		d.PlanSummary = v
	}

	cmd := normalizeMap(m["command"])
	switch {
	case m["filter"] != nil:
		d.Filter = normalizeMap(m["filter"])
	case cmd != nil && cmd["filter"] != nil:
		d.Filter = normalizeMap(cmd["filter"])
	case cmd != nil && cmd["q"] != nil:
		// legacy `query` op 의 filter 위치.
		d.Filter = normalizeMap(cmd["q"])
	case cmd != nil && cmd["pipeline"] != nil:
		// aggregation: pipeline[0] 의 $match.
		if arr := normalizeSlice(cmd["pipeline"]); len(arr) > 0 {
			if stage := normalizeMap(arr[0]); stage != nil {
				if mt, ok := stage["$match"]; ok {
					d.Filter = normalizeMap(mt)
				}
			}
		}
	}

	switch {
	case m["sort"] != nil:
		d.Sort = normalizeMap(m["sort"])
	case cmd != nil && cmd["sort"] != nil:
		d.Sort = normalizeMap(cmd["sort"])
	}

	d.DocsExamined = readInt64Any(m["docsExamined"])
	d.NReturned = readInt64Any(m["nreturned"])
	if d.NReturned == 0 {
		// command op 의 경우 nreturned 가 command 응답 안.
		d.NReturned = readInt64Any(m["nReturned"])
	}
	d.KeysExamined = readInt64Any(m["keysExamined"])
	return d
}

// normalizeMap — bson.M | bson.D | map[string]any → map[string]any.
// nil 또는 인식 불가 시 nil 반환.
func normalizeMap(v any) map[string]any {
	switch m := v.(type) {
	case nil:
		return nil
	case bson.M:
		out := make(map[string]any, len(m))
		for k, vv := range m {
			out[k] = vv
		}
		return out
	case map[string]any:
		// 호출자 mutation 방지 위해 얕은 복사.
		out := make(map[string]any, len(m))
		for k, vv := range m {
			out[k] = vv
		}
		return out
	case bson.D:
		out := make(map[string]any, len(m))
		for _, e := range m {
			out[e.Key] = e.Value
		}
		return out
	}
	return nil
}

// normalizeSlice — bson.A | []any → []any.
func normalizeSlice(v any) []any {
	switch s := v.(type) {
	case nil:
		return nil
	case bson.A:
		out := make([]any, len(s))
		copy(out, s)
		return out
	case []any:
		out := make([]any, len(s))
		copy(out, s)
		return out
	}
	return nil
}

// readInt64Any — int32 | int64 | float64 | nil → int64.
func readInt64Any(v any) int64 {
	switch x := v.(type) {
	case int32:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case int:
		return int64(x)
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
