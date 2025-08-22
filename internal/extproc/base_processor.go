// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"fmt"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// RouterFilterHelper provides shared functionality for router filters.
type RouterFilterHelper struct {
	upstreamFilter      Processor
	config              *processorConfig
	requestHeaders      map[string]string
	logger              *slog.Logger
	upstreamFilterCount int
}

// NewRouterFilterHelper creates a new RouterFilterHelper.
func NewRouterFilterHelper(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger) *RouterFilterHelper {
	return &RouterFilterHelper{
		config:         config,
		requestHeaders: requestHeaders,
		logger:         logger,
	}
}

// ProcessResponseHeaders provides common response header processing for router filters.
func (h *RouterFilterHelper) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap, passthrough passThroughProcessor) (*extprocv3.ProcessingResponse, error) {
	if h.upstreamFilter != nil {
		return h.upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return passthrough.ProcessResponseHeaders(ctx, headerMap)
}

// ProcessResponseBody provides common response body processing for router filters.
func (h *RouterFilterHelper) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody, passthrough passThroughProcessor) (*extprocv3.ProcessingResponse, error) {
	if h.upstreamFilter != nil {
		return h.upstreamFilter.ProcessResponseBody(ctx, body)
	}
	return passthrough.ProcessResponseBody(ctx, body)
}

// CreateStandardRequestResponse creates a standard request body response with headers.
func (h *RouterFilterHelper) CreateStandardRequestResponse(modelName string, additionalHeaders []*corev3.HeaderValueOption) *extprocv3.ProcessingResponse {
	h.requestHeaders[h.config.modelNameHeaderKey] = modelName

	// Create base headers
	baseHeaders := []*corev3.HeaderValueOption{
		{
			Header: &corev3.HeaderValue{Key: h.config.modelNameHeaderKey, RawValue: []byte(modelName)},
		},
		{
			Header: &corev3.HeaderValue{Key: originalPathHeader, RawValue: []byte(h.requestHeaders[":path"])},
		},
	}

	// Combine base headers with additional headers
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

// SetUpstreamFilter sets the upstream filter and increments the counter.
func (h *RouterFilterHelper) SetUpstreamFilter(filter Processor) {
	h.upstreamFilter = filter
	h.upstreamFilterCount++
}

// GetUpstreamFilterCount returns the current upstream filter count.
func (h *RouterFilterHelper) GetUpstreamFilterCount() int {
	return h.upstreamFilterCount
}

// UpstreamFilterHelper provides shared functionality for upstream filters.
type UpstreamFilterHelper struct {
	config              *processorConfig
	requestHeaders      map[string]string
	responseHeaders     map[string]string
	responseEncoding    string
	modelNameOverride   string
	backendName         string
	logger              *slog.Logger
	onRetry             bool
}

// NewUpstreamFilterHelper creates a new UpstreamFilterHelper.
func NewUpstreamFilterHelper(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger) *UpstreamFilterHelper {
	return &UpstreamFilterHelper{
		config:         config,
		requestHeaders: requestHeaders,
		logger:         logger,
	}
}

// ProcessResponseHeadersBase provides common response header processing for upstream filters.
func (h *UpstreamFilterHelper) ProcessResponseHeadersBase(ctx context.Context, headers *corev3.HeaderMap, responseHeadersFunc func(map[string]string) (*extprocv3.HeaderMutation, error)) (*extprocv3.ProcessingResponse, error) {
	h.responseHeaders = headersToMap(headers)
	if enc := h.responseHeaders["content-encoding"]; enc != "" {
		h.responseEncoding = enc
	}

	headerMutation, err := responseHeadersFunc(h.responseHeaders)
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

// SetBackendInfo sets backend information.
func (h *UpstreamFilterHelper) SetBackendInfo(modelNameOverride, backendName string, onRetry bool) {
	h.modelNameOverride = modelNameOverride
	h.backendName = backendName
	h.onRetry = onRetry
}

// GetResponseHeaders returns the response headers.
func (h *UpstreamFilterHelper) GetResponseHeaders() map[string]string {
	return h.responseHeaders
}

// GetResponseEncoding returns the response encoding.
func (h *UpstreamFilterHelper) GetResponseEncoding() string {
	return h.responseEncoding
}

// IsOnRetry returns whether this is a retry request.
func (h *UpstreamFilterHelper) IsOnRetry() bool {
	return h.onRetry
}

// ProcessorCreationHelper provides utilities for creating processor factories.
type ProcessorCreationHelper struct{}

// CreateStandardFactory creates a standardized processor factory with common logging setup.
func (ProcessorCreationHelper) CreateStandardFactory(
	processorName string,
	routerCreator func(*processorConfig, map[string]string, *slog.Logger, tracing.Tracing) Processor,
	upstreamCreator func(*processorConfig, map[string]string, *slog.Logger, interface{}) Processor,
	metrics interface{},
) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, tracing tracing.Tracing, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("processor", processorName, "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return routerCreator(config, requestHeaders, logger, tracing), nil
		}
		return upstreamCreator(config, requestHeaders, logger, metrics), nil
	}
}