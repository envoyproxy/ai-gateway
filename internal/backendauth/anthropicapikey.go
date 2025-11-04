// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

type anthropicAPIKeyHandler struct {
	apiKey string
}

func newAnthropicAPIKeyHandler(auth *filterapi.AnthropicAPIKeyAuth) (Handler, error) {
	return &anthropicAPIKeyHandler{apiKey: strings.TrimSpace(auth.Key)}, nil
}

// Do sets the api-key header for Anthropic API requests.
// Anthropic uses "x-api-key" header instead of "Authorization: Bearer".
//
// https://docs.claude.com/en/api/overview#authentication
func (a *anthropicAPIKeyHandler) Do(_ context.Context, requestHeaders map[string]string, _ []byte) ([][2]string, error) {
	requestHeaders["x-api-key"] = a.apiKey
	return [][2]string{{"x-api-key", a.apiKey}}, nil
}
