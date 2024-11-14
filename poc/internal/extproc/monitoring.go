package extproc

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	processTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "process_total",
			Help: "Number of process operations",
		},
		[]string{"method"},
	)

	processFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "process_failures_total",
			Help: "Number of process operations",
		},
		[]string{"reason"},
	)
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(processTotal)
}

func increaseProcessTotal(method string) {
	processTotal.WithLabelValues(method).Inc()
}

func increaseProcessFailures(reason string) {
	processFailures.WithLabelValues(reason).Inc()
}
