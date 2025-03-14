PROTO_FILE=routing_processor.proto
PROTO_DIR=.
PYTHON=$(shell which python3)
GO_OUT_DIR=proto/go
PYTHON_OUT_DIR=proto/python

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
	mkdir -p $(PYTHON_OUT_DIR)
	$(PYTHON) -m grpc_tools.protoc \
		-I=$(PROTO_DIR) \
		--python_out=$(PYTHON_OUT_DIR) \
		--grpc_python_out=$(PYTHON_OUT_DIR) \
		$(PROTO_DIR)/$(PROTO_FILE) 