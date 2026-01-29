// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internaltesting

import "testing"

// ClearTestEnv clears env vars aigw reads to avoid inheriting from user's shell.
func ClearTestEnv(t testing.TB) {
	t.Helper()
	for _, env := range []string{
		"OPENAI_API_KEY",
		"OPENAI_BASE_URL",
		"AZURE_OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_BASE_URL",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_HEADERS",
		"OTEL_METRICS_EXPORTER",
		"OTEL_RESOURCE_ATTRIBUTES",
		"OTEL_SERVICE_NAME",
	} {
		t.Setenv(env, "")
	}
}
