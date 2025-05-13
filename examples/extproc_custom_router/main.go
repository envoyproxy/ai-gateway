// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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

// myCustomRouter implements [x.Router].
type myCustomRouter struct {
	config        *filterapi.Config
	defaultRouter x.Router
}

// Calculate implements [x.Router.Calculate].
func (m *myCustomRouter) Calculate(headers map[string]string) (backend filterapi.RouteRuleName, err error) {
	// Simply logs the headers and delegates the calculation to the default router.
	modelName, ok := headers[m.config.ModelNameHeaderKey]
	if !ok {
		panic("model name not found in the headers")
	}
	fmt.Printf("model name: %s\n", modelName)
	return m.defaultRouter.Calculate(headers)
}

// This demonstrates how to build a custom router for the external processor.
func main() {
	// Initializes the custom router.
	x.NewCustomRouter = newCustomRouter
	// Executes the main function of the external processor.
	ctx, cancel := context.WithCancel(context.Background())
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalsChan
		cancel()
	}()
	mainlib.Main(ctx, os.Args[1:], os.Stderr)
}
