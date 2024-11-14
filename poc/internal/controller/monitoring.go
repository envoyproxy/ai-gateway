package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var reconcileFailures = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "reconcile_failures_total",
		Help: "Number of failed reconcile operations",
	},
	[]string{"gvk"},
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(reconcileFailures)
}

func incrementReconcileFailures(gvk string) {
	reconcileFailures.WithLabelValues(gvk).Inc()
}
