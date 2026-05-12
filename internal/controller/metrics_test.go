/*
Copyright 2026 Keiailab.
*/

package controller

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
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
	if count < 30 {
		t.Errorf("expected at least 30 mongodb_* metrics registered, got %d", count)
	}
	t.Logf("registered mongodb_* metrics: %d", count)
}

func TestDefaultPrometheusAlertRules_Generation(t *testing.T) {
	t.Parallel()
	yaml := DefaultPrometheusAlertRules("default", "my-cluster")
	for _, want := range []string{
		"apiVersion: monitoring.coreos.com/v1",
		"kind: PrometheusRule",
		"name: my-cluster-alerts",
		"namespace: default",
		"alert: MongoDBSlowQuerySpike",
		"alert: MongoDBHighReplicationLag",
		"alert: MongoDBPrimaryFailover",
		"alert: MongoDBBackupFailure",
		"alert: MongoDBOplogUploaderDown",
		"alert: MongoDBAuthFailureSpike",
		"alert: MongoDBFederationDegraded",
		"alert: MongoDBReconcileErrors",
		`mongodb_replication_lag_seconds{name="my-cluster"}`,
		"severity: critical",
		"severity: warning",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("PrometheusRule YAML must contain %q, got:\n%s", want, yaml)
		}
	}
	// 15 rule 정합 검증
	ruleCount := strings.Count(yaml, "    - alert: ")
	if ruleCount < 15 {
		t.Errorf("expected at least 15 alert rules, got %d", ruleCount)
	}
}

// 메트릭 cardinality cleanup 회귀.
func TestDeleteMetricsFor_Cleanup(t *testing.T) {
	t.Parallel()
	MetricReconcileTotal.WithLabelValues("ns-cleanup", "name-cleanup").Inc()
	DeleteMetricsFor("ns-cleanup", "name-cleanup")
}
