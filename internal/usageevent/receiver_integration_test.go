// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package usageevent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
)

// TestHTTPSink_AgainstReferenceReceiver is the end-to-end proof that the gateway's HTTPSink
// (the UsageEventSink implementation used in production) actually works against
// examples/usage-events-receiver, the reference sidecar backed by an embedded NATS JetStream
// server. It builds and runs the real receiver binary as a separate process (mirroring the
// colocated-sidecar deployment topology from the proposal), publishes through the same
// Publisher/HTTPSink the ExtProc processor uses, and asserts against the receiver's HTTP API
// that events were durably stored and deduplicated on event_id.
func TestHTTPSink_AgainstReferenceReceiver(t *testing.T) {
	receiverDir, err := filepath.Abs(filepath.Join("..", "..", "examples", "usage-events-receiver"))
	require.NoError(t, err)
	if _, err := os.Stat(receiverDir); err != nil {
		t.Skipf("reference receiver module not found at %s: %v", receiverDir, err)
	}

	workDir := t.TempDir()
	binPath := filepath.Join(workDir, "usage-events-receiver")
	storeDir := filepath.Join(workDir, "store")
	require.NoError(t, os.MkdirAll(storeDir, 0o755))

	buildCtx, buildCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer buildCancel()
	buildCmd := exec.CommandContext(buildCtx, "go", "build", "-o", binPath, ".")
	buildCmd.Dir = receiverDir
	out, err := buildCmd.CombinedOutput()
	require.NoErrorf(t, err, "building reference receiver failed: %s", out)

	addr := "127.0.0.1:18099"
	runCmd := exec.Command(binPath, "-addr", addr, "-store-dir", storeDir)
	var stdout, stderr bytes.Buffer
	runCmd.Stdout = &stdout
	runCmd.Stderr = &stderr
	require.NoError(t, runCmd.Start())
	t.Cleanup(func() {
		if runCmd.Process != nil {
			_ = runCmd.Process.Signal(syscall.SIGTERM)
			_ = runCmd.Wait()
		}
		if t.Failed() {
			t.Logf("receiver stdout: %s", stdout.String())
			t.Logf("receiver stderr: %s", stderr.String())
		}
	})

	baseURL := "http://" + addr
	waitForHealthy(t, baseURL)

	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	sink := NewHTTPSink(baseURL+"/v1/usage-events", 2*time.Second)
	publisher, err := NewPublisher(sink, meter, nil)
	require.NoError(t, err)

	first := UsageEvent{
		SchemaVersion: SchemaVersion,
		EventID:       "req-e2e-1|llmroute|openai-primary",
		RequestID:     "req-e2e-1",
		Status:        StatusSucceeded,
		StatusCode:    http.StatusOK,
		Provider:      "openai",
		Backend:       "openai-primary",
		InputTokens:   100,
		OutputTokens:  200,
	}
	second := UsageEvent{
		SchemaVersion: SchemaVersion,
		EventID:       "req-e2e-2|llmroute|anthropic-primary",
		RequestID:     "req-e2e-2",
		Status:        StatusSucceeded,
		StatusCode:    http.StatusOK,
		Provider:      "anthropic",
		Backend:       "anthropic-primary",
		InputTokens:   50,
		OutputTokens:  75,
	}
	duplicateOfFirst := first
	duplicateOfFirst.OutputTokens = 999 // must not overwrite the durably stored original.

	ctx := context.Background()
	publisher.Publish(ctx, first)
	publisher.Publish(ctx, second)
	publisher.Publish(ctx, duplicateOfFirst)

	assert3xConstructedAndExported(t, mr)

	// Verify against the receiver's own HTTP API that both distinct events were durably
	// persisted and that the duplicate publish did not create a second entry or overwrite
	// the original — i.e. the gateway's sink and the sidecar's dedup logic actually agree.
	listResp, err := http.Get(baseURL + "/v1/usage-events")
	require.NoError(t, err)
	defer listResp.Body.Close()
	var list struct {
		Count int `json:"count"`
	}
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))
	require.Equal(t, 2, list.Count, "expected exactly 2 distinct events after the duplicate publish")

	getResp, err := http.Get(baseURL + "/v1/usage-events/" + url.PathEscape(first.EventID))
	require.NoError(t, err)
	defer getResp.Body.Close()
	require.Equal(t, http.StatusOK, getResp.StatusCode)
	var stored struct {
		Payload UsageEvent `json:"payload"`
	}
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&stored))
	require.Equal(t, 200, stored.Payload.OutputTokens, "dedup must preserve the first write, not the duplicate")
}

func waitForHealthy(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("receiver at %s did not become healthy in time", baseURL)
}

func assert3xConstructedAndExported(t *testing.T, mr *metric.ManualReader) {
	t.Helper()
	require.EqualValues(t, 3, collectCounterValue(t, mr, metricUsageEventsConstructed))
	require.EqualValues(t, 3, collectCounterValue(t, mr, metricUsageEventsExported))
	require.EqualValues(t, 0, collectCounterValue(t, mr, metricUsageEventsDropped))
}
