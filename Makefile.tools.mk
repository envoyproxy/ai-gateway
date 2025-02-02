LOCALBIN ?= $(shell pwd)/.bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool binary names.
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
GO_FUMPT = $(LOCALBIN)/gofumpt
GCI = $(LOCALBIN)/gci
EDITORCONFIG_CHECKER = $(LOCALBIN)/editorconfig-checker
CODESPELL = $(LOCALBIN)/.venv/codespell@v2.3.0/bin/codespell
YAMLLINT = $(LOCALBIN)/.venv/yamllint@1.35.1/bin/yamllint
KIND ?= $(LOCALBIN)/kind
CRD_REF_DOCS = $(LOCALBIN)/crd-ref-docs
GO_TEST_COVERAGE ?= $(LOCALBIN)/go-test-coverage

## Tool versions.
CONTROLLER_TOOLS_VERSION ?= v0.17.1
ENVTEST_VERSION ?= release-0.19
GOLANGCI_LINT_VERSION ?= v1.63.4
GO_FUMPT_VERSION ?= v0.7.0
GCI_VERSION ?= v0.13.5
EDITORCONFIG_CHECKER_VERSION ?= v3.1.2
KIND_VERSION ?= v0.26.0
CRD_REF_DOCS_VERSION ?= v0.1.0
GO_TEST_COVERAGE_VERSION ?= v2.11.4

# Docker image names
GOLANGCI_LINT_IMAGE ?= golangci/golangci-lint:$(GOLANGCI_LINT_VERSION)

# Cache configuration (set USE_LINT_CACHE=0 to disable)
USE_LINT_CACHE ?= 1
DOCKER_CACHE_DIR ?= $(HOME)/.cache/docker
GOLANGCI_CACHE_DIR ?= $(HOME)/.cache/golangci-lint

# This ensures the Docker image is available and cache directories exist
.PHONY: ensure-golangci-lint-image
ensure-golangci-lint-image: check-prereqs
	@if ! docker image inspect $(GOLANGCI_LINT_IMAGE) >/dev/null 2>&1; then \
		echo "Pulling golangci-lint Docker image $(GOLANGCI_LINT_IMAGE)"; \
		docker pull $(GOLANGCI_LINT_IMAGE); \
	fi

# Define the docker run command with optional caching
define docker-golangci-lint-cmd
docker run --rm \
	-v $$(pwd):/app:delegated \
	-v $$(go env GOMODCACHE):/go/pkg/mod \
	-w /app \
	$(GOLANGCI_LINT_IMAGE)
endef

.PHONY: docker-golangci-lint
docker-golangci-lint: ensure-golangci-lint-image
	@echo "lint => ./..."
	@echo "Starting analysis..."
	@start_time=$$(date +%s); \
	$(docker-golangci-lint-cmd) golangci-lint run \
		--build-tags==test_cel_validation,test_controller,test_extproc \
		--timeout=3m \
		./... > /tmp/golangci.out 2>&1 & \
	lint_pid=$$!; \
	spinner=( "⠋" "⠙" "⠹" "⠸" "⠼" "⠴" "⠦" "⠧" "⠇" "⠏" ); \
	i=0; \
	while kill -0 $$lint_pid 2>/dev/null; do \
		elapsed=$$(($$(date +%s) - start_time)); \
		printf "\r%s Analyzing code... [%02d:%02d elapsed]" \
			"$${spinner[$$i]}" $$((elapsed/60)) $$((elapsed%60)); \
		i=$$((i+1)); \
		[ $$i -eq $${#spinner[@]} ] && i=0; \
		sleep 0.1; \
	done; \
	wait $$lint_pid; \
	lint_exit_code=$$?; \
	elapsed=$$(($$(date +%s) - start_time)); \
	if [ $$lint_exit_code -eq 0 ]; then \
		printf "\r⠿ Analysis completed in %02d:%02d                              \n" $$((elapsed/60)) $$((elapsed%60)); \
		echo "✨ Congratulations! No linting errors! ✨"; \
	else \
		printf "\r⠿ Analysis completed in %02d:%02d                              \n" $$((elapsed/60)) $$((elapsed%60)); \
		echo "Linting issues found:"; \
		cat /tmp/golangci.out; \
		rm -f /tmp/golangci.out; \
		exit 1; \
	fi; \
	rm -f /tmp/golangci.out

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT)
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

.PHONY: gofumpt
gofumpt: $(GO_FUMPT)
$(GO_FUMPT): $(LOCALBIN)
	$(call go-install-tool,$(GO_FUMPT),mvdan.cc/gofumpt,$(GO_FUMPT_VERSION))

.PHONY: gci
gci: $(GCI)
$(GCI): $(LOCALBIN)
	$(call go-install-tool,$(GCI),github.com/daixiang0/gci,$(GCI_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: editorconfig-checker
editorconfig-checker: $(EDITORCONFIG_CHECKER)
$(EDITORCONFIG_CHECKER): $(LOCALBIN)
	$(call go-install-tool,$(EDITORCONFIG_CHECKER),github.com/editorconfig-checker/editorconfig-checker/v3/cmd/editorconfig-checker,$(EDITORCONFIG_CHECKER_VERSION))
	@echo "editorconfig => ./..."
	@$(EDITORCONFIG_CHECKER)

.PHONY: envtest
envtest: $(ENVTEST)
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: kind
kind: $(KIND)
$(KIND): $(LOCALBIN)
	$(call go-install-tool,$(KIND),sigs.k8s.io/kind,$(KIND_VERSION))

.PHONY: crd-ref-docs
crd-ref-docs: $(CRD_REF_DOCS)
$(CRD_REF_DOCS): $(LOCALBIN)
	$(call go-install-tool,$(CRD_REF_DOCS),github.com/elastic/crd-ref-docs,$(CRD_REF_DOCS_VERSION))

.PHONY: go-test-coverage
go-test-coverage: $(GO_TEST_COVERAGE)
$(GO_TEST_COVERAGE): $(LOCALBIN)
	$(call go-install-tool,$(GO_TEST_COVERAGE),github.com/vladopajic/go-test-coverage/v2,$(GO_TEST_COVERAGE_VERSION))

.bin/.venv/%:
	mkdir -p $(@D)
	python3 -m venv $@
	$@/bin/pip3 install $$(echo $* | sed 's/@/==/')

$(CODESPELL): .bin/.venv/codespell@v2.3.0

$(YAMLLINT): .bin/.venv/yamllint@1.35.1

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) || true ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $(1)-$(3) $(1)
endef

# Check for required tools
REQUIRED_BINS := docker
check-prereqs:
	$(foreach bin,$(REQUIRED_BINS),\
		$(if $(shell command -v $(bin) 2> /dev/null),,$(error Please install $(bin) to run this test)))
