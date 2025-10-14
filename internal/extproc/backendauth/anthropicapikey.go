// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

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
func (a *anthropicAPIKeyHandler) Do(_ context.Context, requestHeaders map[string]string, headerMut *extprocv3.HeaderMutation, _ *extprocv3.BodyMutation) error {
	requestHeaders["x-api-key"] = a.apiKey
	headerMut.SetHeaders = append(headerMut.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:      "x-api-key",
			RawValue: []byte(a.apiKey),
		},
	})
	return nil
}
