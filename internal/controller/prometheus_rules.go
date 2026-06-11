/*
Copyright 2026 Keiailab.
*/

// prometheus_rules.go — F22 (cycle 11) PrometheusRule 자동 생성 helper.
//
// MongoDB / MongoDBSharded 의 Spec.Monitoring.PrometheusRule.Enabled 가 true 일
// 때 reconciler 가 본 함수를 호출하여 표준 alerting rule (slow query,
// replication lag, primary failover, backup failure, audit event spike) 을
// 생성한다. 결과는 monitoring.coreos.com/v1 PrometheusRule CR.
//
// v0.11.0: strings.Builder YAML 수작업 조립 → keiailab-commons pkg/monitoring
// NewPrometheusRule 구조화 빌더로 교체 (unstructured CR 반환 — 호출자가 apply).

package controller

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	commonsmonitoring "github.com/keiailab/keiailab-commons/pkg/monitoring"
)

// BuildDefaultPrometheusRule 은 cluster 이름과 namespace 를 받아 표준
// alerting rule 15종을 담은 PrometheusRule CR (unstructured) 을 반환한다.
// caller (reconciler) 가 client 로 apply.
func BuildDefaultPrometheusRule(namespace, clusterName string) *unstructured.Unstructured {
	rules := []struct {
		alert    string
		expr     string
		severity string
		summary  string
	}{
		{"MongoDBSlowQuerySpike",
			fmt.Sprintf(`rate(mongodb_slow_query_total{name="%s"}[5m]) > 1`, clusterName),
			"warning", "Slow queries spiking on MongoDB cluster"},
		{"MongoDBHighReplicationLag",
			fmt.Sprintf(`max(mongodb_replication_lag_seconds{name="%s"}) > 30`, clusterName),
			"critical", "Replication lag exceeds 30 seconds"},
		{"MongoDBOplogWindowTooShort",
			fmt.Sprintf(`mongodb_oplog_window_hours{name="%s"} < 1`, clusterName),
			"warning", "Oplog retention window under 1 hour — PITR at risk"},
		{"MongoDBPrimaryFailover",
			fmt.Sprintf(`increase(mongodb_primary_failover_total{name="%s"}[15m]) > 0`, clusterName),
			"critical", "Primary failover detected"},
		{"MongoDBUnhealthyMembers",
			fmt.Sprintf(`mongodb_replicaset_members{name="%s"} - mongodb_replicaset_healthy_members{name="%s"} > 0`, clusterName, clusterName),
			"critical", "One or more replica set members are unhealthy"},
		{"MongoDBHighConnectionUsage",
			fmt.Sprintf(`(mongodb_connections_active{name="%s"} / (mongodb_connections_active{name="%s"} + mongodb_connections_available{name="%s"})) > 0.85`, clusterName, clusterName, clusterName),
			"warning", "Connection pool utilization above 85%"},
		{"MongoDBConnectionRejection",
			fmt.Sprintf(`rate(mongodb_connections_rejected_total{name="%s"}[5m]) > 0`, clusterName),
			"critical", "Connections being rejected"},
		{"MongoDBStorageNearCapacity",
			fmt.Sprintf(`(mongodb_storage_used_bytes{name="%s"} / mongodb_storage_capacity_bytes{name="%s"}) > 0.85`, clusterName, clusterName),
			"warning", "Storage usage above 85% capacity"},
		{"MongoDBWiredTigerCachePressure",
			fmt.Sprintf(`(mongodb_wiredtiger_cache_used_bytes{name="%s"} / mongodb_wiredtiger_cache_configured_bytes{name="%s"}) > 0.95`, clusterName, clusterName),
			"warning", "WiredTiger cache above 95% — eviction pressure"},
		{"MongoDBCollectionScans",
			fmt.Sprintf(`rate(mongodb_collection_scans_total{name="%s"}[5m]) > 0.1`, clusterName),
			"warning", "Frequent full collection scans — missing index?"},
		{"MongoDBBackupFailure",
			fmt.Sprintf(`increase(mongodb_backup_failures_total{name="%s"}[1h]) > 0`, clusterName),
			"critical", "Backup failed in the last hour"},
		{"MongoDBOplogUploaderDown",
			fmt.Sprintf(`mongodb_oplog_uploader_active{name="%s"} == 0`, clusterName),
			"critical", "PITR oplog uploader inactive while PITR is enabled"},
		{"MongoDBAuthFailureSpike",
			fmt.Sprintf(`rate(mongodb_audit_events_total{name="%s",atype="authenticate"}[5m]) > 10`, clusterName),
			"warning", "Authentication event rate above threshold (possible brute-force)"},
		{"MongoDBFederationDegraded",
			fmt.Sprintf(`mongodb_federation_regions_synced{name="%s"} > 0 and absent(mongodb_federation_regions_synced{name="%s"} == 2)`, clusterName, clusterName),
			"warning", "Federation regions not fully synced"},
		{"MongoDBReconcileErrors",
			fmt.Sprintf(`rate(mongodb_reconcile_errors_total{name="%s"}[5m]) > 0`, clusterName),
			"warning", "Operator reconciler reporting errors"},
	}

	alerts := make([]commonsmonitoring.AlertRule, 0, len(rules))
	for _, r := range rules {
		alerts = append(alerts, commonsmonitoring.AlertRule{
			Alert:       r.alert,
			Expr:        r.expr,
			For:         "5m",
			Labels:      map[string]string{"severity": r.severity},
			Annotations: map[string]string{"summary": r.summary},
		})
	}

	return commonsmonitoring.NewPrometheusRule(
		clusterName+"-alerts", namespace, nil,
		commonsmonitoring.RuleGroup{
			Name:   "mongodb-operator-alerts",
			Alerts: alerts,
		},
	)
}
