// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package usageevent

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/metric"
)

const (
	metricUsageEventsConstructed    = "ai_gateway.usage_events.constructed"
	metricUsageEventsExported       = "ai_gateway.usage_events.exported"
	metricUsageEventsDropped        = "ai_gateway.usage_events.dropped"
	metricUsageEventsPublishLatency = "ai_gateway.usage_events.publish.duration"
)

// Publisher synchronously publishes UsageEvents through a Sink and records the
// constructed/exported/dropped/publish-latency metrics described in the proposal's
// Observability section. Publish never returns an error: a failed or timed-out publication is
// counted as dropped and logged, and request processing continues unchanged, per the
// proposal's stateless, no-retry design.
type Publisher struct {
	sink        Sink
	logger      *slog.Logger
	constructed metric.Int64Counter
	exported    metric.Int64Counter
	dropped     metric.Int64Counter
	latency     metric.Float64Histogram
}

// NewPublisher creates a Publisher that publishes through sink and records metrics on meter.
// sink must not be nil; callers should only construct a Publisher when the feature is enabled
// (i.e. a sink URL was configured) and otherwise skip publishing entirely.
func NewPublisher(sink Sink, meter metric.Meter, logger *slog.Logger) (*Publisher, error) {
	if sink == nil {
		return nil, errors.New("usageevent: sink must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	constructed, err := meter.Int64Counter(metricUsageEventsConstructed,
		metric.WithDescription("Total number of UsageEvents constructed."))
	if err != nil {
		return nil, err
	}
	exported, err := meter.Int64Counter(metricUsageEventsExported,
		metric.WithDescription("Number of UsageEvents successfully exported to the sink."))
	if err != nil {
		return nil, err
	}
	dropped, err := meter.Int64Counter(metricUsageEventsDropped,
		metric.WithDescription("Number of UsageEvents dropped due to publication failure or timeout."))
	if err != nil {
		return nil, err
	}
	latency, err := meter.Float64Histogram(metricUsageEventsPublishLatency,
		metric.WithDescription("Latency of UsageEvent publication to the sink."),
		metric.WithUnit("ms"))
	if err != nil {
		return nil, err
	}

	return &Publisher{
		sink:        sink,
		logger:      logger,
		constructed: constructed,
		exported:    exported,
		dropped:     dropped,
		latency:     latency,
	}, nil
}

// Publish constructs-and-forgets: it records the event as constructed, attempts a single
// synchronous publish through the sink, and records the outcome. It never blocks request
// processing beyond the sink's own timeout, and never returns an error to the caller.
func (p *Publisher) Publish(ctx context.Context, event UsageEvent) {
	p.constructed.Add(ctx, 1)

	start := time.Now()
	err := p.sink.Publish(ctx, event)
	p.latency.Record(ctx, float64(time.Since(start).Milliseconds()))

	if err != nil {
		p.dropped.Add(ctx, 1)
		p.logger.Warn("dropped usage event", "event_id", event.EventID, "request_id", event.RequestID, "error", err)
		return
	}
	p.exported.Add(ctx, 1)
}
