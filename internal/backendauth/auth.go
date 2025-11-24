// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"errors"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

// NewHandler returns a new implementation of [filterapi.BackendAuthHandler] based on the configuration.
func NewHandler(ctx context.Context, config *filterapi.BackendAuth) (filterapi.BackendAuthHandler, error) {
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
	default:
		return nil, errors.New("no backend auth handler found")
	}
}
