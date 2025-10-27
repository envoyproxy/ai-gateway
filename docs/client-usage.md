# AI Gateway Kubernetes Client Usage Guide

This guide demonstrates how to use the generated Kubernetes client for AI Gateway resources.

## Overview

The AI Gateway project provides a typed Kubernetes client for managing AI Gateway resources programmatically. The client includes:

- **Typed Clientset**: Type-safe CRUD operations for all custom resources
- **Informers**: Efficient caching and watching of resources
- **Listers**: Fast in-memory queries using cached data
- **Fake Clients**: Built-in testing support

## Installation

The client is part of the ai-gateway module. Import it in your Go project:

```go
import (
	"github.com/envoyproxy/ai-gateway/api/v1alpha1"
	clientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned"
	informers "github.com/envoyproxy/ai-gateway/pkg/client/informers/externalversions"
	listers "github.com/envoyproxy/ai-gateway/pkg/client/listers/api/v1alpha1"
)
```

## Creating a Client

### From Kubeconfig

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
	kubeconfig := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Fatalf("Error building config: %v", err)
	}

	// Create AI Gateway client
	client, err := clientset.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating client: %v", err)
	}

	// Use the client
	routes, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").List(
		context.Background(),
		metav1.ListOptions{},
	)
	if err != nil {
		log.Fatalf("Error listing routes: %v", err)
	}

	fmt.Printf("Found %d AIGatewayRoutes\n", len(routes.Items))
}
```

### From In-Cluster Config

For applications running inside a Kubernetes cluster:

```go
import (
	"k8s.io/client-go/rest"
	clientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned"
)

func main() {
	// Get in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Error getting in-cluster config: %v", err)
	}

	// Create client
	client, err := clientset.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating client: %v", err)
	}

	// Use the client...
}
```

## Working with AIGatewayRoutes

### Creating an AIGatewayRoute

```go
import (
	"github.com/envoyproxy/ai-gateway/api/v1alpha1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func createAIGatewayRoute(client clientset.Interface) error {
	route := &v1alpha1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-ai-route",
			Namespace: "default",
		},
		Spec: v1alpha1.AIGatewayRouteSpec{
			ParentRefs: []gwapiv1.ParentReference{
				{
					Name: "my-gateway",
				},
			},
			Rules: []v1alpha1.AIGatewayRouteRule{
				{
					BackendRefs: []v1alpha1.AIGatewayRouteRuleBackendRef{
						{
							Name:   "openai-backend",
							Weight: ptrTo(int32(70)),
						},
						{
							Name:   "anthropic-backend",
							Weight: ptrTo(int32(30)),
						},
					},
				},
			},
		},
	}

	created, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Create(
		context.Background(),
		route,
		metav1.CreateOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to create route: %w", err)
	}

	fmt.Printf("Created AIGatewayRoute: %s\n", created.Name)
	return nil
}

func ptrTo[T any](v T) *T {
	return &v
}
```

### Getting an AIGatewayRoute

```go
func getAIGatewayRoute(client clientset.Interface, name, namespace string) (*v1alpha1.AIGatewayRoute, error) {
	route, err := client.AigatewayV1alpha1().AIGatewayRoutes(namespace).Get(
		context.Background(),
		name,
		metav1.GetOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get route: %w", err)
	}

	fmt.Printf("Route %s has %d rules\n", route.Name, len(route.Spec.Rules))
	return route, nil
}
```

### Listing AIGatewayRoutes

```go
func listAIGatewayRoutes(client clientset.Interface, namespace string) error {
	routes, err := client.AigatewayV1alpha1().AIGatewayRoutes(namespace).List(
		context.Background(),
		metav1.ListOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}

	fmt.Printf("Found %d routes:\n", len(routes.Items))
	for _, route := range routes.Items {
		fmt.Printf("  - %s (rules: %d)\n", route.Name, len(route.Spec.Rules))
	}
	return nil
}
```

### Updating an AIGatewayRoute

```go
func updateAIGatewayRoute(client clientset.Interface, name, namespace string) error {
	// Get the current route
	route, err := client.AigatewayV1alpha1().AIGatewayRoutes(namespace).Get(
		context.Background(),
		name,
		metav1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to get route: %w", err)
	}

	// Modify the route
	route.Spec.Rules = append(route.Spec.Rules, v1alpha1.AIGatewayRouteRule{
		BackendRefs: []v1alpha1.AIGatewayRouteRuleBackendRef{
			{
				Name: "new-backend",
			},
		},
	})

	// Update the route
	updated, err := client.AigatewayV1alpha1().AIGatewayRoutes(namespace).Update(
		context.Background(),
		route,
		metav1.UpdateOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to update route: %w", err)
	}

	fmt.Printf("Updated route %s\n", updated.Name)
	return nil
}
```

### Deleting an AIGatewayRoute

```go
func deleteAIGatewayRoute(client clientset.Interface, name, namespace string) error {
	err := client.AigatewayV1alpha1().AIGatewayRoutes(namespace).Delete(
		context.Background(),
		name,
		metav1.DeleteOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to delete route: %w", err)
	}

	fmt.Printf("Deleted route %s\n", name)
	return nil
}
```

## Working with AIServiceBackends

### Creating an AIServiceBackend

```go
func createAIServiceBackend(client clientset.Interface) error {
	backend := &v1alpha1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openai-backend",
			Namespace: "default",
		},
		Spec: v1alpha1.AIServiceBackendSpec{
			APISchema: v1alpha1.VersionedAPISchema{
				Name: v1alpha1.APISchemaOpenAI,
			},
			BackendRef: gwapiv1.BackendObjectReference{
				Name:  "openai-service",
				Group: ptrTo(gwapiv1.Group("gateway.envoyproxy.io")),
				Kind:  ptrTo(gwapiv1.Kind("Backend")),
			},
		},
	}

	created, err := client.AigatewayV1alpha1().AIServiceBackends("default").Create(
		context.Background(),
		backend,
		metav1.CreateOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to create backend: %w", err)
	}

	fmt.Printf("Created AIServiceBackend: %s\n", created.Name)
	return nil
}
```

## Using Informers

Informers provide efficient caching and watching of resources. They're ideal for controllers and applications that need to react to resource changes.

### Basic Informer Setup

```go
package main

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/envoyproxy/ai-gateway/api/v1alpha1"
	clientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned"
	informers "github.com/envoyproxy/ai-gateway/pkg/client/informers/externalversions"
)

func main() {
	// Create client
	kubeconfig := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		panic(err)
	}

	client, err := clientset.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	// Create informer factory
	// Resync period: informers will re-list all resources every 10 minutes
	factory := informers.NewSharedInformerFactory(client, 10*time.Minute)

	// Get AIGatewayRoute informer
	routeInformer := factory.Aigateway().V1alpha1().AIGatewayRoutes()

	// Add event handler
	routeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			route := obj.(*v1alpha1.AIGatewayRoute)
			fmt.Printf("Route added: %s/%s\n", route.Namespace, route.Name)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			route := newObj.(*v1alpha1.AIGatewayRoute)
			fmt.Printf("Route updated: %s/%s\n", route.Namespace, route.Name)
		},
		DeleteFunc: func(obj interface{}) {
			route := obj.(*v1alpha1.AIGatewayRoute)
			fmt.Printf("Route deleted: %s/%s\n", route.Namespace, route.Name)
		},
	})

	// Start informers
	ctx := context.Background()
	factory.Start(ctx.Done())

	// Wait for caches to sync
	factory.WaitForCacheSync(ctx.Done())

	fmt.Println("Informers started and caches synced")

	// Keep running
	<-ctx.Done()
}
```

## Using Listers

Listers provide fast, read-only access to cached data from informers. They're perfect for querying resources without hitting the API server.

### Using a Lister

```go
func useListers(client clientset.Interface) {
	// Create informer factory
	factory := informers.NewSharedInformerFactory(client, 10*time.Minute)

	// Get lister
	routeLister := factory.Aigateway().V1alpha1().AIGatewayRoutes().Lister()

	// Start informers
	ctx := context.Background()
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	// Get a specific route from cache
	route, err := routeLister.AIGatewayRoutes("default").Get("my-route")
	if err != nil {
		fmt.Printf("Error getting route: %v\n", err)
		return
	}
	fmt.Printf("Found route: %s\n", route.Name)

	// List all routes in a namespace from cache
	routes, err := routeLister.AIGatewayRoutes("default").List(labels.Everything())
	if err != nil {
		fmt.Printf("Error listing routes: %v\n", err)
		return
	}
	fmt.Printf("Found %d routes in default namespace\n", len(routes))

	// List routes with label selector
	selector := labels.SelectorFromSet(labels.Set{"app": "my-app"})
	filteredRoutes, err := routeLister.AIGatewayRoutes("default").List(selector)
	if err != nil {
		fmt.Printf("Error listing filtered routes: %v\n", err)
		return
	}
	fmt.Printf("Found %d routes with app=my-app\n", len(filteredRoutes))
}
```

## Testing with Fake Clients

The fake client is perfect for unit testing without requiring a real Kubernetes cluster.

### Example Test

```go
package myapp_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/envoyproxy/ai-gateway/api/v1alpha1"
	fakeclientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned/fake"
)

func TestMyFunction(t *testing.T) {
	// Create fake client
	client := fakeclientset.NewSimpleClientset()

	// Create a route
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
	if err != nil {
		t.Fatalf("Failed to create route: %v", err)
	}

	if created.Name != "test-route" {
		t.Errorf("Expected route name 'test-route', got '%s'", created.Name)
	}

	// Test your function that uses the client
	// myFunction(client)
}
```

## Complete Example Application

Here's a complete example that demonstrates various client operations:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/envoyproxy/ai-gateway/api/v1alpha1"
	clientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned"
	informers "github.com/envoyproxy/ai-gateway/pkg/client/informers/externalversions"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func main() {
	// Load kubeconfig
	kubeconfig := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Fatalf("Error building config: %v", err)
	}

	// Create client
	client, err := clientset.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating client: %v", err)
	}

	ctx := context.Background()

	// Create an AIServiceBackend
	backend := &v1alpha1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openai-backend",
			Namespace: "default",
		},
		Spec: v1alpha1.AIServiceBackendSpec{
			APISchema: v1alpha1.VersionedAPISchema{
				Name: v1alpha1.APISchemaOpenAI,
			},
			BackendRef: gwapiv1.BackendObjectReference{
				Name:  "openai-service",
				Group: ptrTo(gwapiv1.Group("gateway.envoyproxy.io")),
				Kind:  ptrTo(gwapiv1.Kind("Backend")),
			},
		},
	}

	_, err = client.AigatewayV1alpha1().AIServiceBackends("default").Create(ctx, backend, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Backend might already exist: %v", err)
	} else {
		fmt.Println("Created AIServiceBackend: openai-backend")
	}

	// Create an AIGatewayRoute
	route := &v1alpha1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-ai-route",
			Namespace: "default",
		},
		Spec: v1alpha1.AIGatewayRouteSpec{
			Rules: []v1alpha1.AIGatewayRouteRule{
				{
					BackendRefs: []v1alpha1.AIGatewayRouteRuleBackendRef{
						{
							Name: "openai-backend",
						},
					},
				},
			},
		},
	}

	_, err = client.AigatewayV1alpha1().AIGatewayRoutes("default").Create(ctx, route, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Route might already exist: %v", err)
	} else {
		fmt.Println("Created AIGatewayRoute: my-ai-route")
	}

	// Setup informers
	factory := informers.NewSharedInformerFactory(client, 30*time.Second)
	routeInformer := factory.Aigateway().V1alpha1().AIGatewayRoutes()

	routeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			route := obj.(*v1alpha1.AIGatewayRoute)
			fmt.Printf("[Event] Route added: %s/%s\n", route.Namespace, route.Name)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			route := newObj.(*v1alpha1.AIGatewayRoute)
			fmt.Printf("[Event] Route updated: %s/%s\n", route.Namespace, route.Name)
		},
	})

	// Start informers
	stopCh := make(chan struct{})
	defer close(stopCh)
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)

	fmt.Println("\nInformers started. Watching for changes...")

	// Use lister
	lister := routeInformer.Lister()
	routes, err := lister.AIGatewayRoutes("default").List(labels.Everything())
	if err != nil {
		log.Fatalf("Error listing routes: %v", err)
	}

	fmt.Printf("\nFound %d routes via lister:\n", len(routes))
	for _, r := range routes {
		fmt.Printf("  - %s (rules: %d)\n", r.Name, len(r.Spec.Rules))
	}

	// Keep running for a bit to see events
	fmt.Println("\nWatching for 30 seconds...")
	time.Sleep(30 * time.Second)
}

func ptrTo[T any](v T) *T {
	return &v
}
```

## Best Practices

1. **Use Informers for Watching**: Informers are more efficient than repeatedly calling List/Get
2. **Use Listers for Queries**: When you need to query resources frequently, use listers backed by informers
3. **Handle Errors Gracefully**: Always check for `errors.IsNotFound()`, `errors.IsAlreadyExists()`, etc.
4. **Set Appropriate Resync Periods**: For informers, choose resync periods based on your needs (typically 5-30 minutes)
5. **Use Context**: Always pass context for cancellation and timeout support
6. **Test with Fake Clients**: Use fake clients for unit testing without requiring a real cluster

## Related Resources

- [AI Gateway API Documentation](../api/api.mdx)
- [Kubernetes Client-Go Documentation](https://github.com/kubernetes/client-go)
- [Controller Runtime Documentation](https://github.com/kubernetes-sigs/controller-runtime)
