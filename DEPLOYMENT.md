# AI Gateway Deployment Guide

Complete step-by-step guide for deploying Envoy AI Gateway with custom images on Kubernetes (K3s).

## Prerequisites

### Required Tools
- Kubernetes cluster (K3s/K8s)
- Docker
- kubectl
- Helm

### Verify Prerequisites

```bash
kubectl version

kubectl get nodes

kubectl get pods -n envoy-gateway-system

docker --version
```

Expected output:
- Kubernetes cluster running
- Envoy Gateway installed
- Docker daemon running

---

## Step 1: Build Custom Docker Images

### Build Images from Source

```bash
cd /path/to/ai-gateway

make docker-build TAG=custom-v1 DOCKER_BUILD_ARGS="--load"
```

### Verify Built Images

```bash
docker images | grep ai-gateway
```

Expected output:
```
envoyproxy/ai-gateway-controller   custom-v1   ...
envoyproxy/ai-gateway-extproc      custom-v1   ...
```

### Diagnostic: Check Image Build Status

```bash
docker images --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}\t{{.CreatedAt}}"
```

---

## Step 2: Import Images to K3s (For K3s Clusters)

K3s uses containerd instead of Docker, so images must be imported.

### Import Controller Image

```bash
docker save envoyproxy/ai-gateway-controller:custom-v1 | sudo k3s ctr images import -
```

### Import ExtProc Image

```bash
docker save envoyproxy/ai-gateway-extproc:custom-v1 | sudo k3s ctr images import -
```

### Verify Images in K3s

```bash
sudo k3s ctr images ls | grep ai-gateway
```

Expected output:
```
docker.io/envoyproxy/ai-gateway-controller:custom-v1
docker.io/envoyproxy/ai-gateway-extproc:custom-v1
```

### Diagnostic: Check Image Import Issues

```bash
sudo k3s ctr images ls -q | grep envoyproxy

docker images --format "{{.Repository}}:{{.Tag}}" | grep ai-gateway
```

---

## Step 3: Remove Webhook (If Blocking Deployment)

### Check Existing Webhooks

```bash
kubectl get mutatingwebhookconfigurations

kubectl get validatingwebhookconfigurations
```

### Remove AI Gateway Webhook (If Present)

```bash
kubectl delete mutatingwebhookconfigurations envoy-ai-gateway-gateway-pod-mutator.envoy-ai-gateway-system
```

### Diagnostic: Check Webhook Errors

```bash
kubectl get events -n envoy-gateway-system --sort-by='.lastTimestamp' | tail -20

kubectl describe replicaset -n envoy-gateway-system | grep -A 5 "Error"
```

---

## Step 4: Install/Upgrade AI Gateway with Custom Images

### Check Existing Installation

```bash
helm list -A

kubectl get deployment -A | grep ai-gateway
```

### Install CRDs (If Not Present)

```bash
helm install ai-gateway-crds /path/to/ai-gateway/manifests/charts/ai-gateway-crds-helm \
  --namespace envoy-ai-gateway-system \
  --create-namespace
```

### Install or Upgrade AI Gateway

```bash
helm upgrade --install ai-gateway /path/to/ai-gateway/manifests/charts/ai-gateway-helm \
  --namespace envoy-ai-gateway-system \
  --create-namespace \
  --set extProc.image.repository=envoyproxy/ai-gateway-extproc \
  --set extProc.image.tag=custom-v1 \
  --set extProc.imagePullPolicy=Never \
  --set controller.image.repository=envoyproxy/ai-gateway-controller \
  --set controller.image.tag=custom-v1 \
  --set controller.imagePullPolicy=Never
```

### Verify Deployment

```bash
kubectl get pods -n envoy-ai-gateway-system

kubectl get deployment -n envoy-ai-gateway-system
```

Expected output:
```
NAME                        READY   STATUS    RESTARTS   AGE
ai-gateway-controller-xxx   1/1     Running   0          30s
```

### Diagnostic: Troubleshoot Pod Issues

```bash
kubectl describe pod -n envoy-ai-gateway-system -l app.kubernetes.io/name=ai-gateway-controller

kubectl logs -n envoy-ai-gateway-system -l app.kubernetes.io/name=ai-gateway-controller --tail=50

kubectl get events -n envoy-ai-gateway-system --field-selector involvedObject.kind=Pod
```

Common issues:
- **ImagePullBackOff**: Image not imported to K3s
- **ErrImageNeverPull**: Image doesn't exist locally
- **CrashLoopBackOff**: Check logs for application errors

---

## Step 5: Deploy Gateway and Routes

### Deploy Basic Example

```bash
kubectl apply -f /path/to/ai-gateway/examples/basic/basic.yaml
```

### Verify Gateway Resources

```bash
kubectl get gateway -n default

kubectl get aigatewayroute -n default

kubectl get aiservicebackend -n default
```

Expected output:
```
NAME                     STATUS
envoy-ai-gateway-basic   Accepted
```

### Check Envoy Proxy Deployment

```bash
kubectl get pods -n envoy-gateway-system

kubectl get deployment -n envoy-gateway-system | grep envoy-default
```

Expected: Envoy proxy pod with 3/3 containers (envoy, shutdown-manager, ai-gateway-extproc)

### Diagnostic: Envoy Proxy Not Ready

```bash
kubectl describe pod -n envoy-gateway-system -l app.kubernetes.io/name=envoy

kubectl logs -n envoy-gateway-system -l app.kubernetes.io/name=envoy -c envoy --tail=50

kubectl logs -n envoy-gateway-system -l app.kubernetes.io/name=envoy -c ai-gateway-extproc --tail=50
```

Check for:
- Configuration timeouts
- ExtProc connection issues
- Missing routes

---

## Step 6: Deploy OpenAI Provider

### Prepare OpenAI Configuration

Edit the OpenAI example file and replace the API key:

```bash
cp /path/to/ai-gateway/examples/basic/openai.yaml /tmp/openai-custom.yaml

sed -i 's/OPENAI_API_KEY/sk-your-actual-api-key-here/' /tmp/openai-custom.yaml
```

### Deploy OpenAI Provider

```bash
kubectl apply -f /tmp/openai-custom.yaml
```

### Verify OpenAI Resources

```bash
kubectl get aigatewayroute -n default

kubectl get aiservicebackend -n default

kubectl get backendsecuritypolicy -n default

kubectl get secret envoy-ai-gateway-basic-openai-apikey -n default
```

Expected output:
```
NAME                            STATUS
envoy-ai-gateway-basic-openai   Accepted
```

### Diagnostic: OpenAI Configuration Issues

```bash
kubectl describe aigatewayroute envoy-ai-gateway-basic-openai -n default

kubectl describe aiservicebackend envoy-ai-gateway-basic-openai -n default

kubectl logs -n envoy-ai-gateway-system -l app.kubernetes.io/name=ai-gateway-controller --tail=30
```

---

## Step 7: Get Gateway Endpoint

### Find Gateway Service

```bash
kubectl get svc -n envoy-gateway-system | grep envoy-default
```

### Get External IP/LoadBalancer

```bash
kubectl get gateway envoy-ai-gateway-basic -n default -o jsonpath='{.status.addresses[0].value}'
```

Or:

```bash
GATEWAY_IP=$(kubectl get svc -n envoy-gateway-system -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

echo "Gateway endpoint: http://${GATEWAY_IP}"
```

### Diagnostic: No External IP

```bash
kubectl describe svc -n envoy-gateway-system -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic

kubectl get svc -n envoy-gateway-system -o yaml
```

For K3s with Traefik/ServiceLB:
```bash
kubectl get svc -n kube-system | grep svclb
```

---

## Step 8: Test Deployment

### Test Basic Route (Test Upstream)

```bash
GATEWAY_IP=<your-gateway-ip>

curl -X POST http://${GATEWAY_IP}/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "x-ai-eg-model: some-cool-self-hosted-model" \
  -d '{
    "model": "test",
    "messages": [{"role": "user", "content": "test"}]
  }'
```

### Test OpenAI Integration

```bash
curl -X POST http://${GATEWAY_IP}/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "x-ai-eg-model: gpt-4o-mini" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {
        "role": "user",
        "content": "Hello, how are you?"
      }
    ]
  }'
```

### Test Streaming (SSE)

```bash
curl -X POST http://${GATEWAY_IP}/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "x-ai-eg-model: gpt-4o-mini" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {
        "role": "user",
        "content": "Count from 1 to 5"
      }
    ],
    "stream": true
  }' \
  -N
```

### Test Audio/TTS Endpoint

First, add a TTS route:

```bash
cat << 'EOF' | kubectl apply -f -
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: envoy-ai-gateway-basic-openai-tts
  namespace: default
spec:
  parentRefs:
    - name: envoy-ai-gateway-basic
      kind: Gateway
      group: gateway.networking.k8s.io
  rules:
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: gpt-4o-mini-tts
      backendRefs:
        - name: envoy-ai-gateway-basic-openai
EOF
```

Test TTS:

```bash
curl -X POST http://${GATEWAY_IP}/v1/audio/speech \
  -H "Content-Type: application/json" \
  -H "x-ai-eg-model: gpt-4o-mini-tts" \
  -d '{
    "model": "tts-1",
    "input": "Hello, this is a test of text to speech API.",
    "voice": "alloy",
    "response_format": "wav"
  }' \
  --output /tmp/test-audio.wav

file /tmp/test-audio.wav

ls -lh /tmp/test-audio.wav
```

---

## Diagnostic Commands Reference

### Check Overall Status

```bash
kubectl get all -n envoy-ai-gateway-system

kubectl get all -n envoy-gateway-system

kubectl get aigatewayroute,aiservicebackend,backendsecuritypolicy -n default
```

### View Logs

```bash
kubectl logs -n envoy-ai-gateway-system deployment/ai-gateway-controller --tail=100

kubectl logs -n envoy-gateway-system deployment/envoy-gateway --tail=100

kubectl logs -n envoy-gateway-system -l app.kubernetes.io/name=envoy -c ai-gateway-extproc --tail=100

kubectl logs -n envoy-gateway-system -l app.kubernetes.io/name=envoy -c envoy --tail=100
```

### Check Events

```bash
kubectl get events -n envoy-ai-gateway-system --sort-by='.lastTimestamp'

kubectl get events -n envoy-gateway-system --sort-by='.lastTimestamp'

kubectl get events -n default --sort-by='.lastTimestamp'
```

### Describe Resources

```bash
kubectl describe gateway envoy-ai-gateway-basic -n default

kubectl describe aigatewayroute -n default

kubectl describe pod -n envoy-gateway-system -l app.kubernetes.io/name=envoy
```

### Check Configuration

```bash
kubectl get configmap -n envoy-gateway-system

kubectl get secret -n default

kubectl get httproute -n default
```

### Network Debugging

```bash
kubectl get svc -A

kubectl get endpoints -n envoy-gateway-system

kubectl get endpoints -n default
```

### ExtProc Admin Interface

```bash
kubectl port-forward -n envoy-gateway-system <envoy-pod-name> 1064:1064

curl http://localhost:1064/healthz

curl http://localhost:1064/config
```

---

## Troubleshooting Guide

### Issue: Controller Pod Not Starting

**Symptoms:**
- Pod in `ImagePullBackOff` or `ErrImageNeverPull`

**Diagnosis:**
```bash
kubectl describe pod -n envoy-ai-gateway-system -l app.kubernetes.io/name=ai-gateway-controller

docker images | grep ai-gateway-controller

sudo k3s ctr images ls | grep ai-gateway-controller
```

**Solution:**
1. Verify image exists in Docker
2. Import to K3s: `docker save envoyproxy/ai-gateway-controller:custom-v1 | sudo k3s ctr images import -`
3. Set `imagePullPolicy: Never` in Helm values

### Issue: Envoy Proxy Stuck at 1/2 or 2/3 Running

**Symptoms:**
- Envoy proxy containers not all ready
- Startup probe failures

**Diagnosis:**
```bash
kubectl describe pod -n envoy-gateway-system -l app.kubernetes.io/name=envoy

kubectl logs -n envoy-gateway-system -l app.kubernetes.io/name=envoy -c envoy --tail=50

kubectl get aigatewayroute -n default
```

**Solutions:**
- Check if AIGatewayRoute exists
- Verify controller is running and connected
- Check envoy logs for configuration timeout errors

### Issue: No Routes Accepted

**Symptoms:**
- AIGatewayRoute status is not "Accepted"

**Diagnosis:**
```bash
kubectl describe aigatewayroute <route-name> -n default

kubectl logs -n envoy-ai-gateway-system deployment/ai-gateway-controller --tail=50
```

**Solutions:**
- Check parent Gateway exists and is accepted
- Verify backend references are correct
- Check controller logs for reconciliation errors

### Issue: API Requests Return 404

**Symptoms:**
- curl returns 404 Not Found

**Diagnosis:**
```bash
kubectl get httproute -n default

kubectl logs -n envoy-gateway-system -l app.kubernetes.io/name=envoy -c envoy --tail=50

kubectl logs -n envoy-gateway-system -l app.kubernetes.io/name=envoy -c ai-gateway-extproc --tail=50
```

**Solutions:**
- Verify `x-ai-eg-model` header matches route configuration
- Check HTTPRoute exists and is attached to Gateway
- Verify backend endpoints are reachable

### Issue: ExtProc Not Injected

**Symptoms:**
- Envoy pod has only 2/2 containers instead of 3/3

**Diagnosis:**
```bash
kubectl get pods -n envoy-gateway-system -o jsonpath='{.items[*].spec.containers[*].name}'

kubectl describe gateway envoy-ai-gateway-basic -n default

kubectl logs -n envoy-ai-gateway-system deployment/ai-gateway-controller | grep "mutating gateway pod"
```

**Solutions:**
- Ensure AIGatewayRoute or MCPRoute is attached to Gateway
- Check controller logs for mutation errors
- Verify controller is running and healthy

---

## Configuration Files Location

```
/tmp/ai-gateway/
├── examples/
│   ├── basic/
│   │   ├── basic.yaml          # Basic gateway + test upstream
│   │   ├── openai.yaml         # OpenAI provider config
│   │   ├── anthropic.yaml      # Anthropic provider config
│   │   ├── aws.yaml            # AWS Bedrock config
│   │   └── azure_openai.yaml   # Azure OpenAI config
│   └── ...
└── manifests/
    └── charts/
        ├── ai-gateway-crds-helm/  # CRD definitions
        └── ai-gateway-helm/       # Main Helm chart
```

---

## Useful kubectl Aliases

```bash
alias k='kubectl'
alias kgp='kubectl get pods'
alias kgs='kubectl get svc'
alias kgn='kubectl get nodes'
alias kdp='kubectl describe pod'
alias kl='kubectl logs'
alias kaf='kubectl apply -f'
alias kdf='kubectl delete -f'
```

---

## Next Steps

1. **Configure Monitoring**: Set up metrics collection from ExtProc admin endpoint
2. **Add Rate Limiting**: Configure token-based rate limiting
3. **Enable Tracing**: Configure OpenTelemetry for distributed tracing
4. **Security Hardening**: Add network policies and RBAC
5. **Multi-Provider Setup**: Add more AI provider backends
6. **Production Deployment**: Configure resource limits, HPA, and PDB

---

## Additional Resources

- [AI Gateway Documentation](https://aigateway.envoyproxy.io/docs)
- [Envoy Gateway Documentation](https://gateway.envoyproxy.io)
- [Gateway API Specification](https://gateway-api.sigs.k8s.io)

---

## Version Information

This deployment guide is for:
- AI Gateway: Custom build (custom-v1)
- Kubernetes: v1.33+
- Envoy Gateway: latest
- Helm: v3.x



