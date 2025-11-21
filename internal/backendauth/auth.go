// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"errors"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// Handler is the interface that deals with the backend auth for a specific backend.
//
// TODO: maybe this can be just "post-transformation" handler, as it is not really only about auth.
type Handler interface {
	// Do performs the backend auth, and make changes to the request headers passed in as `requestHeaders`.
	// It also returns a list of headers that were added or modified as a slice of key-value pairs.
	Do(ctx context.Context, requestHeaders map[string]string, mutatedBody []byte) ([]internalapi.Header, error)
}

// NewHandler returns a new implementation of [Handler] based on the configuration.
func NewHandler(ctx context.Context, config *filterapi.BackendAuth) (Handler, error) {
	switch {
	case config.AWSAuth != nil:
		return newAWSHandler(ctx, config.AWSAuth)
	case config.APIKey != nil:
		return newAPIKeyHandler(config.APIKey)
	case config.AzureAPIKey != nil:
		return newAzureAPIKeyHandler(config.AzureAPIKey)
	case config.AzureAuth != nil:
		return newAzureHandler(config.AzureAuth)
	case config.GCPAuth != nil:
		return newGCPHandler(config.GCPAuth)
	case config.AnthropicAPIKey != nil:
		return newAnthropicAPIKeyHandler(config.AnthropicAPIKey)
	case config.GeminiAPIKey != nil:
		return newGeminiAPIKeyHandler(config.GeminiAPIKey)
	default:
		return nil, errors.New("no backend auth handler found")
	}
}
