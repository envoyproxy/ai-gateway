// Package extprocapi is for building a custom external process.
package extprocapi

import "github.com/envoyproxy/ai-gateway/filterconfig"

// NewCustomRouter is the function to create a custom router over the default router.
// This is nil by default and can be set by the custom build of external processor.
var NewCustomRouter NewCustomRouterFn

// NewCustomRouterFn is the function signature for [NewCustomRouter].
//
// It accepts the exptproc config passed to the AI Gateway filter and returns a [Router].
// This is called when the new configuration is loaded.
//
// The defaultRouter can be used to delegate the calculation to the default router implementation.
type NewCustomRouterFn func(defaultRouter Router, config *filterconfig.Config) Router

// Router is the interface for the router.
//
// Router must be goroutine-safe as it is shared across multiple requests.
type Router interface {
	// Calculate determines the backend to route to based on the request headers.
	//
	// The request headers include the populated [filterconfig.Config.ModelNameHeaderKey]
	// with the parsed model name based on the [filterconfig.Config] given to the NewCustomRouterFn.
	//
	// Returns the backend.
	Calculate(requestHeaders map[string]string) (backend *filterconfig.Backend, err error)
}
