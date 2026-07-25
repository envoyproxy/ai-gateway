// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package usageevent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testEvent() UsageEvent {
	return UsageEvent{
		SchemaVersion:  SchemaVersion,
		EventID:        "req-abc123|llmroute|openai-primary",
		EmittedAt:      1753961025123,
		RequestID:      "req-abc123",
		Status:         StatusSucceeded,
		StatusCode:     http.StatusOK,
		Provider:       "openai",
		Backend:        "openai-primary",
		ModelRequested: "o4-mini",
		ModelResponse:  "o4-mini",
		InputTokens:    120,
		OutputTokens:   480,
	}
}

func TestHTTPSink_Publish_Success(t *testing.T) {
	var gotBody UsageEvent
	var gotContentType string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("server: decode request body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	sink := NewHTTPSink(ts.URL, time.Second)
	event := testEvent()
	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() failed: %v", err)
	}

	if gotContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", gotContentType)
	}
	if gotBody.EventID != event.EventID {
		t.Errorf("expected event_id %q, got %q", event.EventID, gotBody.EventID)
	}
	if gotBody.OutputTokens != event.OutputTokens {
		t.Errorf("expected output_tokens %d, got %d", event.OutputTokens, gotBody.OutputTokens)
	}
}

func TestHTTPSink_Publish_AcceptsAny2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // duplicate/idempotent ack, as the reference receiver returns
	}))
	defer ts.Close()

	sink := NewHTTPSink(ts.URL, time.Second)
	if err := sink.Publish(context.Background(), testEvent()); err != nil {
		t.Fatalf("Publish() failed: %v", err)
	}
}

func TestHTTPSink_Publish_NonSuccessStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	sink := NewHTTPSink(ts.URL, time.Second)
	err := sink.Publish(context.Background(), testEvent())
	if err == nil {
		t.Fatal("expected an error for a non-2xx response, got nil")
	}
}

func TestHTTPSink_Publish_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	sink := NewHTTPSink(ts.URL, 10*time.Millisecond)
	err := sink.Publish(context.Background(), testEvent())
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
}

func TestHTTPSink_Publish_ConnectionError(t *testing.T) {
	sink := NewHTTPSink("http://127.0.0.1:1", time.Second)
	err := sink.Publish(context.Background(), testEvent())
	if err == nil {
		t.Fatal("expected a connection error, got nil")
	}
}

func TestHTTPSink_Publish_ContextCanceledBeforeCall(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sink := NewHTTPSink(ts.URL, time.Second)
	err := sink.Publish(ctx, testEvent())
	if err == nil {
		t.Fatal("expected an error for an already-canceled context, got nil")
	}
}

func TestHTTPSink_Publish_RequestBodyMatchesSchema(t *testing.T) {
	var raw map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("server: decode: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	sink := NewHTTPSink(ts.URL, time.Second)
	if err := sink.Publish(context.Background(), testEvent()); err != nil {
		t.Fatalf("Publish() failed: %v", err)
	}

	for _, field := range []string{
		"schema_version", "event_id", "emitted_at", "request_id", "status", "status_code",
		"provider", "backend", "model_requested", "model_response",
		"input_tokens", "output_tokens", "cached_input_tokens", "cache_write_input_tokens", "reasoning_tokens",
	} {
		if _, ok := raw[field]; !ok {
			t.Errorf("expected field %q in the published JSON body, got keys: %v", field, keysOf(raw))
		}
	}
	if _, ok := raw["attributes"]; ok {
		t.Errorf("expected attributes to be omitted when empty, got %v", raw["attributes"])
	}
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestNewHTTPSink_TrimsTrailingSlash(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	sink := NewHTTPSink(ts.URL+"/", time.Second)
	if err := sink.Publish(context.Background(), testEvent()); err != nil {
		t.Fatalf("Publish() failed: %v", err)
	}
	if strings.HasSuffix(gotPath, "//") {
		t.Errorf("expected no double slash in request path, got %q", gotPath)
	}
}
