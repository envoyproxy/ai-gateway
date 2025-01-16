package main

import (
	"github.com/envoyproxy/ai-gateway/filterconfig"
	maincmd "github.com/envoyproxy/ai-gateway/internal/extproc/main"
)

// newCustomRouter implements [filterconfig.NewCustomRouter].
func newCustomRouter(rules []filterconfig.RouteRule) filterconfig.CustomRouter {
	// You can poke the current configuration of the routes, and the list of backends
	// specified in the AIGatewayRoute.Rules.
	return myCustomRouter{}
}

// myCustomRouter implements [filterconfig.CustomRouter].
type myCustomRouter struct{}

// Calculate implements [filterconfig.CustomRouter.Calculate].
func (m myCustomRouter) Calculate(headers map[string]string) (backend *filterconfig.Backend, err error) {
	//TODO implement me
	panic("implement me")
}

func main() {
	// Initializes the custom router.
	filterconfig.NewCustomRouter = newCustomRouter
	// Executes the main function of the external processor.
	maincmd.Main()
}
