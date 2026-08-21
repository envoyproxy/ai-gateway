// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

type googleAPIKeyHandler struct {
	apiKey string
}

func newGoogleAPIKeyHandler(auth *filterapi.GoogleAPIKeyAuth) (filterapi.BackendAuthHandler, error) {
	return &googleAPIKeyHandler{apiKey: strings.TrimSpace(auth.Key)}, nil
}

// Do sets the x-goog-api-key header. Google APIs take an API key in "x-goog-api-key" rather than
// "Authorization: Bearer"; the Gemini Developer API on generativelanguage.googleapis.com is one such API.
//
// https://ai.google.dev/api/rest
func (g *googleAPIKeyHandler) Do(_ context.Context, requestHeaders map[string]string, _ []byte) ([]internalapi.Header, error) {
	requestHeaders["x-goog-api-key"] = g.apiKey
	return []internalapi.Header{{"x-goog-api-key", g.apiKey}}, nil
}
