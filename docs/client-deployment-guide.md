# AI Gateway Client Deployment and Testing Guide

This guide provides step-by-step instructions for deploying and testing the AI Gateway Kubernetes client on a cluster.

## Prerequisites

Before you begin, ensure you have the following:

- Kubernetes cluster (v1.31+ recommended)
- `kubectl` configured to access your cluster
- `helm` (v3.0+) installed
- Go 1.25+ installed (for building from source)
- Access to the cluster with appropriate RBAC permissions

## Table of Contents

1. [Building the Client](#building-the-client)
2. [Installing AI Gateway CRDs](#installing-ai-gateway-crds)
3. [Deploying AI Gateway Controller](#deploying-ai-gateway-controller)
4. [Creating Test Resources](#creating-test-resources)
5. [Testing the Client](#testing-the-client)
6. [Troubleshooting](#troubleshooting)

## Building the Client

### 1. Clone the Repository

```bash
git clone https://github.com/envoyproxy/ai-gateway.git
cd ai-gateway
```

### 2. Generate the Client Code

```bash
# Install dependencies
make tidy

# Generate CRDs and client code
make apigen
make codegen
```

### 3. Run Tests

```bash
# Run all client tests
go test ./pkg/client/... -v

# Run specific test
go test ./pkg/client/clientset/versioned/typed/api/v1alpha1/... -v
```

## Installing AI Gateway CRDs

### Option 1: Using Helm

```bash
# Add the AI Gateway Helm repository (if published)
helm repo add ai-gateway https://envoyproxy.github.io/ai-gateway
helm repo update

# Install CRDs
helm install ai-gateway-crds ai-gateway/ai-gateway-crds-helm \
  --namespace ai-gateway-system \
  --create-namespace
```

### Option 2: Using kubectl

```bash
# Apply CRDs directly from manifests
kubectl apply -f manifests/charts/ai-gateway-crds-helm/templates/
```

### Option 3: Build and Install from Source

```bash
# Build Helm charts
make helm-package

# Install the CRDs chart
helm install ai-gateway-crds ./out/ai-gateway-crds-helm-*.tgz \
  --namespace ai-gateway-system \
  --create-namespace
```

### Verify CRD Installation

```bash
# Check if CRDs are installed
kubectl get crds | grep aigateway.envoyproxy.io

# Expected output:
# aigatewayroutes.aigateway.envoyproxy.io
# aiservicebackends.aigateway.envoyproxy.io
# backendsecuritypolicies.aigateway.envoyproxy.io
# mcproutes.aigateway.envoyproxy.io
```

## Deploying AI Gateway Controller

### Install Envoy Gateway (Prerequisite)

AI Gateway requires Envoy Gateway to be installed:

```bash
# Install Envoy Gateway
helm install eg oci://docker.io/envoyproxy/gateway-helm \
  --version v1.2.4 \
  --namespace envoy-gateway-system \
  --create-namespace
```

### Install AI Gateway

```bash
# Build AI Gateway Helm chart
make helm-package

# Install AI Gateway
helm install ai-gateway ./out/ai-gateway-helm-*.tgz \
  --namespace ai-gateway-system \
  --create-namespace \
  --set image.tag=latest
```

### Verify Controller Deployment

```bash
# Check controller pods
kubectl get pods -n ai-gateway-system

# Check controller logs
kubectl logs -n ai-gateway-system deployment/ai-gateway-controller

# Check external processor pods
kubectl get pods -n ai-gateway-system -l app=ai-gateway-extproc
```

## Creating Test Resources

### 1. Create a Gateway

```bash
kubectl apply -f - << EOF
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: eg
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: ai-gateway
  namespace: default
spec:
  gatewayClassName: eg
  listeners:
  - name: http
    protocol: HTTP
    port: 80
EOF
```

### 2. Create an Envoy Backend

```bash
kubectl apply -f - << EOF
apiVersion: v1
kind: Service
metadata:
  name: openai-service
  namespace: default
spec:
  type: ExternalName
  externalName: api.openai.com
  ports:
  - port: 443
    name: https
---
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: Backend
metadata:
  name: openai-backend-eg
  namespace: default
spec:
  endpoints:
  - fqdn:
      hostname: api.openai.com
      port: 443
  appProtocols:
  - HTTPS
EOF
```

### 3. Create an AIServiceBackend

```bash
kubectl apply -f - << EOF
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: openai-backend
  namespace: default
spec:
  schema:
    name: OpenAI
  backendRef:
    name: openai-backend-eg
    group: gateway.envoyproxy.io
    kind: Backend
EOF
```

### 4. Create a BackendSecurityPolicy (Optional)

```bash
# Create a secret with API key
kubectl create secret generic openai-api-key \
  --from-literal=apiKey=sk-your-openai-api-key \
  --namespace default

# Create security policy
kubectl apply -f - << EOF
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: BackendSecurityPolicy
metadata:
  name: openai-security
  namespace: default
spec:
  type: APIKey
  targetRefs:
  - group: aigateway.envoyproxy.io
    kind: AIServiceBackend
    name: openai-backend
  apiKey:
    secretRef:
      name: openai-api-key
EOF
```

### 5. Create an AIGatewayRoute

```bash
kubectl apply -f - << EOF
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: my-ai-route
  namespace: default
spec:
  parentRefs:
  - name: ai-gateway
  rules:
  - backendRefs:
    - name: openai-backend
    matches:
    - headers:
      - name: x-ai-eg-model
        value: gpt-4
EOF
```

### 6. Verify Resources

```bash
# Check AIServiceBackends
kubectl get aiservicebackends -n default

# Check AIGatewayRoutes
kubectl get aigatewayroutes -n default

# Check BackendSecurityPolicies
kubectl get backendsecuritypolicies -n default

# Get detailed information
kubectl describe aigatewayroute my-ai-route -n default
```

## Testing the Client

### Test 1: Basic Client Operations

Create a test program to verify client functionality:

```go
// test-client.go
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

	// Create client
	client, err := clientset.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating client: %v", err)
	}

	ctx := context.Background()

	// List AIGatewayRoutes
	routes, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("Error listing routes: %v", err)
	}

	fmt.Printf("Found %d AIGatewayRoutes:\n", len(routes.Items))
	for _, route := range routes.Items {
		fmt.Printf("  - %s (rules: %d)\n", route.Name, len(route.Spec.Rules))
	}

	// List AIServiceBackends
	backends, err := client.AigatewayV1alpha1().AIServiceBackends("default").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("Error listing backends: %v", err)
	}

	fmt.Printf("\nFound %d AIServiceBackends:\n", len(backends.Items))
	for _, backend := range backends.Items {
		fmt.Printf("  - %s (schema: %s)\n", backend.Name, backend.Spec.APISchema.Name)
	}
}
```

Run the test:

```bash
go run test-client.go
```

### Test 2: Watch Resources with Informers

```go
// watch-resources.go
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
	kubeconfig := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		panic(err)
	}

	client, err := clientset.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	factory := informers.NewSharedInformerFactory(client, 30*time.Second)
	routeInformer := factory.Aigateway().V1alpha1().AIGatewayRoutes()

	routeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			route := obj.(*v1alpha1.AIGatewayRoute)
			fmt.Printf("[ADD] Route: %s/%s\n", route.Namespace, route.Name)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			route := newObj.(*v1alpha1.AIGatewayRoute)
			fmt.Printf("[UPDATE] Route: %s/%s\n", route.Namespace, route.Name)
		},
		DeleteFunc: func(obj interface{}) {
			route := obj.(*v1alpha1.AIGatewayRoute)
			fmt.Printf("[DELETE] Route: %s/%s\n", route.Namespace, route.Name)
		},
	})

	ctx := context.Background()
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	fmt.Println("Watching for AIGatewayRoute changes. Press Ctrl+C to exit...")
	<-ctx.Done()
}
```

Run and test:

```bash
# Terminal 1: Start the watcher
go run watch-resources.go

# Terminal 2: Make changes to trigger events
kubectl patch aigatewayroute my-ai-route -n default --type=json \
  -p='[{"op": "add", "path": "/metadata/labels", "value": {"test":"update"}}]'
```

### Test 3: Create Resources Programmatically

```bash
cat > create-resources.go << 'EOF'
package main

import (
    "context"
    "fmt"
    "log"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/tools/clientcmd"

    "github.com/envoyproxy/ai-gateway/api/v1alpha1"
    clientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned"
    gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func main() {
    kubeconfig := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
    config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
    if err != nil {
        log.Fatalf("Error building config: %v", err)
    }

    client, err := clientset.NewForConfig(config)
    if err != nil {
        log.Fatalf("Error creating client: %v", err)
    }

    ctx := context.Background()

    // Create AIServiceBackend
    backend := &v1alpha1.AIServiceBackend{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test-backend",
            Namespace: "default",
        },
        Spec: v1alpha1.AIServiceBackendSpec{
            APISchema: v1alpha1.VersionedAPISchema{
                Name: v1alpha1.APISchemaOpenAI,
            },
            BackendRef: gwapiv1.BackendObjectReference{
                Name:  "openai-backend-eg",
                Group: ptrTo(gwapiv1.Group("gateway.envoyproxy.io")),
                Kind:  ptrTo(gwapiv1.Kind("Backend")),
            },
        },
    }

    created, err := client.AigatewayV1alpha1().AIServiceBackends("default").Create(ctx, backend, metav1.CreateOptions{})
    if err != nil {
        log.Fatalf("Error creating backend: %v", err)
    }

    fmt.Printf("Created AIServiceBackend: %s\n", created.Name)

    // Create AIGatewayRoute
    route := &v1alpha1.AIGatewayRoute{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test-route",
            Namespace: "default",
        },
        Spec: v1alpha1.AIGatewayRouteSpec{
            Rules: []v1alpha1.AIGatewayRouteRule{
                {
                    BackendRefs: []v1alpha1.AIGatewayRouteRuleBackendRef{
                        {
                            Name: "test-backend",
                        },
                    },
                },
            },
        },
    }

    createdRoute, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Create(ctx, route, metav1.CreateOptions{})
    if err != nil {
        log.Fatalf("Error creating route: %v", err)
    }

    fmt.Printf("Created AIGatewayRoute: %s\n", createdRoute.Name)
}

func ptrTo[T any](v T) *T {
    return &v
}
EOF

go run create-resources.go
```

### Test 4: Integration Test Script

```bash
#!/bin/bash
# integration-test.sh

set -e

echo "=== AI Gateway Client Integration Test ==="

echo "1. Checking cluster connectivity..."
kubectl cluster-info

echo "2. Verifying CRDs are installed..."
kubectl get crds | grep aigateway.envoyproxy.io

echo "3. Creating test namespace..."
kubectl create namespace ai-gateway-test --dry-run=client -o yaml | kubectl apply -f -

echo "4. Creating test backend..."
kubectl apply -f - << EOF
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: Backend
metadata:
  name: test-backend-eg
  namespace: ai-gateway-test
spec:
  endpoints:
  - fqdn:
      hostname: api.openai.com
      port: 443
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: test-backend
  namespace: ai-gateway-test
spec:
  schema:
    name: OpenAI
  backendRef:
    name: test-backend-eg
    group: gateway.envoyproxy.io
    kind: Backend
EOF

echo "5. Waiting for backend to be created..."
kubectl wait --for=condition=Programmed aiservicebackend/test-backend -n ai-gateway-test --timeout=60s || true

echo "6. Creating test route..."
kubectl apply -f - << EOF
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: test-route
  namespace: ai-gateway-test
spec:
  rules:
  - backendRefs:
    - name: test-backend
EOF

echo "7. Verifying resources..."
kubectl get aiservicebackends -n ai-gateway-test
kubectl get aigatewayroutes -n ai-gateway-test

echo "8. Running client test..."
go run test-client.go

echo "9. Cleaning up..."
kubectl delete namespace ai-gateway-test

echo "=== Integration Test Complete ==="
```

Make it executable and run:

```bash
chmod +x integration-test.sh
./integration-test.sh
```

## Troubleshooting

### Issue: CRDs not found

**Solution:**

```bash
# Reinstall CRDs
kubectl apply -f manifests/charts/ai-gateway-crds-helm/templates/

# Verify installation
kubectl get crds | grep aigateway
```

### Issue: Client cannot connect to cluster

**Solution:**

```bash
# Check kubeconfig
kubectl config view

# Test connectivity
kubectl get nodes

# Verify RBAC permissions
kubectl auth can-i list aigatewayroutes --all-namespaces
```

### Issue: Resources not appearing in client

**Solution:**

```bash
# Check if resources exist
kubectl get aigatewayroutes --all-namespaces

# Verify client is looking in correct namespace
kubectl config get-contexts

# Check for API errors
kubectl get events -n <namespace>
```

### Issue: Informer not receiving events

**Solution:**

```go
// Add logging to troubleshoot
routeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
	AddFunc: func(obj interface{}) {
		log.Printf("ADD event received: %+v\n", obj)
	},
})

// Ensure factory is started and synced
factory.Start(ctx.Done())
if !cache.WaitForCacheSync(ctx.Done(), routeInformer.Informer().HasSynced) {
	log.Fatal("Failed to sync cache")
}
```

### Issue: Tests failing with "connection refused"

**Solution:**

```bash
# For integration tests, ensure cluster is accessible
export KUBECONFIG=~/.kube/config

# For unit tests, use fake clients
# No cluster connection needed

# Check if using correct test tags
go test ./pkg/client/... -v
```

## Performance Considerations

### Informer Resync Period

```go
// Short resync for development (frequent updates)
factory := informers.NewSharedInformerFactory(client, 10*time.Second)

// Long resync for production (reduced load)
factory := informers.NewSharedInformerFactory(client, 10*time.Minute)
```

### List vs Get Operations

```go
// Bad: Repeatedly calling Get
for _, name := range routeNames {
	route, _ := client.AigatewayV1alpha1().AIGatewayRoutes(ns).Get(ctx, name, metav1.GetOptions{})
	// ... use route
}

// Good: Use List once
routes, _ := client.AigatewayV1alpha1().AIGatewayRoutes(ns).List(ctx, metav1.ListOptions{})
for _, route := range routes.Items {
	// ... use route
}

// Best: Use Lister with Informer (cached)
routes, _ := lister.AIGatewayRoutes(ns).List(labels.Everything())
```

## Next Steps

- Review [Client Usage Guide](client-usage.md) for detailed API examples
- Check [AI Gateway Documentation](../README.md) for more information
- Explore example applications in the `examples/` directory
- Join the [AI Gateway community](https://github.com/envoyproxy/ai-gateway)

## Additional Resources

- [Kubernetes Client-Go Documentation](https://github.com/kubernetes/client-go)
- [Controller Runtime Documentation](https://github.com/kubernetes-sigs/controller-runtime)
- [Envoy Gateway Documentation](https://gateway.envoyproxy.io)
- [Gateway API Documentation](https://gateway-api.sigs.k8s.io)
