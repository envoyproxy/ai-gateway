#!/usr/bin/env bash
# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

set -o errexit
set -o nounset
set -o pipefail

# Set working directory to repo root
SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
cd "${SCRIPT_ROOT}"

# Module name
MODULE="github.com/envoyproxy/ai-gateway"

# Output directory for generated code
OUTPUT_PKG="${MODULE}/pkg/client"

# API group and version
APIS_PKG="${MODULE}/api"

# Cleanup old generated code (but preserve tests and README)
echo "Cleaning up old generated client code..."
if [ -d "./pkg/client/tests" ]; then
    mv ./pkg/client/tests /tmp/client-tests-backup
fi
if [ -f "./pkg/client/README.md" ]; then
    mv ./pkg/client/README.md /tmp/client-readme-backup
fi
rm -rf ./pkg/client
mkdir -p ./pkg/client
if [ -d "/tmp/client-tests-backup" ]; then
    mv /tmp/client-tests-backup ./pkg/client/tests
fi
if [ -f "/tmp/client-readme-backup" ]; then
    mv /tmp/client-readme-backup ./pkg/client/README.md
fi

# Get the go tool path
GO_TOOL="go tool -modfile=tools/go.mod"

echo "Generating clientset..."
${GO_TOOL} client-gen \
  --go-header-file=./hack/boilerplate.go.txt \
  --clientset-name="versioned" \
  --input-base="" \
  --input="${MODULE}/api/v1alpha1" \
  --output-dir="./pkg/client/clientset" \
  --output-pkg="${MODULE}/pkg/client/clientset" \
  --plural-exceptions="BackendSecurityPolicy:BackendSecurityPolicies"

echo "Generating listers..."
${GO_TOOL} lister-gen \
  --go-header-file=./hack/boilerplate.go.txt \
  --output-dir="./pkg/client/listers" \
  --output-pkg="${MODULE}/pkg/client/listers" \
  --plural-exceptions="BackendSecurityPolicy:BackendSecurityPolicies" \
  "${MODULE}/api/v1alpha1"

echo "Generating informers..."
${GO_TOOL} informer-gen \
  --go-header-file=./hack/boilerplate.go.txt \
  --versioned-clientset-package="${MODULE}/pkg/client/clientset/versioned" \
  --listers-package="${MODULE}/pkg/client/listers" \
  --output-dir="./pkg/client/informers" \
  --output-pkg="${MODULE}/pkg/client/informers" \
  --plural-exceptions="BackendSecurityPolicy:BackendSecurityPolicies" \
  "${MODULE}/api/v1alpha1"

echo "Code generation complete!"

