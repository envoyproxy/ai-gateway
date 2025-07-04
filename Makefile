# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

# Read any local configuration. This is an optional, local git-ignored file that can be used
# to set any value commonly used for development. This helps not having to set the overrides
# in the command line every time.
-include .makerc

# The Go-based tools are defined in Makefile.tools.mk.
include Makefile.tools.mk

# The list of commands that can be built.
COMMANDS := controller extproc

# This is the package that contains the version information for the build.
GIT_COMMIT:=$(shell git rev-parse HEAD)
VERSION_PACKAGE := github.com/envoyproxy/ai-gateway/internal/version
GO_LDFLAGS += -X $(VERSION_PACKAGE).Version=$(GIT_COMMIT)

# This is the directory where the built artifacts will be placed.
OUTPUT_DIR ?= out

# Arguments for docker builds.
OCI_REGISTRY ?= docker.io/envoyproxy
OCI_REPOSITORY_PREFIX ?= ${OCI_REGISTRY}/ai-gateway
TAG ?= latest
ENABLE_MULTI_PLATFORMS ?= false
HELM_CHART_VERSION ?= v0.0.0-latest

# Arguments for go test. This can be used, for example, to run specific tests via
# `GO_TEST_ARGS="-run TestName/foo/etc -v -race"`.
GO_TEST_ARGS ?= -race
# Arguments for go test in e2e tests in addition to GO_TEST_ARGS, applicable to test-e2e, test-extproc, and test-controller.
GO_TEST_E2E_ARGS ?= -count=1

## help: Show this help info.
.PHONY: help
help:
	@echo "Envoy AI Gateway is an Open Source project for using Envoy Gateway to handle request traffic from application clients to GenAI services.\n"
	@echo "Usage:\n  make \033[36m<Target>\033[0m \n\nTargets:"
	@awk 'BEGIN {FS = ":.*##"; printf ""} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Precommit: Usually, targets here do not need to be run individually, but run `make precommit` to run all of them at once. CI will fail if `precommit` is not run before committing.

# This runs all necessary steps to prepare for a commit.
.PHONY: precommit
precommit: ## Run all necessary steps to prepare for a commit.
precommit: tidy codespell apigen apidoc format lint editorconfig yamllint helm-test

.PHONY: lint
lint: ## This runs the linter, formatter, and tidy on the codebase.
	@echo "lint => ./..."
	@go tool golangci-lint run --build-tags==test_crdcel,test_controller,test_extproc ./...

.PHONY: codespell
CODESPELL_SKIP := $(shell cat .codespell.skip | tr \\n ',')
CODESPELL_IGNORE_WORDS := ".codespell.ignorewords"
codespell: $(CODESPELL) ## Spell check the codebase.
	@echo "spell => ./..."
	@$(CODESPELL) --skip $(CODESPELL_SKIP) --ignore-words $(CODESPELL_IGNORE_WORDS)

.PHONY: yamllint
yamllint: $(YAMLLINT) ## Lint yaml files.
	@echo "yamllint => ./..."
	@$(YAMLLINT) --config-file=.yamllint $$(git ls-files :*.yml :*.yaml | xargs -L1 dirname | sort -u)

# This runs the formatter on the codebase as well as goimports via gci.
.PHONY: format
format: ## Format the codebase.
	@echo "format => *.go"
	@find . -type f -name '*.go' | xargs gofmt -s -w
	@find . -type f -name '*.go' | xargs go tool gofumpt -l -w
	@echo "gci => *.go"
	@go tool gci write -s standard -s default -s "prefix(github.com/envoyproxy/ai-gateway)" `find . -name '*.go'`
	@echo "licenses => **"
	@go tool license-eye header fix

# This runs go mod tidy on every module.
.PHONY: tidy
tidy: ## Run go mod tidy on every module.
	@find . -name "go.mod" \
	| grep go.mod \
	| xargs -I {} bash -c 'dirname {}' \
	| xargs -I {} bash -c 'echo "tidy => {}"; cd {}; go mod tidy -v; '

# This runs precommit and checks for any differences in the codebase, failing if there are any.
.PHONY: check
check: precommit ## Run all necessary steps to prepare for a commit and check for any differences in the codebase.
	@if [ ! -z "`git status -s`" ]; then \
		echo "The following differences will fail CI until committed:"; \
		git diff --exit-code; \
		echo "Please ensure you have run 'make precommit' and committed the changes."; \
		exit 1; \
	fi

# This runs the editorconfig-checker on the codebase.
editorconfig:
	@echo "running editorconfig-checker"
	@go tool editorconfig-checker

# This re-generates the CRDs for the API defined in the api/v1alpha1 directory.
.PHONY: apigen
apigen: ## Generate CRDs for the API defined in the api directory.
	@echo "apigen => ./api/v1alpha1/..."
	@go tool controller-gen object crd paths="./api/v1alpha1/..." output:dir=./api/v1alpha1 output:crd:dir=./manifests/charts/ai-gateway-crds-helm/templates
	@go tool controller-gen object crd paths="./api/v1alpha1/..." output:dir=./api/v1alpha1 output:crd:dir=./manifests/charts/ai-gateway-helm/crds

# This generates the API documentation for the API defined in the api/v1alpha1 directory.
.PHONY: apidoc
apidoc: ## Generate API documentation for the API defined in the api directory.
	@go tool crd-ref-docs \
		--source-path=api/v1alpha1 \
		--config=site/crd-ref-docs/config-core.yaml \
		--templates-dir=site/crd-ref-docs/templates \
		--max-depth 20 \
		--output-path site/docs/api/api.mdx \
		--renderer=markdown


##@ Testing

# This runs the unit tests for the codebase.
.PHONY: test
test: ## Run the unit tests for the codebase.
	@echo "test => ./..."
	@go test $(GO_TEST_ARGS) ./...

# This runs the unit tests for the codebase with coverage check.
.PHONY: test-coverage
test-coverage: ## Run the unit tests for the codebase with coverage check.
	@mkdir -p $(OUTPUT_DIR)
	@$(MAKE) test GO_TEST_ARGS="-coverprofile=$(OUTPUT_DIR)/go-test-coverage.out -covermode=atomic -coverpkg=github.com/envoyproxy/ai-gateway/... $(GO_TEST_ARGS)"
	@go tool go-test-coverage --config=.testcoverage.yml

ENVTEST_K8S_VERSIONS ?= 1.31.0 1.32.0 1.33.0

# This runs the integration tests of CEL validation rules in CRD definitions.
#
# This requires the EnvTest binary to be built.
.PHONY: test-crdcel
test-crdcel: apigen ## Run the integration tests of CEL validation in CRD definitions with envtest.
	@for k8sVersion in $(ENVTEST_K8S_VERSIONS); do \
  		echo "Run CEL Validation on k8s $$k8sVersion"; \
        ENVTEST_K8S_VERSION=$$k8sVersion go test ./tests/crdcel $(GO_TEST_ARGS) $(GO_TEST_E2E_ARGS) --tags test_crdcel; \
    done

# This runs the end-to-end tests for extproc without controller or k8s at all.
# It is useful for the fast iteration of the extproc code.
#
# This requires the extproc binary to be built as well as Envoy binary to be available in the PATH.
.PHONY: test-extproc # This requires the extproc binary to be built.
test-extproc: build.extproc ## Run the integration tests for extproc without controller or k8s at all.
	@$(MAKE) build.extproc_custom_metrics CMD_PATH_PREFIX=examples
	@$(MAKE) build.testupstream CMD_PATH_PREFIX=tests/internal/testupstreamlib
	@echo "Run ExtProc test"
	@go test ./tests/extproc/... $(GO_TEST_ARGS) $(GO_TEST_E2E_ARGS) -tags test_extproc

# This runs the end-to-end tests for the controller with EnvTest.
.PHONY: test-controller
test-controller: apigen ## Run the integration tests for the controller with envtest.
	@for k8sVersion in $(ENVTEST_K8S_VERSIONS); do \
  		echo "Run Controller tests on k8s $$k8sVersion"; \
        ENVTEST_K8S_VERSION=$$k8sVersion go test ./tests/controller $(GO_TEST_ARGS) $(GO_TEST_E2E_ARGS) -tags test_controller; \
    done

# This runs the end-to-end tests for the controller and extproc with a local kind cluster.
.PHONY: test-e2e
test-e2e: build-e2e ## Run the end-to-end tests with a local kind cluster.
	@echo "Run E2E tests"
	@go test -v ./tests/e2e/... $(GO_TEST_ARGS) $(GO_TEST_E2E_ARGS) -tags test_e2e



##@ Common

# This builds a binary for the given command under the internal/cmd directory.
#
# Example:
# - `make build.controller`: will build the cmd/controller directory.
# - `make build.extproc`: will build the cmd/extproc directory.
# - `make build.extproc_custom_metrics CMD_PATH_PREFIX=examples`: will build the examples/extproc_custom_metrics directory.
# - `make build.testupstream CMD_PATH_PREFIX=tests/internal/testupstreamlib`: will build the tests/internal/testupstreamlib/testupstream directory.
#
# By default, this will build for the current GOOS and GOARCH.
# To build for multiple platforms, set the GOOS_LIST and GOARCH_LIST variables.
#
# Example:
# - `make build.controller GOOS_LIST="linux darwin" GOARCH_LIST="amd64 arm64"`
CMD_PATH_PREFIX ?= cmd
GOOS_LIST ?= $(shell go env GOOS)
GOARCH_LIST ?= $(shell go env GOARCH)
.PHONY: build.%
build.%: ## Build a binary for the given command under the internal/cmd directory.
	$(eval COMMAND_NAME := $(subst build.,,$@))
	@mkdir -p $(OUTPUT_DIR)
	@for goos in $(GOOS_LIST); do \
		for goarch in $(GOARCH_LIST); do \
			echo "-> Building $(COMMAND_NAME) for $$goos/$$goarch"; \
			CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch go build -ldflags "$(GO_LDFLAGS)" \
				-o $(OUTPUT_DIR)/$(COMMAND_NAME)-$$goos-$$goarch ./$(CMD_PATH_PREFIX)/$(COMMAND_NAME); \
			echo "<- Built $(OUTPUT_DIR)/$(COMMAND_NAME)-$$goos-$$goarch"; \
		done; \
	done

# This builds binaries for all commands under cmd/ directory. All options for `build.%` apply.
#
# Example:
# - `make build`
.PHONE: build
build: ## Build all binaries under cmd/ directory.
	@$(foreach COMMAND_NAME,$(COMMANDS),$(MAKE) build.$(COMMAND_NAME);)

# This builds the docker images for the controller, extproc and testupstream for the e2e tests.
.PHONY: build-e2e
build-e2e: ## Build the docker images for the controller, extproc and testupstream for the e2e tests.
	@$(MAKE) docker-build DOCKER_BUILD_ARGS="--load"
	@$(MAKE) docker-build.testupstream CMD_PATH_PREFIX=tests/internal/testupstreamlib DOCKER_BUILD_ARGS="--load"

# This builds a docker image for a given command.
#
# Example:
# - `make docker-build.controller`: will build the controller command.
# - `make docker-build.extproc`: will build the extproc command.
#
# By default, this will build for the current GOARCH and linux.
# To build for multiple platforms, set the ENABLE_MULTI_PLATFORMS variable to true.
#
# Example:
# - `make docker-build.controller ENABLE_MULTI_PLATFORMS=true`
#
# Also, DOCKER_BUILD_ARGS can be set to pass additional arguments to the docker build command.
#
# Example:
# - `make docker-build.controller ENABLE_MULTI_PLATFORMS=true DOCKER_BUILD_ARGS="--push"` to push the image to the registry.
# - `make docker-build.controller ENABLE_MULTI_PLATFORMS=true DOCKER_BUILD_ARGS="--load"` to load the image after building.
#
# By default, the image tag is set to `latest`. `TAG` can be set to a different value.
#
# Example:
# - `make docker-build.controller TAG=v1.2.3`
#
# To build the main functions outside cmd/ directory, set CMD_PATH_PREFIX to the directory containing the main function.
#
# Example:
# - `make docker-build.extproc_custom_metrics CMD_PATH_PREFIX=examples`
.PHONY: docker-build.%
ifeq ($(ENABLE_MULTI_PLATFORMS),true)
docker-build.%: GOARCH_LIST = amd64 arm64
docker-build.%: PLATFORMS = --platform linux/amd64,linux/arm64
endif
docker-build.%: ## Build a docker image for a given command.
	$(eval COMMAND_NAME := $(subst docker-build.,,$@))
	@$(MAKE) build.$(COMMAND_NAME) GOOS_LIST="linux" GOARCH_LIST="$(GOARCH_LIST)"
	docker buildx build . -t $(OCI_REPOSITORY_PREFIX)-$(COMMAND_NAME):$(TAG) --build-arg COMMAND_NAME=$(COMMAND_NAME) $(PLATFORMS) $(DOCKER_BUILD_ARGS)

# This builds docker images for all commands under cmd/ directory. All options for `docker-build.%` apply.
#
# Example:
# - `make docker-build`
# - `make docker-build ENABLE_MULTI_PLATFORMS=true DOCKER_BUILD_ARGS="--load"`
# - `make docker-build ENABLE_MULTI_PLATFORMS=true DOCKER_BUILD_ARGS="--push" TAG=v1.2.3`
.PHONE: docker-build
docker-build: ## Build docker images for all commands under cmd/ directory.
	@$(foreach COMMAND_NAME,$(COMMANDS),$(MAKE) docker-build.$(COMMAND_NAME);)

HELM_DIR := ./manifests/charts/ai-gateway-helm ./manifests/charts/ai-gateway-crds-helm

# This clears all built artifacts and installed binaries.
#
# Whenever you run into issues with the target like `precommit` or `test`, try running this target.
.PHONY: clean
clean: ## Clears all built artifacts and installed binaries.
	rm -rf $(OUTPUT_DIR)
	rm -rf $(LOCALBIN)

##@ Helm

# This lints the helm chart, ensuring that it is for packaging.
.PHONY: helm-lint
helm-lint: ## Lint envoy ai gateway relevant helm charts.
	@echo "helm-lint => .${HELM_DIR}"
	@go tool helm lint ${HELM_DIR}

# This packages the helm chart into a tgz file, ready for deployment as well as for pushing to the OCI registry.
# This must pass before `helm-push` can be run as well as on any commit.
#
# TAG and HELM_CHART_VERSION are set to the same value when cutting a release. On main branch,
# TAG is set to latest and HELM_CHART_VERSION is set to v0.0.0-latest.
.PHONY: helm-package
helm-package: helm-lint ## Package envoy ai gateway relevant helm charts.
	@echo "helm-package => ${HELM_DIR}"
	@go tool helm package ${HELM_DIR} --app-version ${TAG} --version ${HELM_CHART_VERSION} -d ${OUTPUT_DIR}

# This tests the helm chart, ensuring that the container images are set to have the correct version tag.
.PHONY: helm-test
helm-test: HELM_CHART_VERSION = v9.9.9-latest
helm-test: TAG = v9.9.9
helm-test: HELM_CHART_PATH = $(OUTPUT_DIR)/ai-gateway-helm-${HELM_CHART_VERSION}.tgz
helm-test: helm-package  ## Test the helm chart with a dummy version.
	@go tool helm show chart ${HELM_CHART_PATH} | grep -q "version: ${HELM_CHART_VERSION}"
	@go tool helm show chart ${HELM_CHART_PATH} | grep -q "appVersion: ${TAG}"
	@go tool helm template ${HELM_CHART_PATH} | grep -q "docker.io/envoyproxy/ai-gateway-extproc:${TAG}"
	@go tool helm template ${HELM_CHART_PATH} | grep -q "docker.io/envoyproxy/ai-gateway-controller:${TAG}"

# This pushes the helm chart to the OCI registry, requiring the access to the registry endpoint.
.PHONY: helm-push
helm-push: helm-package ## Push envoy ai gateway relevant helm charts to OCI registry
	@echo "helm-push => .${HELM_DIR}"
	@go tool helm push ${OUTPUT_DIR}/ai-gateway-crds-helm-${HELM_CHART_VERSION}.tgz oci://${OCI_REGISTRY}
	@go tool helm push ${OUTPUT_DIR}/ai-gateway-helm-${HELM_CHART_VERSION}.tgz oci://${OCI_REGISTRY}
