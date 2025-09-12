# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

GO_TOOL := go tool -modfile=tools/go.mod

LOCALBIN ?= $(shell pwd)/.bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

CODESPELL = $(LOCALBIN)/.venv/codespell@v2.3.0/bin/codespell
YAMLLINT = $(LOCALBIN)/.venv/yamllint@1.35.1/bin/yamllint

.bin/.venv/%:
	mkdir -p $(@D)
	python3 -m venv $@
	$@/bin/pip3 install $$(echo $* | sed 's/@/==/')

$(CODESPELL): .bin/.venv/codespell@v2.3.0

$(YAMLLINT): .bin/.venv/yamllint@1.35.1
