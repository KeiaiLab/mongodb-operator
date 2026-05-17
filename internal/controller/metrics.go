/*
Copyright 2026 Keiailab.
*/

// Package controller — Prometheus metrics 정의.
//
// controller-runtime 의 글로벌 metrics registry 자동 등록. valkey-operator PR #47
// + postgres-operator PR #34 cross-operator 이식 — SLO 추적.
package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const metricSubsystem = "mongodb"

var labelNamespaceName = []string{"namespace", "name"}

var (
	// --- Reconcile (3) — existing ---
	MetricReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "reconcile_total", Help: "Total Reconcile invocations"},
		labelNamespaceName,
	)
	MetricReconcileLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: metricSubsystem, Name: "reconcile_duration_seconds",
			Help:    "Reconcile function wall-clock duration in seconds",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0},
		},
		[]string{"namespace", "name", "result"},
	)
	MetricReconcileErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "reconcile_errors_total", Help: "Total Reconcile component failures"},
		[]string{"namespace", "name", "component"},
	)

	// --- F17/F18 Query performance (5) — cycle 11 ---
	MetricQueryLatencySeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Subsystem: metricSubsystem, Name: "query_latency_seconds", Help: "MongoDB query latency observed by operator probes", Buckets: prometheus.ExponentialBuckets(0.001, 2, 14)},
		[]string{"namespace", "name", "op_type"},
	)
	MetricQueryIndexUsageRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "query_index_usage_ratio", Help: "Ratio of queries using indexes (0..1)"}, labelNamespaceName)
	MetricSlowQueryTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "slow_query_total", Help: "Total slow queries observed (above SlowQueryThresholdMs)"}, labelNamespaceName)
	MetricCollectionScansTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "collection_scans_total", Help: "Total full collection scans"}, labelNamespaceName)
	MetricQueriesIssuedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "queries_issued_total", Help: "Total client queries observed"}, []string{"namespace", "name", "op_type"})

	// --- F19 Replication (6) — cycle 11 ---
	MetricReplicationLagSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "replication_lag_seconds", Help: "Per-member replication lag in seconds"}, []string{"namespace", "name", "member"})
	MetricOplogWindowHours = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "oplog_window_hours", Help: "Current oplog retention window in hours"}, labelNamespaceName)
	MetricReplicaSetMembers = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "replicaset_members", Help: "Total members in the replica set"}, labelNamespaceName)
	MetricReplicaSetHealthyMembers = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "replicaset_healthy_members", Help: "Number of healthy members"}, labelNamespaceName)
	MetricPrimaryFailoverTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "primary_failover_total", Help: "Total primary failovers detected"}, labelNamespaceName)
	MetricHeartbeatFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "heartbeat_failures_total", Help: "Total RS heartbeat failures"}, []string{"namespace", "name", "member"})

	// --- F20 Storage / WiredTiger (5) — cycle 11 ---
	MetricStorageUsedBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "storage_used_bytes", Help: "Current storage bytes used"}, labelNamespaceName)
	MetricStorageCapacityBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "storage_capacity_bytes", Help: "PVC capacity bytes"}, labelNamespaceName)
	MetricWiredTigerCacheUsedBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "wiredtiger_cache_used_bytes", Help: "WiredTiger cache used bytes"}, labelNamespaceName)
	MetricWiredTigerCacheConfiguredBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "wiredtiger_cache_configured_bytes", Help: "WiredTiger cache configured bytes"}, labelNamespaceName)
	MetricStorageCompressionRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "storage_compression_ratio", Help: "WiredTiger compression ratio (uncompressed/compressed)"}, labelNamespaceName)

	// --- F21 Connection pool (4) — cycle 11 ---
	MetricConnectionsActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "connections_active", Help: "Active client connections"}, labelNamespaceName)
	MetricConnectionsAvailable = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "connections_available", Help: "Available connection pool slots"}, labelNamespaceName)
	MetricConnectionsWaiting = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "connections_waiting", Help: "Clients waiting for a connection slot"}, labelNamespaceName)
	MetricConnectionsRejectedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "connections_rejected_total", Help: "Total connection attempts rejected"}, labelNamespaceName)

	// --- Backup / PITR (5) — cycle 11, supports backup.json dashboard ---
	MetricBackupPhase = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "backup_phase", Help: "Current backup phase enum (0=Pending,1=Running,2=Completed,3=Failed,4=Restoring)"}, []string{"namespace", "name", "phase"})
	MetricBackupDurationSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "backup_duration_seconds", Help: "Backup wall-clock duration of last completion"}, []string{"namespace", "name", "backup"})
	MetricBackupFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "backup_failures_total", Help: "Total backup failures"}, labelNamespaceName)
	MetricOplogUploaderActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "oplog_uploader_active", Help: "Oplog uploader is active for the cluster (1=active)"}, labelNamespaceName)
	MetricBackupIOThrottledBytesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "backup_io_throttled_bytes_total", Help: "Total bytes delayed by backup throttle"}, []string{"namespace", "name", "direction"})

	// --- Audit / KMS / Federation (5) — cycle 11 ---
	MetricAuditEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "audit_events_total", Help: "Total MongoDB audit events"}, []string{"namespace", "name", "atype"})
	MetricEncryptionEnabled = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "encryption_enabled", Help: "1 if encryption-at-rest enabled"}, labelNamespaceName)
	MetricKeyRotationTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "key_rotation_total", Help: "Total KMS key rotations performed"}, labelNamespaceName)
	MetricFederationRegionsSynced = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "federation_regions_synced", Help: "Number of federation regions in Synced phase"}, labelNamespaceName)
	MetricClusterGroupMembers = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "clustergroup_members", Help: "Total members in MongoDBClusterGroup"}, labelNamespaceName)

	// --- MongoDBInsights (3) — cycle 9 P2 (ROADMAP §3.2) ---
	// `mongodb_insights_recommendations` Gauge — 현재 active recommendations
	// 수 (type × severity 별). 분석 cycle 마다 reset + 재기록.
	MetricInsightsRecommendations = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Subsystem: metricSubsystem, Name: "insights_recommendations",
			Help: "Current active MongoDBInsights recommendations by type and severity"},
		[]string{"namespace", "name", "type", "severity"})
	// `mongodb_insights_analysis_total` Counter — 분석 사이클 시도 누적
	// (result=success|error). Reconcile loop 의 가시성 + SLO 추적.
	MetricInsightsAnalysisTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "insights_analysis_total",
			Help: "Total MongoDBInsights analysis cycles attempted, partitioned by result"},
		[]string{"namespace", "name", "result"})
	// `mongodb_insights_sampled_total` Counter — 수집된 system.profile 문서 누적.
	// fetcher.Fetch 의 throughput 가시성 + analyzer 입력 추적.
	MetricInsightsSampledTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Subsystem: metricSubsystem, Name: "insights_sampled_total",
			Help: "Total MongoDBInsights profile docs sampled across analysis cycles"},
		labelNamespaceName)

	// Total: 3 (existing reconcile) + 5 (query) + 6 (replication) + 5 (storage)
	//      + 4 (connections) + 5 (backup) + 5 (audit/kms/fed)
	//      + 3 (insights, cycle 9 P2) = 36 metrics.
)

func init() {
	metrics.Registry.MustRegister(
		// reconcile (3)
		MetricReconcileTotal, MetricReconcileLatency, MetricReconcileErrors,
		// query (5)
		MetricQueryLatencySeconds, MetricQueryIndexUsageRatio, MetricSlowQueryTotal,
		MetricCollectionScansTotal, MetricQueriesIssuedTotal,
		// replication (6)
		MetricReplicationLagSeconds, MetricOplogWindowHours, MetricReplicaSetMembers,
		MetricReplicaSetHealthyMembers, MetricPrimaryFailoverTotal, MetricHeartbeatFailuresTotal,
		// storage (5)
		MetricStorageUsedBytes, MetricStorageCapacityBytes, MetricWiredTigerCacheUsedBytes,
		MetricWiredTigerCacheConfiguredBytes, MetricStorageCompressionRatio,
		// connections (4)
		MetricConnectionsActive, MetricConnectionsAvailable, MetricConnectionsWaiting,
		MetricConnectionsRejectedTotal,
		// backup (5)
		MetricBackupPhase, MetricBackupDurationSeconds, MetricBackupFailuresTotal,
		MetricOplogUploaderActive, MetricBackupIOThrottledBytesTotal,
		// audit/kms/federation (5)
		MetricAuditEventsTotal, MetricEncryptionEnabled, MetricKeyRotationTotal,
		MetricFederationRegionsSynced, MetricClusterGroupMembers,
		// insights (3, cycle 9 P2)
		MetricInsightsRecommendations, MetricInsightsAnalysisTotal, MetricInsightsSampledTotal,
	)
}

// DeleteMetricsFor — CR 삭제 시 cardinality 누적 방지.
func DeleteMetricsFor(namespace, name string) {
	MetricReconcileTotal.DeleteLabelValues(namespace, name)
	MetricReconcileErrors.DeletePartialMatch(prometheus.Labels{
		"namespace": namespace, "name": name,
	})
	for _, r := range []string{"success", "error"} {
		MetricReconcileLatency.DeleteLabelValues(namespace, name, r)
	}
}
