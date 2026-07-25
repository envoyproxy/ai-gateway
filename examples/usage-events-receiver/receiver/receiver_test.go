// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package receiver implements a reference UsageEvent sink: an HTTP server backed by an
// embedded NATS JetStream KV store, meant to run as a sidecar next to Envoy AI Gateway.
package receiver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

// newTestServer starts a Server backed by an embedded NATS instance rooted at dir and
// returns it together with an httptest server exposing its HTTP handler. Callers must
// call the returned shutdown func to release resources.
func newTestServer(t *testing.T, dir string) (*Server, *httptest.Server, func()) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	srv, err := New(ctx, Config{StoreDir: dir})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	shutdown := func() {
		ts.Close()
		if err := srv.Close(); err != nil {
			t.Errorf("srv.Close() failed: %v", err)
		}
	}
	return srv, ts, shutdown
}

func postEvent(t *testing.T, baseURL string, event map[string]any) *http.Response {
	t.Helper()
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	resp, err := http.Post(baseURL+"/v1/usage-events", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/usage-events: %v", err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	return out
}

func TestHealthz(t *testing.T) {
	_, ts, shutdown := newTestServer(t, t.TempDir())
	defer shutdown()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestReadyz(t *testing.T) {
	_, ts, shutdown := newTestServer(t, t.TempDir())
	defer shutdown()

	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 once JetStream is ready, got %d", resp.StatusCode)
	}
}

func TestPublishUsageEvent_Success(t *testing.T) {
	_, ts, shutdown := newTestServer(t, t.TempDir())
	defer shutdown()

	event := map[string]any{
		"schema_version": "v1",
		"event_id":       "req-abc123|llmroute|openai-primary",
		"status":         "succeeded",
		"input_tokens":   120,
		"output_tokens":  480,
	}

	resp := postEvent(t, ts.URL, event)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	got := decodeJSON(t, resp)
	if got["status"] != "accepted" {
		t.Errorf("expected status=accepted, got %v", got["status"])
	}
	if got["event_id"] != event["event_id"] {
		t.Errorf("expected event_id=%v, got %v", event["event_id"], got["event_id"])
	}

	// GET must return exactly what was stored, durably, including the pipe-delimited event_id.
	getResp, err := http.Get(ts.URL + "/v1/usage-events/" + url.PathEscape(event["event_id"].(string)))
	if err != nil {
		t.Fatalf("GET usage event: %v", err)
	}
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on GET, got %d", getResp.StatusCode)
	}
	stored := decodeJSON(t, getResp)
	payload, ok := stored["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected payload object, got %v", stored)
	}
	if payload["event_id"] != event["event_id"] {
		t.Errorf("stored payload event_id mismatch: got %v", payload["event_id"])
	}
	if payload["output_tokens"].(float64) != 480 {
		t.Errorf("stored payload output_tokens mismatch: got %v", payload["output_tokens"])
	}
}

func TestPublishUsageEvent_Deduplication(t *testing.T) {
	_, ts, shutdown := newTestServer(t, t.TempDir())
	defer shutdown()

	event := map[string]any{"event_id": "dup-event-1", "output_tokens": 10}

	first := postEvent(t, ts.URL, event)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first publish: expected 201, got %d", first.StatusCode)
	}
	decodeJSON(t, first)

	// Republish the same event_id with a different payload; the original must win and the
	// gateway-facing contract is idempotent acknowledgement, not last-write-wins.
	second := postEvent(t, ts.URL, map[string]any{"event_id": "dup-event-1", "output_tokens": 999})
	if second.StatusCode != http.StatusOK {
		t.Fatalf("duplicate publish: expected 200, got %d", second.StatusCode)
	}
	got := decodeJSON(t, second)
	if got["status"] != "duplicate" {
		t.Errorf("expected status=duplicate, got %v", got["status"])
	}

	listResp, err := http.Get(ts.URL + "/v1/usage-events")
	if err != nil {
		t.Fatalf("GET /v1/usage-events: %v", err)
	}
	list := decodeJSON(t, listResp)
	if int(list["count"].(float64)) != 1 {
		t.Fatalf("expected exactly 1 stored event after duplicate publish, got %v", list["count"])
	}

	getResp, err := http.Get(ts.URL + "/v1/usage-events/dup-event-1")
	if err != nil {
		t.Fatalf("GET stored event: %v", err)
	}
	stored := decodeJSON(t, getResp)
	payload := stored["payload"].(map[string]any)
	if payload["output_tokens"].(float64) != 10 {
		t.Errorf("dedup must preserve the first write; got output_tokens=%v", payload["output_tokens"])
	}
}

func TestPublishUsageEvent_InvalidJSON(t *testing.T) {
	_, ts, shutdown := newTestServer(t, t.TempDir())
	defer shutdown()

	resp, err := http.Post(ts.URL+"/v1/usage-events", "application/json", bytes.NewReader([]byte("{not json")))
	if err != nil {
		t.Fatalf("POST invalid json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestPublishUsageEvent_MissingEventID(t *testing.T) {
	_, ts, shutdown := newTestServer(t, t.TempDir())
	defer shutdown()

	resp := postEvent(t, ts.URL, map[string]any{"output_tokens": 5})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing event_id, got %d", resp.StatusCode)
	}
}

func TestGetUsageEvent_NotFound(t *testing.T) {
	_, ts, shutdown := newTestServer(t, t.TempDir())
	defer shutdown()

	resp, err := http.Get(ts.URL + "/v1/usage-events/does-not-exist")
	if err != nil {
		t.Fatalf("GET missing event: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestListUsageEvents(t *testing.T) {
	_, ts, shutdown := newTestServer(t, t.TempDir())
	defer shutdown()

	for i := 0; i < 3; i++ {
		resp := postEvent(t, ts.URL, map[string]any{"event_id": fmt.Sprintf("evt-%d", i)})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("publish evt-%d: expected 201, got %d", i, resp.StatusCode)
		}
		resp.Body.Close()
	}

	resp, err := http.Get(ts.URL + "/v1/usage-events")
	if err != nil {
		t.Fatalf("GET /v1/usage-events: %v", err)
	}
	list := decodeJSON(t, resp)
	if int(list["count"].(float64)) != 3 {
		t.Fatalf("expected 3 events, got %v", list["count"])
	}
}

// TestPersistenceAcrossRestart proves the receiver is durable: events published before an
// embedded NATS/JetStream restart (simulating a sidecar crash-restart) must survive, since
// this is the entire reason the proposal's colocated-receiver topology uses JetStream instead
// of an in-memory map.
func TestPersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	srv1, ts1, err := startForRestartTest(t, dir)
	if err != nil {
		t.Fatalf("start srv1: %v", err)
	}
	resp := postEvent(t, ts1.URL, map[string]any{"event_id": "durable-event", "output_tokens": 42})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	ts1.Close()
	if err := srv1.Close(); err != nil {
		t.Fatalf("close srv1: %v", err)
	}

	srv2, ts2, err := startForRestartTest(t, dir)
	if err != nil {
		t.Fatalf("start srv2: %v", err)
	}
	defer func() {
		ts2.Close()
		_ = srv2.Close()
	}()

	getResp, err := http.Get(ts2.URL + "/v1/usage-events/durable-event")
	if err != nil {
		t.Fatalf("GET after restart: %v", err)
	}
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected event to survive restart, got status %d", getResp.StatusCode)
	}
	stored := decodeJSON(t, getResp)
	payload := stored["payload"].(map[string]any)
	if payload["output_tokens"].(float64) != 42 {
		t.Errorf("restored payload mismatch: got %v", payload["output_tokens"])
	}
}

func startForRestartTest(t *testing.T, dir string) (*Server, *httptest.Server, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv, err := New(ctx, Config{StoreDir: filepath.Join(dir)})
	if err != nil {
		return nil, nil, err
	}
	return srv, httptest.NewServer(srv.Handler()), nil
}
