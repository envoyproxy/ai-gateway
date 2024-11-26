# The Go-based tools are defined in Makefile.tools.mk.
include Makefile.tools.mk

.PHONY: lint
lint: golangci-lint
	@echo "lint => ./..."
	@$(GOLANGCI_LINT) run --build-tags=$(LINT_BUILD_TAGS) ./...

.PHONY: format
format: goimports gofumpt
	@echo "format => *.go"
	@find . -type f -name '*.go' | xargs gofmt -s -w
	@find . -type f -name '*.go' | xargs $(GO_FUMPT) -l -w
	@echo "goimports => *.go"
	@for f in `find . -name '*.go'`; do \
	    awk '/^import \($$/,/^\)$$/{if($$0=="")next}{print}' $$f > /tmp/fmt; \
	    mv /tmp/fmt $$f; \
	done
	@$(GO_IMPORTS) -w -local github.com/envoyproxy/ai-gateway `find . -name '*.go'`

.PHONY: tidy
tidy: ## Runs go mod tidy on every module
	@find . -name "go.mod" \
	| grep go.mod \
	| xargs -I {} bash -c 'dirname {}' \
	| xargs -I {} bash -c 'echo "tidy => {}"; cd {}; go mod tidy -v; '

.PHONY: precommit
precommit: format tidy lint

.PHONY: check
check:
	@$(MAKE) precommit
	@if [ ! -z "`git status -s`" ]; then \
		echo "The following differences will fail CI until committed:"; \
		git diff --exit-code; \
	fi

.PHONY: test
test:
	@echo "test => ./..."
	@go test -v $(shell go list ./... | grep -v e2e)
