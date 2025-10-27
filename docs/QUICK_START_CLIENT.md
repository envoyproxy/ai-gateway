# AI Gateway Client - Quick Start

## Installation

Add the AI Gateway client to your Go module:

```bash
go get github.com/envoyproxy/ai-gateway
```

## Basic Usage

### 1. Create a Client

```go
import (
	"k8s.io/client-go/tools/clientcmd"
	clientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned"
)

func main() {
	// Load kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("",
		clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename())
	if err != nil {
		panic(err)
	}

	// Create client
	client, err := clientset.NewForConfig(config)
	if err != nil {
		panic(err)
	}
}
```

### 2. List Resources

```go
routes, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").List(
	context.Background(),
	metav1.ListOptions{},
)
```

### 3. Use Informers (Recommended for Controllers)

```go
import (
    informers "github.com/envoyproxy/ai-gateway/pkg/client/informers/externalversions"
)

factory := informers.NewSharedInformerFactory(client, 10*time.Minute)
routeInformer := factory.Aigateway().V1alpha1().AIGatewayRoutes()

// Start watching
factory.Start(ctx.Done())
factory.WaitForCacheSync(ctx.Done())

// Use lister (cached)
lister := routeInformer.Lister()
route, err := lister.AIGatewayRoutes("default").Get("my-route")
```

## Development

### Generate Client Code

```bash
make codegen
```

### Run Tests

```bash
# All tests
go test ./pkg/client/tests/... -v

# Quick test
go test ./pkg/client/tests/... -v -short
```

### Verify Code Generation

```bash
make verify-codegen
```

## Documentation

- **Full Usage Guide**: [docs/client-usage.md](client-usage.md)
- **Deployment Guide**: [docs/client-deployment-guide.md](client-deployment-guide.md)
- **Implementation Details**: [docs/CLIENT_IMPLEMENTATION_SUMMARY.md](CLIENT_IMPLEMENTATION_SUMMARY.md)

## Resources

- AIGatewayRoute - AI service routing
- AIServiceBackend - Backend configurations
- BackendSecurityPolicy - Security policies
- MCPRoute - Model Context Protocol routes

## Support

For issues and questions:

- GitHub Issues: https://github.com/envoyproxy/ai-gateway/issues
- Documentation: https://gateway.envoyproxy.io/ai-gateway
