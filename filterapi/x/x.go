// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package x is an experimental package that provides the customizability of the AI Gateway filter.
package x

import (
	"errors"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

// NewCustomRouter is the function to create a custom router over the default router.
// This is nil by default and can be set by the custom build of external processor.
var NewCustomRouter NewCustomRouterFn

// ErrNoMatchingRule is the error the router function must return if there is no matching rule.
var ErrNoMatchingRule = errors.New("no matching rule found")

// ErrNotImplemented is the error returned when a method is not implemented.
var ErrNotImplemented = errors.New("method not implemented")

// NewCustomRouterFn is the function signature for [NewCustomRouter].
//
// It accepts the exptproc config passed to the AI Gateway filter and returns a [Router].
// This is called when the new configuration is loaded.
//
// The defaultRouter can be used to delegate the calculation to the default router implementation.
type NewCustomRouterFn func(defaultRouter Router, config *filterapi.Config) Router

// Router is the interface for the router.
//
// Router must be goroutine-safe as it is shared across multiple requests.
type Router interface {
	// Calculate determines the backend to route to based on the request headers.
	//
	// The request headers include the populated [filterapi.Config.ModelNameHeaderKey]
	// with the parsed model name based on the [filterapi.Config] given to the NewCustomRouterFn.
	//
	// Returns the backend.
	Calculate(requestHeaders map[string]string) (backend *filterapi.Backend, err error)

	// CalculateWithBody determines the backend to route to based on both request headers and body.
	// This is an optional method that can be implemented to support semantic routing.
	// If not implemented, the router will fall back to Calculate.
	//
	// The body parameter contains the full request body.
	//
	// Returns the backend and optional cache information.
	CalculateWithBody(requestHeaders map[string]string, body []byte) (backend *filterapi.Backend, cacheInfo *filterapi.CacheInfo, err error)
}
