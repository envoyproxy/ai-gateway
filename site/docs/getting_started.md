---
id: getting_started
title: Getting Started with Envoy AI Gateway
---

## Pre-requisites

Envoy AI Gateway is built on top of Envoy Gateway. To get started, we assume that your kubeconfig is set up and you have a Kubernetes cluster running.
If you don't have a Kubernetes cluster, you can use [kind](https://kind.sigs.k8s.io/) to create a local cluster.

To install Envoy Gateway, you can follow the instructions in the [Envoy Gateway documentation](https://gateway.envoyproxy.io/latest/tasks/quickstart/#installation):

```
# Install the Envoy Gateway Helm chart.
helm upgrade -i eg oci://docker.io/envoyproxy/gateway-helm --version v0.0.0-latest -n envoy-gateway-system --create-namespace
# Wait for the deployment to be ready.
kubectl wait --timeout=2m -n envoy-gateway-system deployment/envoy-gateway --for=condition=Available
```

## Install Envoy AI Gateway

To install Envoy AI Gateway, the easiest way is to use the Helm chart like this:

```
# Install the AI Gateway Helm chart.
helm upgrade -i aieg oci://ghcr.io/envoyproxy/ai-gateway/ai-gateway-helm --version v0.0.0-latest -n envoy-ai-gateway-system --create-namespace
# Wait for the deployment to be created.
kubectl wait --timeout=2m -n envoy-ai-gateway-system deployment/ai-gateway-controller --for=create
# Wait for the deployment to be ready.
kubectl wait --timeout=2m -n envoy-ai-gateway-system deployment/ai-gateway-controller --for=condition=Available
```

In addition, we need to apply the AI Gateway specific configuration for the Envoy Gateway:

```
# Apply the Envoy Gateway configuration specific to AI Gateway.
kubectl apply -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/manifests/envoy-gateway-config/config.yaml
kubectl apply -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/manifests/envoy-gateway-config/rbac.yaml
# Restart the Envoy Gateway deployment.
kubectl rollout restart -n envoy-gateway-system deployment/envoy-gateway
# Wait for the deployment to be ready.
kubectl wait --timeout=2m -n envoy-gateway-system deployment/envoy-gateway --for=condition=Available
```

## Install basic AI Gateway setup

In the `examples/basic` directory, you can find a basic setup for the AI Gateway. To install it, you can run:

```
# Install the basic AI Gateway setup.
kubectl apply -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/examples/basic/basic.yaml
# Wait for the Gateway pod to be ready (it may take a few seconds).
kubectl wait pods --timeout=2m -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic -n envoy-gateway-system --for=condition=Ready
```

Now, let's make a request to the AI Gateway:

```
# Port-forward the AI Gateway service.
export ENVOY_SERVICE=$(kubectl get svc -n envoy-gateway-system --selector=gateway.envoyproxy.io/owning-gateway-namespace=default,gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic -o jsonpath='{.items[0].metadata.name}') && exec kubectl port-forward -n envoy-gateway-system svc/$ENVOY_SERVICE 8080:80
# Open a new terminal and run the following command to make a request to the AI Gateway.
curl --fail -H "Content-Type: application/json" -d '{"model":"some-cool-self-hosted-model","messages":[{"role":"system","content":"Hi."}]}' http://localhost:8080/v1/chat/completions
```

and you should see a response from the AI Gateway similar to this:

```
{"completions":[{"role":"system","content":"I am a chatbot."}]}
```

Note that the backend LLM selected for the model `some-cool-self-hosted-model` is a fake one,
so the response doesn't make much sense. To get a real response, you either need to deploy
a real backend by yourself or follow the instructions in the next section:

## (Optional) Accessing OpenAI and AWS Bedrock

The deployed example yaml in the `examples/basic` directory contains the configuration to access OpenAI and AWS Bedrock.
However, you need to provider the credentials for these services. To do so, you can download the manifest and replace
the variables `OPENAI_API_KEY`, `AWS_ACCESS_KEY_ID`, and `AWS_SECRET_ACCESS_KEY` with your own credentials.

First, let's delete the existing manifest:

```
# Delete the existing AI Gateway setup.
kubectl delete -f https://raw.githubusercontent.com/envoyproxy/ai-gateway/main/examples/basic/basic.yaml
# Wait for the Gateway pod to be deleted (it may take a few seconds).
kubectl wait pods --timeout=15s -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic -n envoy-gateway-system --for=delete
```

After replacing the variables, you can re-apply the manifest. These credentials are stored in Kubernetes secrets,
so to trigger the AI Gateway to reload the configuration, you have to add the annotation to AIGatewayRoute resource:

Then, you can make a request to the OpenAI and AWS Bedrock models.

```
# Wait for the Gateway pod to be ready (it may take a few seconds).
kubectl wait pods --timeout=2m -l gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic -n envoy-gateway-system --for=condition=Ready
# Port-forward the AI Gateway service.
ENVOY_SERVICE=$(kubectl get svc -n envoy-gateway-system --selector=gateway.envoyproxy.io/owning-gateway-namespace=default,gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic -o jsonpath='{.items[0].metadata.name}') && exec kubectl port-forward -n envoy-gateway-system svc/$ENVOY_SERVICE 8081:80
# Open a new terminal and run the following command to make a request to OpenAI and AWS Bedrock through the AI Gateway.
curl --fail -H "Content-Type: application/json" -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hi."}]}' http://localhost:8081/v1/chat/completions
curl --fail -H "Content-Type: application/json" -d '{"model":"us.meta.llama3-2-1b-instruct-v1:0","messages":[{"role":"user","content":"Hi."}]}' http://localhost:8081/v1/chat/completions
```
