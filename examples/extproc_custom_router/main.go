// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"fmt"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
)

// newCustomRouter implements [x.NewCustomRouter].
func newCustomRouter(defaultRouter x.Router, config *filterapi.Config) x.Router {
	// You can poke the current configuration of the routes, and the list of backends
	// specified in the AIGatewayRoute.Rules, etc.
	return &myCustomRouter{config: config, defaultRouter: defaultRouter}
}

// myCustomRouter implements [filterapi.Router].
type myCustomRouter struct {
	config        *filterapi.Config
	defaultRouter x.Router
}

// Calculate implements [x.Router.Calculate].
func (m *myCustomRouter) Calculate(headers map[string]string) (backend *filterapi.Backend, err error) {
	// Simply logs the headers and delegates the calculation to the default router.
	modelName, ok := headers[m.config.ModelNameHeaderKey]
	if !ok {
		panic("model name not found in the headers")
	}
	fmt.Printf("model name: %s\n", modelName)
	return m.defaultRouter.Calculate(headers)
}

// CalculateWithBody implements [x.Router.CalculateWithBody].
func (m *myCustomRouter) CalculateWithBody(headers map[string]string, body []byte) (backend *filterapi.Backend, cacheInfo *filterapi.CacheInfo, err error) {
	return nil, nil, x.ErrNotImplemented
}

// This demonstrates how to build a custom router for the external processor.
func main() {
	// Initializes the custom router.
	x.NewCustomRouter = newCustomRouter
	// Executes the main function of the external processor.
	mainlib.Main()
}
