PROTO_FILE=routing-processor.proto
PROTO_DIR=.
GO_OUT_DIR=poc/semantic_cache/client/routing_processor
PYTHON=$(shell which python3)
PYTHON_OUT_DIR=poc/semantic_cache/server

.PHONY: generate
generate: generate-go generate-python

.PHONY: generate-go
generate-go:
	mkdir -p $(GO_OUT_DIR)
	protoc -I=$(PROTO_DIR) \
		--go_out=$(GO_OUT_DIR) \
		--go_opt=paths=source_relative \
		--go-grpc_out=$(GO_OUT_DIR) \
		--experimental_allow_proto3_optional \
		--go-grpc_opt=paths=source_relative \
		$(PROTO_DIR)/$(PROTO_FILE)

.PHONY: generate-python
generate-python:
	$(PYTHON) -m grpc_tools.protoc \
		-I=$(PROTO_DIR) \
		--python_out=$(PYTHON_OUT_DIR) \
		--grpc_python_out=$(PYTHON_OUT_DIR) \
		$(PROTO_DIR)/$(PROTO_FILE)