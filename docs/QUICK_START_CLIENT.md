# AI Gateway Client - Quick Start Guide

This is a quick reference for deploying and testing the AI Gateway Kubernetes client on a cluster.

## 5-Minute Setup

### Step 1: Install Prerequisites

```bash
# Install Envoy Gateway
helm install eg oci://docker.io/envoyproxy/gateway-helm \
  --version v1.2.4 \
  --namespace envoy-gateway-system \
  --create-namespace

# Install AI Gateway CRDs
kubectl apply -f https://github.com/envoyproxy/ai-gateway/releases/latest/download/ai-gateway-crds.yaml
```

### Step 2: Create Test Resources

```bash
# Create a test namespace
kubectl create namespace ai-gateway-demo

# Create an Envoy Backend
kubectl apply -f - <<EOF
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: Backend
metadata:
  name: openai-backend
  namespace: ai-gateway-demo
spec:
  endpoints:
  - fqdn:
      hostname: api.openai.com
      port: 443
  appProtocols:
  - HTTPS
EOF

# Create an AIServiceBackend
kubectl apply -f - <<EOF
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: openai-backend
  namespace: ai-gateway-demo
spec:
  schema:
    name: OpenAI
  backendRef:
    name: openai-backend
    group: gateway.envoyproxy.io
    kind: Backend
EOF

# Create an AIGatewayRoute
kubectl apply -f - <<EOF
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: demo-route
  namespace: ai-gateway-demo
spec:
  rules:
  - backendRefs:
    - name: openai-backend
EOF
```

### Step 3: Test the Client

Create a simple test program:

```bash
cat > test-client.go <<'EOF'
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
        log.Fatalf("Error loading kubeconfig: %v", err)
    }

    // Create client
    client, err := clientset.NewForConfig(config)
    if err != nil {
        log.Fatalf("Error creating client: %v", err)
    }

    ctx := context.Background()

    // List AIGatewayRoutes
    routes, err := client.AigatewayV1alpha1().AIGatewayRoutes("ai-gateway-demo").List(
        ctx, metav1.ListOptions{})
    if err != nil {
        log.Fatalf("Error listing routes: %v", err)
    }
    
    fmt.Printf("✓ Found %d AIGatewayRoute(s):\n", len(routes.Items))
    for _, route := range routes.Items {
        fmt.Printf("  - %s (rules: %d)\n", route.Name, len(route.Spec.Rules))
    }

    // List AIServiceBackends
    backends, err := client.AigatewayV1alpha1().AIServiceBackends("ai-gateway-demo").List(
        ctx, metav1.ListOptions{})
    if err != nil {
        log.Fatalf("Error listing backends: %v", err)
    }
    
    fmt.Printf("✓ Found %d AIServiceBackend(s):\n", len(backends.Items))
    for _, backend := range backends.Items {
        fmt.Printf("  - %s (schema: %s)\n", backend.Name, backend.Spec.APISchema.Name)
    }

    fmt.Println("\n✓ Client test successful!")
}
EOF

# Run the test
go mod init test-client
go get github.com/envoyproxy/ai-gateway@latest
go run test-client.go
```

Expected output:
```
✓ Found 1 AIGatewayRoute(s):
  - demo-route (rules: 1)
✓ Found 1 AIServiceBackend(s):
  - openai-backend (schema: OpenAI)

✓ Client test successful!
```

### Step 4: Watch for Changes

```bash
cat > watch-resources.go <<'EOF'
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
    config, _ := clientcmd.BuildConfigFromFlags("", 
        clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename())
    client, _ := clientset.NewForConfig(config)

    factory := informers.NewSharedInformerFactory(client, 30*time.Second)
    routeInformer := factory.Aigateway().V1alpha1().AIGatewayRoutes()

    routeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
        AddFunc: func(obj interface{}) {
            route := obj.(*v1alpha1.AIGatewayRoute)
            fmt.Printf("➕ Route added: %s/%s\n", route.Namespace, route.Name)
        },
        UpdateFunc: func(oldObj, newObj interface{}) {
            route := newObj.(*v1alpha1.AIGatewayRoute)
            fmt.Printf("✏️  Route updated: %s/%s\n", route.Namespace, route.Name)
        },
        DeleteFunc: func(obj interface{}) {
            route := obj.(*v1alpha1.AIGatewayRoute)
            fmt.Printf("🗑️  Route deleted: %s/%s\n", route.Namespace, route.Name)
        },
    })

    ctx := context.Background()
    factory.Start(ctx.Done())
    factory.WaitForCacheSync(ctx.Done())

    fmt.Println("👀 Watching for AIGatewayRoute changes. Press Ctrl+C to exit...")
    <-ctx.Done()
}
EOF

# Run the watcher in background
go run watch-resources.go &

# Make a change to trigger an event
kubectl label aigatewayroute demo-route -n ai-gateway-demo test=update

# You should see: ✏️  Route updated: ai-gateway-demo/demo-route
```

## Common Operations

### List All Resources

```bash
kubectl get aigatewayroutes -A
kubectl get aiservicebackends -A
kubectl get backendsecuritypolicies -A
kubectl get mcproutes -A
```

### Get Resource Details

```bash
kubectl describe aigatewayroute demo-route -n ai-gateway-demo
kubectl get aiservicebackend openai-backend -n ai-gateway-demo -o yaml
```

### Update a Resource

```bash
kubectl patch aigatewayroute demo-route -n ai-gateway-demo \
  --type=json -p='[{"op": "add", "path": "/metadata/labels/env", "value": "demo"}]'
```

### Delete Resources

```bash
kubectl delete aigatewayroute demo-route -n ai-gateway-demo
kubectl delete aiservicebackend openai-backend -n ai-gateway-demo
```

## Cleanup

```bash
# Delete test namespace
kubectl delete namespace ai-gateway-demo

# Delete test files
rm -f test-client.go watch-resources.go go.mod go.sum
```

## Next Steps

1. **Read Full Documentation**
   - [Client Usage Guide](client-usage.md) - Detailed examples
   - [Deployment Guide](client-deployment-guide.md) - Complete deployment instructions

2. **Explore Examples**
   - Check `examples/` directory for more samples
   - Review test files in `pkg/client/` for usage patterns

3. **Build Your Application**
   - Use the client in your Go applications
   - Implement controllers or operators
   - Create CLI tools

## Troubleshooting

### Issue: Cannot connect to cluster

```bash
# Verify kubectl works
kubectl cluster-info

# Check kubeconfig
echo $KUBECONFIG
kubectl config view
```

### Issue: CRDs not found

```bash
# Reinstall CRDs
kubectl apply -f https://github.com/envoyproxy/ai-gateway/releases/latest/download/ai-gateway-crds.yaml

# Verify installation
kubectl get crds | grep aigateway
```

### Issue: Import errors

```bash
# Update dependencies
go get github.com/envoyproxy/ai-gateway@latest
go mod tidy
```

## Quick Reference

### Import Paths

```go
import (
    "github.com/envoyproxy/ai-gateway/api/v1alpha1"
    clientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned"
    informers "github.com/envoyproxy/ai-gateway/pkg/client/informers/externalversions"
    listers "github.com/envoyproxy/ai-gateway/pkg/client/listers/api/v1alpha1"
)
```

### Create Client

```go
config, _ := clientcmd.BuildConfigFromFlags("", kubeconfig)
client, _ := clientset.NewForConfig(config)
```

### CRUD Operations

```go
// Create
route, _ := client.AigatewayV1alpha1().AIGatewayRoutes(ns).Create(ctx, route, metav1.CreateOptions{})

// Get
route, _ := client.AigatewayV1alpha1().AIGatewayRoutes(ns).Get(ctx, name, metav1.GetOptions{})

// List
routes, _ := client.AigatewayV1alpha1().AIGatewayRoutes(ns).List(ctx, metav1.ListOptions{})

// Update
route, _ := client.AigatewayV1alpha1().AIGatewayRoutes(ns).Update(ctx, route, metav1.UpdateOptions{})

// Delete
_ = client.AigatewayV1alpha1().AIGatewayRoutes(ns).Delete(ctx, name, metav1.DeleteOptions{})
```

## Support

- 📖 [Full Documentation](client-usage.md)
- 🐛 [Report Issues](https://github.com/envoyproxy/ai-gateway/issues)
- 💬 [Community Chat](https://github.com/envoyproxy/ai-gateway/discussions)

