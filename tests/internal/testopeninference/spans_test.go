// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopeninference

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

// TestGetChatSpan records any missing spans vs cassettes.
func TestGetChatSpan(t *testing.T) {
	for _, cassette := range testopenai.ChatCassettes() {
		t.Run(cassette.String(), func(t *testing.T) {
			span, err := GetChatSpan(t.Context(), os.Stdout, cassette)
			require.NoError(t, err)

			require.NotEmpty(t, span.Name, "span name is empty for %s", cassette)
			require.NotEmpty(t, span.Attributes, "span has no attributes for %s", cassette)

			// Basic validation that this looks like an OpenInference span.
			require.Equal(t, "ChatCompletion", span.Name, "unexpected span name for %s", cassette)
		})
	}
}
