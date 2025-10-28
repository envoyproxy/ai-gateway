# Go Client

AI Gateway provides a typed Go client for interacting with custom resources programmatically.

## Installation

```bash
go get github.com/envoyproxy/ai-gateway
```

## Usage

### Creating a Client

```go
import (
    "k8s.io/client-go/tools/clientcmd"
    clientset "github.com/envoyproxy/ai-gateway/api/v1alpha1/client/clientset/versioned"
)

config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
client, err := clientset.NewForConfig(config)
```

### Basic Operations

```go
// List AIGatewayRoutes
routes, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").List(ctx, metav1.ListOptions{})

// Get a specific backend
backend, err := client.AigatewayV1alpha1().AIServiceBackends("default").Get(ctx, "my-backend", metav1.GetOptions{})

// Create a resource
route := &v1alpha1.AIGatewayRoute{...}
created, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Create(ctx, route, metav1.CreateOptions{})
```

### Using Informers

For controllers and watch scenarios, use informers for efficient caching:

```go
import (
    informers "github.com/envoyproxy/ai-gateway/api/v1alpha1/client/informers/externalversions"
)

factory := informers.NewSharedInformerFactory(client, 10*time.Minute)
routeInformer := factory.Aigateway().V1alpha1().AIGatewayRoutes()

// Add event handler
routeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
    AddFunc: func(obj interface{}) {
        route := obj.(*v1alpha1.AIGatewayRoute)
        // Handle add
    },
})

// Start informers
factory.Start(ctx.Done())
factory.WaitForCacheSync(ctx.Done())

// Use lister for cached reads
lister := routeInformer.Lister()
route, err := lister.AIGatewayRoutes("default").Get("my-route")
```

## Development

### Generate Client Code

The client code is generated from the API types. To regenerate:

```bash
make codegen
```

### Run Tests

```bash
go test ./api/v1alpha1/client/tests/...
```

## Resources

- **AIGatewayRoute** - Routes for AI service traffic
- **AIServiceBackend** - Backend service configurations
- **BackendSecurityPolicy** - Security policies
- **MCPRoute** - Model Context Protocol routing

