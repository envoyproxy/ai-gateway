// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/cel-go/cel"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
	"github.com/envoyproxy/ai-gateway/internal/extproc/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/dynlb"
)

// processorConfig is the configuration for the processor.
// This will be created by the server and passed to the processor when it detects a new configuration.
type processorConfig struct {
	uuid                                         string
	schema                                       filterapi.VersionedAPISchema
	router                                       x.Router
	modelNameHeaderKey, selectedBackendHeaderKey string
	backendAuthHandlers                          map[string]backendauth.Handler
	metadataNamespace                            string
	requestCosts                                 []processorConfigRequestCost
	declaredModels                               []string
	dynamicLoadBalancers                         map[*filterapi.DynamicLoadBalancing]dynlb.DynamicLoadBalancer
}

// processorConfigRequestCost is the configuration for the request cost.
type processorConfigRequestCost struct {
	*filterapi.LLMRequestCost
	celProg cel.Program
}

// ProcessorFactory is the factory function used to create new instances of a processor.
type ProcessorFactory func(*processorConfig, map[string]string, *slog.Logger) (Processor, error)

// Processor is the interface for the processor.
// This decouples the processor implementation detail from the server implementation.
type Processor interface {
	// ProcessRequestHeaders processes the request headers message.
	ProcessRequestHeaders(context.Context, *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error)
	// ProcessRequestBody processes the request body message.
	ProcessRequestBody(context.Context, *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error)
	// ProcessResponseHeaders processes the response headers message.
	ProcessResponseHeaders(context.Context, *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error)
	// ProcessResponseBody processes the response body message.
	ProcessResponseBody(context.Context, *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error)
}

// passThroughProcessor implements the Processor interface.
type passThroughProcessor struct{}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
func (p passThroughProcessor) ProcessRequestHeaders(context.Context, *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{}}, nil
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (p passThroughProcessor) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseBody{}}, nil
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (p passThroughProcessor) ProcessResponseHeaders(context.Context, *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{}}, nil
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (p passThroughProcessor) ProcessResponseBody(context.Context, *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseBody{}}, nil
}
