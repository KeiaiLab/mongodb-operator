/*
Copyright 2026 Keiailab.
*/

package controller

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// F17 (cycle 11): 30+ 메트릭 등록 정합 검증.
// 각 metric vec 에 dummy label 값으로 1 observation 을 추가하여 Gather
// 결과에 노출시킨 뒤 mongodb_ subsystem 개수 집계.
func TestMetricsCount_AtLeast30(t *testing.T) {
	t.Parallel()
	// dummy observation 으로 각 metric 을 활성화.
	MetricReconcileTotal.WithLabelValues("ns-test", "name-test").Inc()
	MetricReconcileLatency.WithLabelValues("ns-test", "name-test", "success").Observe(0.1)
	MetricReconcileErrors.WithLabelValues("ns-test", "name-test", "component").Inc()
	MetricQueryLatencySeconds.WithLabelValues("ns-test", "name-test", "find").Observe(0.05)
	MetricQueryIndexUsageRatio.WithLabelValues("ns-test", "name-test").Set(0.9)
	MetricSlowQueryTotal.WithLabelValues("ns-test", "name-test").Inc()
	MetricCollectionScansTotal.WithLabelValues("ns-test", "name-test").Inc()
	MetricQueriesIssuedTotal.WithLabelValues("ns-test", "name-test", "find").Inc()
	MetricReplicationLagSeconds.WithLabelValues("ns-test", "name-test", "m0").Set(1.0)
	MetricOplogWindowHours.WithLabelValues("ns-test", "name-test").Set(24)
	MetricReplicaSetMembers.WithLabelValues("ns-test", "name-test").Set(3)
	MetricReplicaSetHealthyMembers.WithLabelValues("ns-test", "name-test").Set(3)
	MetricPrimaryFailoverTotal.WithLabelValues("ns-test", "name-test").Inc()
	MetricHeartbeatFailuresTotal.WithLabelValues("ns-test", "name-test", "m1").Inc()
	MetricStorageUsedBytes.WithLabelValues("ns-test", "name-test").Set(1e9)
	MetricStorageCapacityBytes.WithLabelValues("ns-test", "name-test").Set(1e10)
	MetricWiredTigerCacheUsedBytes.WithLabelValues("ns-test", "name-test").Set(5e8)
	MetricWiredTigerCacheConfiguredBytes.WithLabelValues("ns-test", "name-test").Set(1e9)
	MetricStorageCompressionRatio.WithLabelValues("ns-test", "name-test").Set(2.0)
	MetricConnectionsActive.WithLabelValues("ns-test", "name-test").Set(10)
	MetricConnectionsAvailable.WithLabelValues("ns-test", "name-test").Set(100)
	MetricConnectionsWaiting.WithLabelValues("ns-test", "name-test").Set(0)
	MetricConnectionsRejectedTotal.WithLabelValues("ns-test", "name-test").Inc()
	MetricBackupPhase.WithLabelValues("ns-test", "name-test", "Completed").Set(2)
	MetricBackupDurationSeconds.WithLabelValues("ns-test", "name-test", "b1").Set(30)
	MetricBackupFailuresTotal.WithLabelValues("ns-test", "name-test").Inc()
	MetricOplogUploaderActive.WithLabelValues("ns-test", "name-test").Set(1)
	MetricBackupIOThrottledBytesTotal.WithLabelValues("ns-test", "name-test", "read").Inc()
	MetricAuditEventsTotal.WithLabelValues("ns-test", "name-test", "authenticate").Inc()
	MetricEncryptionEnabled.WithLabelValues("ns-test", "name-test").Set(1)
	MetricKeyRotationTotal.WithLabelValues("ns-test", "name-test").Inc()
	MetricFederationRegionsSynced.WithLabelValues("ns-test", "name-test").Set(2)
	MetricClusterGroupMembers.WithLabelValues("ns-test", "name-test").Set(3)
	// insights (cycle 9 P2)
	MetricInsightsRecommendations.WithLabelValues("ns-test", "name-test", "MissingIndex", "warning").Set(2)
	MetricInsightsAnalysisTotal.WithLabelValues("ns-test", "name-test", "success").Inc()
	MetricInsightsSampledTotal.WithLabelValues("ns-test", "name-test").Add(500)

	gathered, err := metrics.Registry.(prometheus.Gatherer).Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	count := 0
	for _, mf := range gathered {
		if strings.HasPrefix(*mf.Name, "mongodb_") {
			count++
		}
	}
	// cycle 9 P2: 33 → 36 metrics (3 insights metrics 추가).
	if count < 33 {
		t.Errorf("expected at least 33 mongodb_* metrics registered, got %d", count)
	}
	t.Logf("registered mongodb_* metrics: %d", count)
}

func TestDefaultPrometheusAlertRules_Generation(t *testing.T) {
	t.Parallel()
	// v0.11.0: YAML 문자열 조립 → commons monitoring.NewPrometheusRule 구조화
	// 빌더 교체. 동일 행동 검증 (CR GVK / 이름 / 15 rule / alert 내용 / severity).
	obj := BuildDefaultPrometheusRule("default", "my-cluster")
	if got := obj.GetAPIVersion(); got != "monitoring.coreos.com/v1" {
		t.Errorf("apiVersion = %q, want monitoring.coreos.com/v1", got)
	}
	if got := obj.GetKind(); got != "PrometheusRule" {
		t.Errorf("kind = %q, want PrometheusRule", got)
	}
	if got := obj.GetName(); got != "my-cluster-alerts" {
		t.Errorf("name = %q, want my-cluster-alerts", got)
	}
	if got := obj.GetNamespace(); got != "default" {
		t.Errorf("namespace = %q, want default", got)
	}

	groups, found, err := unstructured.NestedSlice(obj.Object, "spec", "groups")
	if err != nil || !found || len(groups) != 1 {
		t.Fatalf("spec.groups 1건 기대, found=%v err=%v len=%d", found, err, len(groups))
	}
	group, ok := groups[0].(map[string]any)
	if !ok || group["name"] != "mongodb-operator-alerts" {
		t.Fatalf("group name = %v, want mongodb-operator-alerts", group["name"])
	}
	rules, _ := group["rules"].([]any)
	// 15 rule 정합 검증
	if len(rules) < 15 {
		t.Errorf("expected at least 15 alert rules, got %d", len(rules))
	}

	alerts := map[string]bool{}
	severities := map[string]bool{}
	var lagExpr string
	for _, r := range rules {
		rm, _ := r.(map[string]any)
		alert, _ := rm["alert"].(string)
		alerts[alert] = true
		if labels, ok := rm["labels"].(map[string]any); ok {
			if sev, ok := labels["severity"].(string); ok {
				severities[sev] = true
			}
		}
		if alert == "MongoDBHighReplicationLag" {
			lagExpr, _ = rm["expr"].(string)
		}
		if f, _ := rm["for"].(string); f != "5m" {
			t.Errorf("rule %s for = %q, want 5m", alert, f)
		}
	}
	for _, want := range []string{
		"MongoDBSlowQuerySpike", "MongoDBHighReplicationLag", "MongoDBPrimaryFailover",
		"MongoDBBackupFailure", "MongoDBOplogUploaderDown", "MongoDBAuthFailureSpike",
		"MongoDBFederationDegraded", "MongoDBReconcileErrors",
	} {
		if !alerts[want] {
			t.Errorf("PrometheusRule must contain alert %q", want)
		}
	}
	if !strings.Contains(lagExpr, `mongodb_replication_lag_seconds{name="my-cluster"}`) {
		t.Errorf("MongoDBHighReplicationLag expr = %q, want mongodb_replication_lag_seconds selector", lagExpr)
	}
	if !severities["critical"] || !severities["warning"] {
		t.Errorf("severity critical+warning 모두 필요, got %v", severities)
	}
}

// 메트릭 cardinality cleanup 회귀.
func TestDeleteMetricsFor_Cleanup(t *testing.T) {
	t.Parallel()
	MetricReconcileTotal.WithLabelValues("ns-cleanup", "name-cleanup").Inc()
	DeleteMetricsFor("ns-cleanup", "name-cleanup")
}
