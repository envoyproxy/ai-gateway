// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

// TestMetricsHeaderNames tests that request headers can be included as labels in metrics.
func TestMetricsHeaderNames(t *testing.T) {
	requireBinaries(t)
	accessLogPath := t.TempDir() + "/access.log"
	requireRunEnvoy(t, accessLogPath)
	configPath := t.TempDir() + "/extproc-config.yaml"
	requireTestUpstream(t)

	requireWriteFilterConfig(t, configPath, &filterapi.Config{
		MetadataNamespace:  "ai_gateway_llm_ns",
		Schema:             openAISchema,
		ModelNameHeaderKey: "x-model-name",
		Backends: []filterapi.Backend{
			testUpstreamOpenAIBackend,
		},
	})

	// Start extproc with header names configured for metrics.
	requireExtProcWithHeaderNames(t, os.Stdout, extProcExecutablePath(), configPath, "x-user-id,x-team-id")

	// Make a request with the headers that should be included in metrics.
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodPost, "http://localhost:1062/v1/chat/completions",
		strings.NewReader(`{"model":"something","messages":[{"role":"user","content":"Hello"}]}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-user-id", "user123")
	req.Header.Set("x-team-id", "team456")
	req.Header.Set("x-model-name", "something")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	// Wait a moment for metrics to be recorded.
	time.Sleep(2 * time.Second)

	// Check metrics endpoint to verify headers are included as labels.
	t.Run("metrics_with_header_labels", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://localhost:1064/metrics", nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		metricsOutput := string(body)

		// Verify that header labels are present in the metrics.
		require.Containsf(t, metricsOutput,
			`header_x_user_id="user123"`,
			"expected header_x_user_id label in metrics:\n%s", metricsOutput,
		)
		require.Containsf(t, metricsOutput,
			`header_x_team_id="team456"`,
			"expected header_x_team_id label in metrics:\n%s", metricsOutput,
		)

		// Verify that the headers appear in the chat completion metrics.
		require.Containsf(t, metricsOutput,
			`gen_ai_operation_name="chat"`,
			"expected chat completion metrics:\n%s", metricsOutput,
		)
	})
}

// requireExtProcWithHeaderNames starts the external processor with metrics header names configured.
func requireExtProcWithHeaderNames(t *testing.T, stdout io.Writer, executable, configPath, headerNames string) {
	cmd := exec.CommandContext(t.Context(), executable)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	cmd.Args = append(cmd.Args,
		"-configPath", configPath,
		"-logLevel", "warn",
		"-metricsHeaderNames", headerNames,
	)
	cmd.Env = os.Environ()
	require.NoError(t, cmd.Start())
}
