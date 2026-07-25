// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package usageevent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
)

// TestUsageEventPublisher_SuccessAndAcknowledge is a zero-external-dependency integration test:
// it mocks the acknowledged HTTP sink destination with httptest.NewServer and exercises it
// through the actual production HTTPSink/Publisher used by the ExtProc processor (not a
// reimplementation), verifying payload serialization end-to-end, the constructed/exported
// metrics pathway, and that a 202 Accepted response is treated as a successful acknowledgement.
func TestUsageEventPublisher_SuccessAndAcknowledge(t *testing.T) {
	receivedEvents := make(chan UsageEvent, 1)

	mockSink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST request, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read payload: %v", err)
		}
		defer r.Body.Close()

		var event UsageEvent
		if err := json.Unmarshal(body, &event); err != nil {
			t.Errorf("payload unmarshal failed: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		receivedEvents <- event
		w.WriteHeader(http.StatusAccepted) // 202 Accepted: acknowledged but processed asynchronously downstream.
		_, _ = w.Write([]byte(`{"status":"acknowledged"}`))
	}))
	defer mockSink.Close()

	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	// This is the same HTTPSink/Publisher wiring cmd/extproc/mainlib builds from
	// --usageEventsHTTPURL and passes into the ExtProc processor.
	sink := NewHTTPSink(mockSink.URL, time.Second)
	publisher, err := NewPublisher(sink, meter, nil)
	require.NoError(t, err)

	sent := UsageEvent{
		SchemaVersion:  SchemaVersion,
		EventID:        "evt_01",
		RequestID:      "req_123",
		Status:         StatusSucceeded,
		StatusCode:     http.StatusOK,
		Provider:       "openai",
		Backend:        "openai-primary",
		ModelRequested: "gpt-4o",
		ModelResponse:  "gpt-4o",
		InputTokens:    15,
		OutputTokens:   25,
	}
	publisher.Publish(context.Background(), sent)

	select {
	case event := <-receivedEvents:
		assert.Equal(t, sent.EventID, event.EventID)
		assert.Equal(t, sent.RequestID, event.RequestID)
		assert.Equal(t, sent.InputTokens, event.InputTokens)
		assert.Equal(t, sent.OutputTokens, event.OutputTokens)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for the publisher to hit the mock HTTP sink")
	}

	assert.EqualValues(t, 1, collectCounterValue(t, mr, metricUsageEventsConstructed))
	assert.EqualValues(t, 1, collectCounterValue(t, mr, metricUsageEventsExported))
	assert.EqualValues(t, 0, collectCounterValue(t, mr, metricUsageEventsDropped))
	assert.EqualValues(t, 1, collectHistogramCount(t, mr, metricUsageEventsPublishLatency))
}

// TestUsageEventPublisher_DropOnSinkFailure verifies the drop rule: a non-2xx response from the
// sink must be counted as dropped, not exported, and must never propagate an error back to the
// caller (request processing must continue unaffected).
func TestUsageEventPublisher_DropOnSinkFailure(t *testing.T) {
	mockSink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockSink.Close()

	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	publisher, err := NewPublisher(NewHTTPSink(mockSink.URL, time.Second), meter, nil)
	require.NoError(t, err)

	publisher.Publish(context.Background(), testEvent())

	assert.EqualValues(t, 1, collectCounterValue(t, mr, metricUsageEventsConstructed))
	assert.EqualValues(t, 0, collectCounterValue(t, mr, metricUsageEventsExported))
	assert.EqualValues(t, 1, collectCounterValue(t, mr, metricUsageEventsDropped))
}

// TestUsageEventPublisher_DropOnTimeout verifies the drop rule when the sink is unresponsive:
// publication must give up at the configured timeout and count the event as dropped rather than
// blocking request processing indefinitely.
func TestUsageEventPublisher_DropOnTimeout(t *testing.T) {
	mockSink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Outlast the sink's 20ms timeout but still return well within httptest.Server's own
		// close grace period, so t.Cleanup/Close never has to force-close a still-blocked handler.
		select {
		case <-time.After(200 * time.Millisecond):
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mockSink.Close()

	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	publisher, err := NewPublisher(NewHTTPSink(mockSink.URL, 20*time.Millisecond), meter, nil)
	require.NoError(t, err)

	publisher.Publish(context.Background(), testEvent())

	assert.EqualValues(t, 1, collectCounterValue(t, mr, metricUsageEventsConstructed))
	assert.EqualValues(t, 0, collectCounterValue(t, mr, metricUsageEventsExported))
	assert.EqualValues(t, 1, collectCounterValue(t, mr, metricUsageEventsDropped))
}
