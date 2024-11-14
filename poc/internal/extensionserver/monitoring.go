package extensionserver

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var eventsReceived = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "extension_server_events_received_total",
		Help: "The total number of events extension server for.",
	}, []string{
		"event_type",
	})

func init() {
	metrics.Registry.MustRegister(eventsReceived)
}

func incReceivedEvents(eventType string) {
	eventsReceived.WithLabelValues(eventType).Inc()
}
