// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package vcr

import (
	_ "embed"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/tests/internal/testenvironment"
)

// otelTestEnvironment holds all the services needed for OTEL tests.
type otelTestEnvironment struct {
	*testenvironment.TestEnvironment
	collector *testotel.OTLPCollector
}

// setupOtelTestEnvironment starts all required services and returns ports and a closer.
func setupOtelTestEnvironment(t *testing.T, extraExtProcEnv ...string) *otelTestEnvironment {
	// Start OTLP collector.
	collector := testotel.StartOTLPCollector()
	t.Cleanup(collector.Close)

	extprocEnv := append(collector.Env(), extraExtProcEnv...)

	testEnv := startTestEnvironment(t, extprocBin, extprocConfig, extprocEnv, envoyConfig)

	return &otelTestEnvironment{
		TestEnvironment: testEnv,
		collector:       collector,
	}
}

// failIfBadGateway because is likely a sign of a broken ExtProc or Envoy.
func failIfBadGateway(t *testing.T, resp *http.Response) bool {
	if resp.StatusCode == http.StatusBadGateway {
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		t.Fatalf("received 502 response with body: %s", string(body))
		return true
	}
	return false
}
