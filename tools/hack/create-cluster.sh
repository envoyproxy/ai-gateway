#!/usr/bin/env bash

set -euo pipefail

# Setup default values (match e2e constants)
CLUSTER_NAME=${CLUSTER_NAME:-"envoy-ai-gateway"}
METALLB_VERSION=${METALLB_VERSION:-"v0.13.10"}

## Check if kind cluster already exists.
if go tool kind get clusters | grep -q "${CLUSTER_NAME}"; then
  echo "Cluster ${CLUSTER_NAME} already exists."
else
  echo "Creating kind cluster ${CLUSTER_NAME}"
  # Use simple kind create (like e2e) instead of complex config
  go tool kind create cluster --name "${CLUSTER_NAME}"
fi

# Export kubeconfig (like e2e)
echo "Switching kubectl context to ${CLUSTER_NAME}"
go tool kind export kubeconfig --name "${CLUSTER_NAME}"

## Load Docker images into kind cluster (exactly like e2e)
echo "Loading Docker images into kind cluster"
for image in \
  "docker.io/envoyproxy/ai-gateway-controller:latest" \
  "docker.io/envoyproxy/ai-gateway-extproc:latest" \
  "docker.io/envoyproxy/ai-gateway-testupstream:latest"; do
  go tool kind load docker-image "${image}" --name "${CLUSTER_NAME}"
done

## Install MetalLB (exactly like e2e)
echo "Installing MetalLB"
echo "Applying MetalLB manifests"
kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/"${METALLB_VERSION}"/config/manifests/metallb-native.yaml

# Create memberlist secret if it doesn't exist (exactly like e2e)
echo "Creating memberlist secret if needed"
needCreate="$(kubectl get secret -n metallb-system memberlist --no-headers --ignore-not-found -o custom-columns=NAME:.metadata.name)"
if [ -z "$needCreate" ]; then
    kubectl create secret generic -n metallb-system memberlist --from-literal=secretkey="$(openssl rand -base64 128)"
fi

# Wait for MetalLB deployments (exactly like e2e)
echo "Waiting for MetalLB controller deployment to be ready"
kubectl wait --timeout=2m -n metallb-system deployment/controller --for=condition=Available

echo "Waiting for MetalLB speaker daemonset to be ready"
kubectl wait --timeout=2m -n metallb-system daemonset/speaker --for=create
kubectl wait --timeout=2m -n metallb-system daemonset/speaker --for=jsonpath='{.status.numberReady}'=1

# Configure IP address pools (simplified but matches e2e logic)
echo "Configuring IP address pools"
subnet_v4=$(docker network inspect kind | jq -r '.[].IPAM.Config[] | select(.Subnet | contains(":") | not) | .Subnet')
address_prefix_v4=$(echo "${subnet_v4}" | awk -F. '{print $1"."$2"."$3}')
address_range_v4="${address_prefix_v4}.200-${address_prefix_v4}.250"

kubectl apply -f - <<EOF
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  namespace: metallb-system
  name: kube-services
spec:
  addresses:
  - ${address_range_v4}
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: kube-services
  namespace: metallb-system
spec:
  ipAddressPools:
  - kube-services
EOF

echo "Kind cluster setup complete!" 