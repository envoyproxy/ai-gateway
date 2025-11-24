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

// apiKeyHandler implements [Handler] for api key authz.
type apiKeyHandler struct {
	apiKey string
}

func newAPIKeyHandler(auth *filterapi.APIKeyAuth) (filterapi.BackendAuthHandler, error) {
	return &apiKeyHandler{apiKey: strings.TrimSpace(auth.Key)}, nil
}

// Do implements [Handler.Do].
//
// Extracts the api key from the local file and set it as an authorization header.
func (a *apiKeyHandler) Do(_ context.Context, requestHeaders map[string]string, _ []byte) ([]internalapi.Header, error) {
	requestHeaders["Authorization"] = fmt.Sprintf("Bearer %s", a.apiKey)
	return []internalapi.Header{{"Authorization", fmt.Sprintf("Bearer %s", a.apiKey)}}, nil
}
