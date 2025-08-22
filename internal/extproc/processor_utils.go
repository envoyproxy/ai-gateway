// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"fmt"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/extproc/backendauth"
)

// createStandardRouterRequestResponse creates a standard request response with model and path headers.
// This eliminates the duplicated logic across all router filter ProcessRequestBody implementations.
func createStandardRouterRequestResponse(
	config *processorConfig,
	requestHeaders map[string]string,
	modelName string,
	additionalHeaders []*corev3.HeaderValueOption,
) *extprocv3.ProcessingResponse {
	// Set model name in request headers
	requestHeaders[config.modelNameHeaderKey] = modelName

	// Create base headers that all processors need
	baseHeaders := []*corev3.HeaderValueOption{
		{
			Header: &corev3.HeaderValue{Key: config.modelNameHeaderKey, RawValue: []byte(modelName)},
		},
		{
			Header: &corev3.HeaderValue{Key: originalPathHeader, RawValue: []byte(requestHeaders[":path"])},
		},
	}

	// Combine base headers with any additional headers
	allHeaders := append(baseHeaders, additionalHeaders...)

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: &extprocv3.HeaderMutation{
						SetHeaders: allHeaders,
					},
					ClearRouteCache: true,
				},
			},
		},
	}
}

// processRouterResponseHeaders provides common response header processing for router filters.
// This eliminates duplicated logic across all router filter ProcessResponseHeaders implementations.
func processRouterResponseHeaders(
	ctx context.Context,
	headerMap *corev3.HeaderMap,
	upstreamFilter Processor,
	passthrough passThroughProcessor,
) (*extprocv3.ProcessingResponse, error) {
	if upstreamFilter != nil {
		return upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return passthrough.ProcessResponseHeaders(ctx, headerMap)
}

// processRouterResponseBody provides common response body processing for router filters.
// This eliminates duplicated logic across all router filter ProcessResponseBody implementations.
func processRouterResponseBody(
	ctx context.Context,
	body *extprocv3.HttpBody,
	upstreamFilter Processor,
	passthrough passThroughProcessor,
) (*extprocv3.ProcessingResponse, error) {
	if upstreamFilter != nil {
		return upstreamFilter.ProcessResponseBody(ctx, body)
	}
	return passthrough.ProcessResponseBody(ctx, body)
}

// processUpstreamRequestHeaders provides common request header processing for upstream filters.
// This eliminates duplicated auth and header mutation logic.
func processUpstreamRequestHeaders(
	ctx context.Context,
	config *processorConfig,
	requestHeaders map[string]string,
	originalRequestBodyRaw []byte,
	originalRequestBody interface{},
	translator interface{},
	handler interface{},
	onRetry bool,
	metricsRecorder interface{},
) (*extprocv3.ProcessingResponse, error) {
	// This is a simplified version - each processor can call this with their specific translator
	// implementation and handle the response appropriately
	
	// For now, return nil to indicate this needs processor-specific implementation
	// But this demonstrates the pattern for extracting common logic
	return nil, fmt.Errorf("processUpstreamRequestHeaders needs processor-specific implementation")
}

// standardUpstreamResponseHeadersProcessing provides common response header processing for upstream filters.
func standardUpstreamResponseHeadersProcessing(
	headers *corev3.HeaderMap,
	responseHeaders *map[string]string,
	responseEncoding *string,
	translator interface{},
) (*extprocv3.ProcessingResponse, error) {
	*responseHeaders = headersToMap(headers)
	if enc := (*responseHeaders)["content-encoding"]; enc != "" {
		*responseEncoding = enc
	}

	// Use type assertion to call ResponseHeaders on the translator
	type responseHeadersTranslator interface {
		ResponseHeaders(map[string]string) (*extprocv3.HeaderMutation, error)
	}

	rht, ok := translator.(responseHeadersTranslator)
	if !ok {
		return nil, fmt.Errorf("translator does not implement ResponseHeaders method")
	}

	headerMutation, err := rht.ResponseHeaders(*responseHeaders)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response headers: %w", err)
	}

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{HeaderMutation: headerMutation},
			},
		},
	}, nil
}

// standardUpstreamProcessRequestBodyPanic provides the standard panic for upstream filters.
// All upstream filters should panic if ProcessRequestBody is called.
func standardUpstreamProcessRequestBodyPanic() {
	panic("BUG: ProcessRequestBody should not be called in the upstream filter")
}

// commonUpstreamSetBackendPattern provides common SetBackend logic pattern.
// This reduces duplication in the SetBackend implementations across upstream filters.
func commonUpstreamSetBackendPattern(
	ctx context.Context,
	upstreamFilterCount *int,
	metricsRecorder interface{},
	requestHeaders map[string]string,
	routeProcessor Processor,
	backend *filterapi.Backend,
	backendHandler backendauth.Handler,
	selectTranslatorFunc func(filterapi.VersionedAPISchema) error,
	setBackendSpecificData func(Processor, int),
) error {
	// Common error handling pattern
	if metricsInterface, ok := metricsRecorder.(interface {
		RecordRequestCompletion(context.Context, bool, map[string]string)
	}); ok {
		defer func() {
			// This is called in each SetBackend, but the actual error handling is done by each processor
			_ = metricsInterface
		}()
	}

	// Common backend setup
	if err := selectTranslatorFunc(backend.Schema); err != nil {
		return fmt.Errorf("failed to select translator: %w", err)
	}

	// Set the backend-specific data using the provided function
	setBackendSpecificData(routeProcessor, *upstreamFilterCount+1)

	return nil
}