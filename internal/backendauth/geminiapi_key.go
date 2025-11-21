// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"fmt"
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// geminiAPIKeyHandler implements [Handler] for Gemini API key authentication.
// Unlike other API key handlers that use Authorization headers, Gemini API requires
// the API key to be passed as a query parameter (?key=<api-key>).
type geminiAPIKeyHandler struct {
	apiKey string
}

func newGeminiAPIKeyHandler(auth *filterapi.GeminiAPIKeyAuth) (Handler, error) {
	apiKey := strings.TrimSpace(auth.Key)
	if apiKey == "" {
		return nil, fmt.Errorf("Gemini API key cannot be empty")
	}
	return &geminiAPIKeyHandler{apiKey: apiKey}, nil
}

// Do implements [Handler.Do].
//
// Appends the API key as a query parameter to the :path header.
// Gemini API authentication format: ?key=<api-key>
// Reference: https://ai.google.dev/gemini-api/docs/api-key
func (g *geminiAPIKeyHandler) Do(_ context.Context, requestHeaders map[string]string, _ []byte) ([]internalapi.Header, error) {
	path := requestHeaders[":path"]
	if path == "" {
		return nil, fmt.Errorf("missing ':path' header in the request")
	}

	// Append the key parameter to the existing path
	// The path may already contain query parameters (e.g., ?alt=sse)
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}

	newPath := fmt.Sprintf("%s%skey=%s", path, separator, g.apiKey)
	requestHeaders[":path"] = newPath

	return []internalapi.Header{{":path", newPath}}, nil
}
