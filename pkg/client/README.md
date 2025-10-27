# AI Gateway Kubernetes Client

This package provides a typed Kubernetes client for AI Gateway custom resources.

## Overview

The AI Gateway client allows you to programmatically interact with AI Gateway resources (AIGatewayRoutes, AIServiceBackends, BackendSecurityPolicies, and MCPRoutes) in your Kubernetes cluster.

## Features

- **Type-safe Clientset**: Strongly typed Go client for all AI Gateway CRDs
- **Informers**: Efficient caching and watching of resources with event handlers
- **Listers**: Fast in-memory queries using cached data
- **Fake Clients**: Built-in testing support without requiring a real cluster
- **Standard Kubernetes Patterns**: Follows established client-go conventions

## Quick Start

### Installation

```go
import (
	"github.com/envoyproxy/ai-gateway/api/v1alpha1"
	clientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned"
)
```

### Basic Usage

```go
package main

import (
	"context"
	"fmt"
	"log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"

	clientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned"
)

func main() {
	// Load kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("",
		clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename())
	if err != nil {
		log.Fatal(err)
	}

	// Create client
	client, err := clientset.NewForConfig(config)
	if err != nil {
		log.Fatal(err)
	}

	// List AIGatewayRoutes
	routes, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").List(
		context.Background(),
		metav1.ListOptions{},
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Found %d routes\n", len(routes.Items))
}
```

## Package Structure

```
pkg/client/
├── clientset/versioned/          # Type-safe clients for AI Gateway resources
│   ├── typed/api/v1alpha1/       # v1alpha1 typed clients
│   │   ├── aigatewayroute.go
│   │   ├── aiservicebackend.go
│   │   ├── backendsecuritypolicy.go
│   │   └── mcproute.go
│   └── fake/                     # Fake clients for testing
├── informers/externalversions/   # Informers for watching resources
│   └── api/v1alpha1/
├── listers/api/v1alpha1/         # Listers for querying cached data
└── README.md                     # This file
```

## Available Clients

### AIGatewayRoute Client

```go
routeClient := client.AigatewayV1alpha1().AIGatewayRoutes(namespace)

// CRUD operations
route, err := routeClient.Create(ctx, route, metav1.CreateOptions{})
route, err := routeClient.Get(ctx, name, metav1.GetOptions{})
routes, err := routeClient.List(ctx, metav1.ListOptions{})
route, err := routeClient.Update(ctx, route, metav1.UpdateOptions{})
err := routeClient.Delete(ctx, name, metav1.DeleteOptions{})
```

### AIServiceBackend Client

```go
backendClient := client.AigatewayV1alpha1().AIServiceBackends(namespace)

// CRUD operations
backend, err := backendClient.Create(ctx, backend, metav1.CreateOptions{})
backend, err := backendClient.Get(ctx, name, metav1.GetOptions{})
backends, err := backendClient.List(ctx, metav1.ListOptions{})
backend, err := backendClient.Update(ctx, backend, metav1.UpdateOptions{})
err := backendClient.Delete(ctx, name, metav1.DeleteOptions{})
```

### BackendSecurityPolicy Client

```go
policyClient := client.AigatewayV1alpha1().BackendSecurityPolicies(namespace)

// CRUD operations
policy, err := policyClient.Create(ctx, policy, metav1.CreateOptions{})
policy, err := policyClient.Get(ctx, name, metav1.GetOptions{})
policies, err := policyClient.List(ctx, metav1.ListOptions{})
policy, err := policyClient.Update(ctx, policy, metav1.UpdateOptions{})
err := policyClient.Delete(ctx, name, metav1.DeleteOptions{})
```

### MCPRoute Client

```go
mcpClient := client.AigatewayV1alpha1().MCPRoutes(namespace)

// CRUD operations
mcpRoute, err := mcpClient.Create(ctx, mcpRoute, metav1.CreateOptions{})
mcpRoute, err := mcpClient.Get(ctx, name, metav1.GetOptions{})
mcpRoutes, err := mcpClient.List(ctx, metav1.ListOptions{})
mcpRoute, err := mcpClient.Update(ctx, mcpRoute, metav1.UpdateOptions{})
err := mcpClient.Delete(ctx, name, metav1.DeleteOptions{})
```

## Using Informers

Informers provide efficient caching and watching capabilities:

```go
import (
    informers "github.com/envoyproxy/ai-gateway/pkg/client/informers/externalversions"
    "k8s.io/client-go/tools/cache"
)

// Create informer factory
factory := informers.NewSharedInformerFactory(client, 10*time.Minute)

// Get informer for AIGatewayRoutes
routeInformer := factory.Aigateway().V1alpha1().AIGatewayRoutes()

// Add event handler
routeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
    AddFunc: func(obj interface{}) {
        route := obj.(*v1alpha1.AIGatewayRoute)
        fmt.Printf("Route added: %s\n", route.Name)
    },
    UpdateFunc: func(oldObj, newObj interface{}) {
        route := newObj.(*v1alpha1.AIGatewayRoute)
        fmt.Printf("Route updated: %s\n", route.Name)
    },
    DeleteFunc: func(obj interface{}) {
        route := obj.(*v1alpha1.AIGatewayRoute)
        fmt.Printf("Route deleted: %s\n", route.Name)
    },
})

// Start informers
factory.Start(ctx.Done())
factory.WaitForCacheSync(ctx.Done())
```

## Using Listers

Listers provide fast, cached access to resources:

```go
import (
    "k8s.io/apimachinery/pkg/labels"
)

// Get lister from informer
lister := routeInformer.Lister()

// Get a specific resource from cache
route, err := lister.AIGatewayRoutes("default").Get("my-route")

// List all resources in a namespace
routes, err := lister.AIGatewayRoutes("default").List(labels.Everything())

// List with label selector
selector := labels.SelectorFromSet(labels.Set{"app": "my-app"})
filteredRoutes, err := lister.AIGatewayRoutes("default").List(selector)
```

## Testing with Fake Clients

The fake client is perfect for unit testing:

```go
import (
	fakeclientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned/fake"
)

func TestMyFunction(t *testing.T) {
	// Create fake client
	client := fakeclientset.NewSimpleClientset()

	// Use it like a real client
	route := &v1alpha1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-route",
			Namespace: "default",
		},
	}

	created, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Create(
		context.Background(),
		route,
		metav1.CreateOptions{},
	)
	// ... assertions
}
```

## Documentation

- **[Client Usage Guide](../../docs/client-usage.md)**: Comprehensive usage examples and patterns
- **[Deployment Guide](../../docs/client-deployment-guide.md)**: Step-by-step deployment and testing instructions
- **[API Reference](../../docs/api/api.mdx)**: Complete API documentation

## Development

### Generating the Client

The client code is generated using Kubernetes code-generator:

```bash
# Generate client code
make codegen

# Verify generation
make verify-codegen
```

### Running Tests

```bash
# Run all client tests
go test ./pkg/client/... -v

# Run specific test package
go test ./pkg/client/clientset/versioned/typed/api/v1alpha1/... -v

# Run with coverage
go test ./pkg/client/... -cover
```

## Requirements

- Go 1.25+
- Kubernetes 1.31+
- AI Gateway CRDs installed in the cluster

## Contributing

Contributions are welcome! Please see the main [CONTRIBUTING.md](../../CONTRIBUTING.md) for guidelines.

## License

Copyright Envoy AI Gateway Authors. Licensed under the Apache License, Version 2.0.
