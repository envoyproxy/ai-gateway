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

## Tool versions.
CONTROLLER_TOOLS_VERSION ?= v0.16.2
ENVTEST_VERSION ?= release-0.19
GOLANGCI_LINT_VERSION ?= v1.60.1
GO_FUMPT_VERSION ?= v0.6.0
GCI_VERSION ?= v0.13.5
EDITORCONFIG_CHECKER_VERSION ?= v3.0.3

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

.PHONY: envtest
envtest: $(ENVTEST)
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.bin/.venv/%:
	mkdir -p $(@D)
	python3 -m venv $@
	$@/bin/pip3 install $$(echo $* | sed 's/@/==/')

$(CODESPELL): .bin/.venv/codespell@v2.3.0

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
