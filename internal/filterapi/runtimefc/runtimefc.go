// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package runtimefc is the API for the construct of runtime filter configurations parsed from filterapi.Config.
//
// Decoupled from filterapi to avoid circular dependencies.
package runtimefc

import (
	"context"
	"fmt"

	"github.com/google/cel-go/cel"

	"github.com/envoyproxy/ai-gateway/internal/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

// Config is the runtime filter configuration.
type Config struct {
	// UUID is the unique identifier of the filter configuration, inherited from filterapi.Config.
	UUID string
	// RequestCosts is the list of request costs.
	RequestCosts []RequestCost
	// DeclaredModels is the list of declared models.
	DeclaredModels []filterapi.Model
	// Backends is the map of backends by name.
	Backends map[string]*Backend
}

// Backend is a filter backend with its auth handler.
type Backend struct {
	// Backend is the filter backend configuration.
	Backend *filterapi.Backend
	// Handler is the backend auth handler.
	Handler backendauth.Handler
}

// RequestCost is the configuration for the request cost, optionally with a CEL program.
type RequestCost struct {
	*filterapi.LLMRequestCost
	CELProg cel.Program
}

// NewRuntimeFilterConfig creates a new runtime filter configuration from the given filterapi.Config.
func NewRuntimeFilterConfig(ctx context.Context, config *filterapi.Config) (*Config, error) {
	backends := make(map[string]*Backend, len(config.Backends))
	for _, backend := range config.Backends {
		b := backend
		var h backendauth.Handler
		if b.Auth != nil {
			var err error
			h, err = backendauth.NewHandler(ctx, b.Auth)
			if err != nil {
				return nil, fmt.Errorf("cannot create backend auth handler: %w", err)
			}
		}
		backends[b.Name] = &Backend{Backend: &b, Handler: h}
	}

	costs := make([]RequestCost, 0, len(config.LLMRequestCosts))
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
		costs = append(costs, RequestCost{LLMRequestCost: c, CELProg: prog})
	}

	return &Config{
		UUID:           config.UUID,
		Backends:       backends,
		RequestCosts:   costs,
		DeclaredModels: config.Models,
	}, nil
}
