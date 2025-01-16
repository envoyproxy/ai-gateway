package main

import (
	"fmt"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
	"github.com/envoyproxy/ai-gateway/extprocapi"
	"github.com/envoyproxy/ai-gateway/filterconfig"
)

// newCustomRouter implements [extprocapi.NewCustomRouter].
func newCustomRouter(defaultRouter extprocapi.Router, config *filterconfig.Config) extprocapi.Router {
	// You can poke the current configuration of the routes, and the list of backends
	// specified in the AIGatewayRoute.Rules, etc.
	return &myCustomRouter{config: config, defaultRouter: defaultRouter}
}

// myCustomRouter implements [extprocapi.Router].
type myCustomRouter struct {
	config        *filterconfig.Config
	defaultRouter extprocapi.Router
}

// Calculate implements [extprocapi.Router.Calculate].
func (m *myCustomRouter) Calculate(headers map[string]string) (backend *filterconfig.Backend, err error) {
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
	extprocapi.NewCustomRouter = newCustomRouter
	// Executes the main function of the external processor.
	mainlib.Main()
}
