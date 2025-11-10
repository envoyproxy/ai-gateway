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

type azureAPIKeyHandler struct {
	apiKey string
}

func newAzureAPIKeyHandler(auth *filterapi.AzureAPIKeyAuth) (Handler, error) {
	if auth.Key == "" {
		return nil, fmt.Errorf("azure API key is required")
	}
	return &azureAPIKeyHandler{apiKey: strings.TrimSpace(auth.Key)}, nil
}

// Do sets the api-key header for Azure OpenAI authentication.
// Azure OpenAI uses "api-key" header instead of "Authorization: Bearer".
func (a *azureAPIKeyHandler) Do(_ context.Context, requestHeaders map[string]string, _ []byte) ([]internalapi.Header, error) {
	requestHeaders["api-key"] = a.apiKey
	return []internalapi.Header{{"api-key", a.apiKey}}, nil
}
