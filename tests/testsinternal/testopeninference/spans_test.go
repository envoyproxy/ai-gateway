// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopeninference

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/testsinternal/testopenai"
)

// TestGetAllSpans records any missing spans vs cassettes.
func TestGetAllSpans(t *testing.T) {
	tests := []struct {
		name      string
		cassettes []testopenai.Cassette
	}{
		{"ChatCompletion", testopenai.ChatCassettes()},
		{"Completion", testopenai.CompletionCassettes()},
		{"CreateEmbeddings", testopenai.EmbeddingsCassettes()},
		{"ImagesResponse", testopenai.ImageCassettes()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, cassette := range tc.cassettes {
				t.Run(cassette.String(), func(t *testing.T) {
					span, err := GetSpan(t.Context(), os.Stdout, cassette)
					require.NoError(t, err)

					require.NotEmpty(t, span.Name, "span name is empty for %s", cassette)
					require.NotEmpty(t, span.Attributes, "span has no attributes for %s", cassette)

					// Basic validation that this looks like an OpenInference span.
					require.Equal(t, tc.name, span.Name, "unexpected span name for %s", cassette)
				})
			}
		})
	}
}
