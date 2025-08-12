# Envoy AI Gateway Helm Chart

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)][license]

This chart deploys the [Envoy AI Gateway][ai-gateway] controller on a
[Kubernetes][k8s] cluster using the [Helm][helm] package manager.

## Prerequisites

- Kubernetes 1.29+
- kubectl
- Helm 3.0+
- [Envoy Gateway][envoy-gateway] v1.3.0+ (will be installed if not present)

## Installing the Chart

```bash
# Install the CRDs
helm install aieg-crd oci://docker.io/envoyproxy/ai-gateway-crds-helm \
  --version v0.0.0-latest \
  --namespace envoy-ai-gateway-system \
  --create-namespace

# Install the AI Gateway controller
helm install aieg oci://docker.io/envoyproxy/ai-gateway-helm \
  --version v0.0.0-latest \
  --namespace envoy-ai-gateway-system
```

## Uninstalling the Chart

To uninstall/delete the deployment:

```bash
helm uninstall aieg -n envoy-ai-gateway-system
helm uninstall aieg-crd -n envoy-ai-gateway-system
```

## Configuration

The following table lists the configurable parameters of the AI Gateway chart
and their default values.

### Global Parameters

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `nameOverride` | string | `""` | Override the name of the chart |
| `fullnameOverride` | string | `""` | Override the fullname of the chart |

### Controller Configuration

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `controller.logLevel` | string | `"info"` | Log level for the controller (info, debug, trace, warn, error, fatal, panic) |
| `controller.fullnameOverride` | string | `"ai-gateway-controller"` | Override the fullname for the controller |
| `controller.metricsRequestHeaderLabels` | string | `""` | Comma-separated key-value pairs for mapping HTTP request headers to Prometheus metric labels. Format: "header1:label1,header2:label2" |
| `controller.leaderElection.enabled` | bool | `true` | Enable leader election for controller Manager for protecting against split brain |
| `controller.image.repository` | string | `"docker.io/envoyproxy/ai-gateway-controller"` | Controller image repository |
| `controller.image.tag` | string | `""` | Overrides the image tag (default is the chart appVersion) |
| `controller.imagePullPolicy` | string | `"IfNotPresent"` | Image pull policy |
| `controller.replicaCount` | int | `1` | Number of controller replicas |
| `controller.imagePullSecrets` | list | `[]` | Image pull secrets |
| `controller.podAnnotations` | object | `{}` | Annotations for controller pods |
| `controller.podSecurityContext` | object | `{}` | Security context for controller pods |
| `controller.securityContext` | object | `{}` | Security context for controller containers |
| `controller.podEnv` | object | `{}` | Environment variables for controller pods |
| `controller.extraEnvVars` | list | `[]` | Extra environment variables for controller containers |
| `controller.volumes` | list | `[]` | Additional volumes for controller pods |
| `controller.resources` | object | `{}` | Resource limits and requests for controller containers |
| `controller.nodeSelector` | object | `{}` | Node selector for controller pods |
| `controller.tolerations` | list | `[]` | Tolerations for controller pods |
| `controller.affinity` | object | `{}` | Affinity rules for controller pods |

### Service Account Configuration

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `controller.serviceAccount.create` | bool | `true` | Specifies whether a service account should be created |
| `controller.serviceAccount.annotations` | object | `{}` | Annotations to add to the service account |
| `controller.serviceAccount.name` | string | `""` | The name of the service account to use (generated if not set) |

### Service Configuration

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `controller.service.type` | string | `"ClusterIP"` | Service type |
| `controller.service.ports` | list | See values.yaml | Service ports configuration |

### Webhook Configuration

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `controller.webhookService.type` | string | `"ClusterIP"` | Webhook service type |
| `controller.webhookService.ports` | list | See values.yaml | Webhook service ports |

### ExtProc Configuration

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `extProc.image.repository` | string | `"docker.io/envoyproxy/ai-gateway-extproc"` | ExtProc sidecar image repository |
| `extProc.image.tag` | string | `""` | Overrides the image tag (default is the chart appVersion) |
| `extProc.imagePullPolicy` | string | `"IfNotPresent"` | Image pull policy |
| `extProc.logLevel` | string | `"info"` | Log level for extProc (info, debug, trace, warn, error, fatal, panic) |
| `extProc.extraEnvVars` | list | `[]` | Extra environment variables for extProc containers |

## Examples

### Basic Installation

```bash
helm install aieg oci://docker.io/envoyproxy/ai-gateway-helm \
  --version v0.0.0-latest \
  --namespace envoy-ai-gateway-system
```

### OpenTelemetry with Arize Phoenix

Deploy AI Gateway with OpenTelemetry tracing visualized by [Arize Phoenix][phoenix],
an open-source LLM observability platform with [OpenInference semantic
conventions][openinference].

1. **Install Phoenix for LLM observability**:
```bash
# Install Phoenix (check latest version at hub.docker.com)
helm install phoenix oci://registry-1.docker.io/arizephoenix/phoenix-helm \
  --version 0.1.0 \
  --namespace envoy-ai-gateway-system \
  --set server.port=6006
```

2. **Configure AI Gateway with OpenTelemetry**:

Create `values.yaml`:
```yaml
extProc:
  extraEnvVars:
    # OTEL_SERVICE_NAME defaults to "ai-gateway" if not set
    - name: OTEL_EXPORTER_OTLP_ENDPOINT
      value: "http://phoenix:6006"
```

3. **Install AI Gateway with tracing**:
```bash
helm upgrade -i aieg oci://docker.io/envoyproxy/ai-gateway-helm \
  --version v0.0.0-latest \
  --namespace envoy-ai-gateway-system \
  -f values.yaml
```

4. **Access Phoenix UI**:
```bash
# Port-forward to access Phoenix
kubectl port-forward -n envoy-ai-gateway-system svc/phoenix 6006:6006

# Open Phoenix UI
open http://localhost:6006
```

5. **Verify traces are being collected**:
```bash
# Check Phoenix logs
kubectl logs -n envoy-ai-gateway-system deployment/phoenix | \
  grep "POST /v1/traces"

# Check extproc logs for OTEL configuration
kubectl logs -n envoy-gateway-system <envoy-pod> -c ai-gateway-extproc | \
  grep OTEL
```

## Contributing

Please see the [contributing guide][contributing] for more information.

## License

This chart is licensed under the Apache 2.0 License. See the [LICENSE][license]
file for details.

[ai-gateway]: https://aigateway.envoyproxy.io/
[contributing]: https://github.com/envoyproxy/ai-gateway/blob/main/CONTRIBUTING.md
[envoy-gateway]: https://gateway.envoyproxy.io/
[helm]: https://helm.sh
[k8s]: https://kubernetes.io
[license]: https://github.com/envoyproxy/ai-gateway/blob/main/LICENSE
[openinference]: https://github.com/Arize-ai/openinference
[phoenix]: https://phoenix.arize.com
