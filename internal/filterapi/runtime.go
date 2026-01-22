// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package filterapi

import (
	"context"
	"fmt"

	"github.com/google/cel-go/cel"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

// BackendAuthHandler is the interface that deals with the backend auth for a specific backend.
type BackendAuthHandler interface {
	// Do performs the backend auth, and make changes to the request headers passed in as `requestHeaders`.
	// It also returns a list of headers that were added or modified as a slice of key-value pairs.
	Do(ctx context.Context, requestHeaders map[string]string, mutatedBody []byte) ([]internalapi.Header, error)
}

// NewBackendAuthHandlerFunc is a function type that creates a new BackendAuthHandler for a given BackendAuth configuration.
type NewBackendAuthHandlerFunc func(ctx context.Context, auth *BackendAuth) (BackendAuthHandler, error)

// RuntimeConfig is the runtime filter configuration that is derived from the filterapi.Config.
type RuntimeConfig struct {
	// UUID is the unique identifier of the filter configuration, inherited from filterapi.Config.
	UUID string
	// RequestCosts is the list of request costs.
	RequestCosts []RuntimeRequestCost
	// DeclaredModels is the list of declared models.
	DeclaredModels []Model
	// Backends is the map of backends by name.
	Backends map[string]*RuntimeBackend
	// ResponseCache is the response cache configuration.
	ResponseCache *ResponseCacheConfig
}

// RuntimeBackend is a filter backend with its auth handler that is derived from the filterapi.Backend configuration.
type RuntimeBackend struct {
	// Backend is the filter backend configuration.
	Backend *Backend
	// Handler is the backend auth handler.
	Handler BackendAuthHandler
}

// RuntimeRequestCost is the configuration for the request cost, optionally with a CEL program.
// This is derived from the filterapi.LLMRequestCost configuration, and includes the compiled CEL program if provided.
type RuntimeRequestCost struct {
	*LLMRequestCost
	CELProg cel.Program
}

// NewRuntimeConfig creates a new runtime filter configuration from the given filterapi.Config and a function to create backend auth handlers.
func NewRuntimeConfig(ctx context.Context, config *Config, fn NewBackendAuthHandlerFunc) (*RuntimeConfig, error) {
	backends := make(map[string]*RuntimeBackend, len(config.Backends))
	for _, backend := range config.Backends {
		b := backend
		var h BackendAuthHandler
		if b.Auth != nil {
			var err error
			h, err = fn(ctx, b.Auth)
			if err != nil {
				return nil, fmt.Errorf("cannot create backend auth handler: %w", err)
			}
		}
		backends[b.Name] = &RuntimeBackend{Backend: &b, Handler: h}
	}

	costs := make([]RuntimeRequestCost, 0, len(config.LLMRequestCosts))
	for i := range config.LLMRequestCosts {
		c := &config.LLMRequestCosts[i]
		var prog cel.Program
		if c.CEL != "" {
			var err error
			prog, err = llmcostcel.NewProgram(c.CEL)
			if err != nil {
				return nil, fmt.Errorf("cannot create CEL program for cost: %w", err)
			}
		}
		costs = append(costs, RuntimeRequestCost{LLMRequestCost: c, CELProg: prog})
	}

	return &RuntimeConfig{
		UUID:           config.UUID,
		Backends:       backends,
		RequestCosts:   costs,
		DeclaredModels: config.Models,
		ResponseCache:  config.ResponseCache,
	}, nil
}
