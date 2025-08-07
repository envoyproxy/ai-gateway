# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

##@ Kubernetes: Targets for managing Kubernetes clusters and deployments

KIND_CLUSTER_NAME ?= envoy-ai-gateway
KUBE_NAMESPACE ?= envoy-ai-gateway-system
KUBE_WAIT_TIMEOUT ?= 2m
EG_VERSION ?= v0.0.0-latest

# Kind cluster management
.PHONY: create-cluster
create-cluster: ## Create a Kind cluster with same setup as e2e tests
	@echo "Setting up the kind cluster"
	@tools/hack/create-cluster.sh

.PHONY: delete-cluster
delete-cluster: ## Delete the Kind cluster
	@echo "Deleting Kind cluster: $(KIND_CLUSTER_NAME)"
	@kind delete cluster --name $(KIND_CLUSTER_NAME)
	@echo "Kind cluster deleted successfully"

# Load local images (for development)
.PHONY: load-local-images
load-local-images: build ## Load locally built images into Kind cluster
	@echo "Loading local Docker images into kind cluster"
	@kind load docker-image docker.io/envoyproxy/ai-gateway-controller:latest --name $(KIND_CLUSTER_NAME)
	@kind load docker-image docker.io/envoyproxy/ai-gateway-extproc:latest --name $(KIND_CLUSTER_NAME)
	@kind load docker-image docker.io/envoyproxy/ai-gateway-testupstream:latest --name $(KIND_CLUSTER_NAME)
	@echo "Local images loaded successfully"

# Envoy Gateway setup (mirrors initEnvoyGateway)
.PHONY: deploy-envoy-gateway
deploy-envoy-gateway: ## Deploy Envoy Gateway (same as e2e tests)
	@echo "Installing Envoy Gateway"
	@echo "Helm Install"
	@helm upgrade -i eg oci://docker.io/envoyproxy/gateway-helm --version $(EG_VERSION) -n envoy-gateway-system --create-namespace
	@echo "Applying Patch for Envoy Gateway"
	@kubectl apply -f manifests/envoy-gateway-config/
	@echo "Applying InferencePool Patch for Envoy Gateway"
	@kubectl apply -f examples/inference-pool/config.yaml
	@echo "Restart Envoy Gateway deployment"
	@kubectl rollout restart -n envoy-gateway-system deployment/envoy-gateway
	@echo "Waiting for Envoy Gateway deployment to be ready"
	@kubectl wait --timeout=$(KUBE_WAIT_TIMEOUT) -n envoy-gateway-system deployment/envoy-gateway --for=condition=Available
	@echo "Envoy Gateway deployed successfully"

# AI Gateway setup (mirrors initAIGateway)
.PHONY: deploy-ai-gateway
deploy-ai-gateway: ## Deploy AI Gateway (same as e2e tests)
	@echo "Installing AI Gateway"
	@echo "Helm Install CRDs"
	@helm upgrade -i ai-eg-crd manifests/charts/ai-gateway-crds-helm -n $(KUBE_NAMESPACE) --create-namespace
	@echo "Helm Install AI Gateway"
	@helm upgrade -i ai-eg manifests/charts/ai-gateway-helm \
		--set controller.metricsRequestHeaderLabels=x-user-id:user_id \
		-n $(KUBE_NAMESPACE) --create-namespace
	@echo "Restart AI Gateway controller"
	@kubectl rollout restart -n $(KUBE_NAMESPACE) deployment/ai-gateway-controller
	@kubectl wait --timeout=$(KUBE_WAIT_TIMEOUT) -n $(KUBE_NAMESPACE) deployment/ai-gateway-controller --for=condition=Available
	@echo "AI Gateway deployed successfully"

# Prometheus setup (mirrors initPrometheus)
.PHONY: deploy-prometheus
deploy-prometheus: ## Deploy Prometheus monitoring (same as e2e tests)
	@echo "Installing Prometheus"
	@echo "Applying manifests"
	@kubectl apply -f examples/monitoring/monitoring.yaml
	@echo "Waiting for deployment"
	@kubectl wait --timeout=$(KUBE_WAIT_TIMEOUT) -n monitoring deployment/prometheus --for=condition=Available
	@echo "Prometheus deployed successfully"

# Complete deployment workflow (mirrors TestMain sequence)
.PHONY: deploy-complete
deploy-complete: deploy-envoy-gateway deploy-ai-gateway deploy-prometheus ## Deploy complete stack (same as e2e tests)

# Development setup (build + deploy)
.PHONY: dev-setup
dev-setup: build create-cluster deploy-complete ## Complete development setup with local images

# Deploy with local images
.PHONY: deploy-with-local-images
deploy-with-local-images: load-local-images deploy-complete ## Deploy with locally built images

# Undeploy everything
.PHONY: undeploy-all
undeploy-all: ## Undeploy all components
	@echo "Undeploying all components"
	@kubectl delete -f examples/monitoring/monitoring.yaml --ignore-not-found || true
	@helm uninstall ai-eg -n $(KUBE_NAMESPACE) --ignore-not-found || true
	@helm uninstall ai-eg-crd -n $(KUBE_NAMESPACE) --ignore-not-found || true
	@helm uninstall eg -n envoy-gateway-system --ignore-not-found || true
	@kubectl delete namespace $(KUBE_NAMESPACE) --ignore-not-found || true
	@kubectl delete namespace envoy-gateway-system --ignore-not-found || true
	@kubectl delete namespace monitoring --ignore-not-found || true
	@echo "All components undeployed successfully"

# Complete cleanup
.PHONY: dev-cleanup
dev-cleanup: undeploy-all delete-cluster ## Complete development cleanup

# Main kube-deploy target (similar to Envoy Gateway)
.PHONY: kube-deploy
kube-deploy: create-cluster deploy-complete ## Deploy AI Gateway to Kind cluster

# Undeploy target (similar to Envoy Gateway)
.PHONY: kube-undeploy
kube-undeploy: undeploy-all delete-cluster ## Undeploy AI Gateway from cluster 