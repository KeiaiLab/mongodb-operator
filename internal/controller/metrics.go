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
	MetricReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: metricSubsystem,
			Name:      "reconcile_total",
			Help:      "Total Reconcile invocations",
		},
		labelNamespaceName,
	)

	MetricReconcileLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: metricSubsystem,
			Name:      "reconcile_duration_seconds",
			Help:      "Reconcile function wall-clock duration in seconds",
			Buckets: []float64{
				0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0,
			},
		},
		[]string{"namespace", "name", "result"},
	)

	MetricReconcileErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: metricSubsystem,
			Name:      "reconcile_errors_total",
			Help:      "Total Reconcile component failures",
		},
		[]string{"namespace", "name", "component"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		MetricReconcileTotal,
		MetricReconcileLatency,
		MetricReconcileErrors,
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
