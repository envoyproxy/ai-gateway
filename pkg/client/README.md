# AI Gateway Kubernetes Client

This package contains the generated Kubernetes client for AI Gateway custom resources.

## Overview

The client provides typed access to AI Gateway resources:

- **AIGatewayRoute** - Routes for AI service traffic
- **AIServiceBackend** - Backend service configurations for AI providers
- **BackendSecurityPolicy** - Security policies for backend authentication
- **MCPRoute** - Model Context Protocol routing

## Package Structure

```
pkg/client/
├── clientset/          # Typed clientset for direct API calls
│   └── versioned/
│       ├── typed/      # Type-safe resource clients
│       └── fake/       # Fake clients for testing
├── informers/          # Shared informer factories for caching
├── listers/            # Cached lister interfaces
├── tests/              # Comprehensive test suite
│   ├── typed_client_test.go   # Tests for typed clients
│   └── informers_test.go      # Tests for informers and listers
└── README.md           # This file
```

## Usage

### Creating a Client

```go
import (
    "k8s.io/client-go/tools/clientcmd"
    clientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned"
)

config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
client, err := clientset.NewForConfig(config)
```

### Using Typed Clients

```go
// List AIGatewayRoutes
routes, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").List(ctx, metav1.ListOptions{})

// Get a specific backend
backend, err := client.AigatewayV1alpha1().AIServiceBackends("default").Get(ctx, "my-backend", metav1.GetOptions{})
```

### Using Informers and Listers

```go
import (
    informers "github.com/envoyproxy/ai-gateway/pkg/client/informers/externalversions"
)

factory := informers.NewSharedInformerFactory(client, 10*time.Minute)
routeInformer := factory.Aigateway().V1alpha1().AIGatewayRoutes()

// Add event handler
routeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
    AddFunc: func(obj interface{}) {
        route := obj.(*v1alpha1.AIGatewayRoute)
        fmt.Printf("Route added: %s\n", route.Name)
    },
})

// Start informers
factory.Start(ctx.Done())
factory.WaitForCacheSync(ctx.Done())

// Use lister (cached)
lister := routeInformer.Lister()
route, err := lister.AIGatewayRoutes("default").Get("my-route")
```

## Documentation

- **Usage Examples**: See [docs/client-usage.md](../../docs/client-usage.md)
- **Deployment Guide**: See [docs/client-deployment-guide.md](../../docs/client-deployment-guide.md)
- **Implementation Summary**: See [docs/CLIENT_IMPLEMENTATION_SUMMARY.md](../../docs/CLIENT_IMPLEMENTATION_SUMMARY.md)

## Code Generation

This client code is generated using Kubernetes code-generator tools. To regenerate:

```bash
make codegen
```

The generation script is located at `hack/update-codegen.sh`.

**Note**: The `tests/` directory and this `README.md` are preserved during code generation.

## Testing

Run the comprehensive test suite:

```bash
# Run all client tests
go test ./pkg/client/tests/... -v

# Run with short mode (faster)
go test ./pkg/client/tests/... -v -short
```

## Performance

| Operation     | Method          | Performance      |
| ------------- | --------------- | ---------------- |
| Single Get    | Direct API call | ~10-50ms         |
| List All      | Direct API call | ~50-200ms        |
| Get (cached)  | Lister          | <1ms             |
| List (cached) | Lister          | <1ms             |
| Watch         | Informer        | Real-time events |

## Integration with Other Tools

This client can be used alongside:

- `controller-runtime` dynamic clients
- Standard Kubernetes clients
- Custom controllers and operators
- CLI tools and scripts

## License

Copyright Envoy AI Gateway Authors
SPDX-License-Identifier: Apache-2.0
