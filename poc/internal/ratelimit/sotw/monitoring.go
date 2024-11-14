package sotw

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	snapshotVersion = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "ratelimit_snapshot_version",
			Help: "Number of process operations",
		},
	)
	snapshotFailures = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ratelimit_snapshot_failures_total",
			Help: "Number of process snapshot failures",
		},
	)
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(snapshotVersion)
}

func recordSnapshotVersion(version int64) {
	snapshotVersion.Set(float64(version))
}

func increaseSnapshotFailures() {
	snapshotFailures.Inc()
}
