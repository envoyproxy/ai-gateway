# The Go-based tools are defined in Makefile.tools.mk.
include Makefile.tools.mk

# This runs the linter, formatter, and tidy on the codebase.
.PHONY: lint
lint: golangci-lint
	@echo "lint => ./..."
	@$(GOLANGCI_LINT) run ./...

# This runs the formatter on the codebase as well as goimports via gci.
.PHONY: format
format: gci gofumpt
	@echo "format => *.go"
	@find . -type f -name '*.go' | xargs gofmt -s -w
	@find . -type f -name '*.go' | xargs $(GO_FUMPT) -l -w
	@echo "gci => *.go"
	@$(GCI) write -s standard -s default -s "prefix(github.com/envoyproxy/ai-gateway)" `find . -name '*.go'`

# This runs go mod tidy on every module.
.PHONY: tidy
tidy:
	@find . -name "go.mod" \
	| grep go.mod \
	| xargs -I {} bash -c 'dirname {}' \
	| xargs -I {} bash -c 'echo "tidy => {}"; cd {}; go mod tidy -v; '

# This re-generates the CRDs for the API defined in the api/v1alpha1 directory.
.PHONY: apigen
apigen: controller-gen
	@echo "apigen => ./api/v1alpha1/..."
	@$(CONTROLLER_GEN) object crd paths="./api/v1alpha1/..." output:dir=./api/v1alpha1 output:crd:dir=./manifests/charts/ai-gateway-helm/crds

# This runs all necessary steps to prepare for a commit.
.PHONY: precommit
precommit: tidy apigen format lint

# This runs precommit and checks for any differences in the codebase, failing if there are any.
.PHONY: check
check: editorconfig-checker
	@$(MAKE) precommit
	@echo "running editorconfig-checker"
	@$(EDITORCONFIG_CHECKER)
	@if [ ! -z "`git status -s`" ]; then \
		echo "The following differences will fail CI until committed:"; \
		git diff --exit-code; \
	fi

# This runs the unit tests for the codebase.
.PHONY: test
test:
	@echo "test => ./..."
	@go test -v $(shell go list ./... | grep -v e2e)
