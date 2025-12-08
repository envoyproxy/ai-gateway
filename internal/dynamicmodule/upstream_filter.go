// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dynamicmodule

import (
	"context"
	"fmt"
	"strconv"
	"unsafe"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/dynamicmodule/sdk"
	"github.com/envoyproxy/ai-gateway/internal/endpointspec"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

type (
	// upstreamFilterConfig implements [sdk.HTTPFilterConfig].
	upstreamFilterConfig struct{ env *Env }
	// upstreamFilter implements [sdk.HTTPFilter].
	upstreamFilter struct {
		env         *Env
		typedFilter upstreamFilterTypedIface
	}

	upstreamFilterTypedIface interface {
		RequestHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.RequestHeadersStatus
		RequestBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.RequestBodyStatus
		ResponseHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.ResponseHeadersStatus
		ResponseBody(sdk.EnvoyHTTPFilter, bool) sdk.ResponseBodyStatus
	}

	upstreamFilterTyped[ReqT, RespT, RespChunkT any, EndpointSpec endpointspec.Spec[ReqT, RespT, RespChunkT]] struct {
		routerFilter *routerFilterTyped[ReqT, RespT, RespChunkT, EndpointSpec]
		translator   translator.Translator[ReqT, tracing.Span[RespT, RespChunkT]]
		metrics      metrics.Metrics

		onRetry    bool
		reqHeaders map[string]string
		backend    *filterapi.RuntimeBackend
	}
)

func NewUpstreamFilterConfig(env *Env) sdk.HTTPFilterConfig {
	return &upstreamFilterConfig{env: env}
}

// NewFilter implements [sdk.HTTPFilterConfig].
func (f *upstreamFilterConfig) NewFilter() sdk.HTTPFilter {
	return &upstreamFilter{env: f.env}
}

// RequestHeaders implements [sdk.HTTPFilter].
func (f *upstreamFilter) RequestHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.RequestHeadersStatus {
	rfPtrStr, ok := e.GetDynamicMetadataString(internalapi.AIGatewayFilterMetadataNamespace,
		routerFilterPointerDynamicMetadataKey)
	if !ok {
		e.SendLocalReply(500, nil, []byte("router filter pointer not found in dynamic metadata"))
		return sdk.RequestHeadersStatusStopIteration
	}
	rfPtr, err := strconv.ParseInt(rfPtrStr, 10, 64)
	if err != nil {
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("invalid router filter pointer: %v", err)))
		return sdk.RequestHeadersStatusStopIteration
	}
	rf := (*routerFilter)(unsafe.Pointer(uintptr(rfPtr))) // nolint:govet
	rf.attemptCount++
	onRetry := rf.attemptCount > 1
	backend, ok := e.GetUpstreamHostMetadataString(internalapi.AIGatewayFilterMetadataNamespace, internalapi.InternalMetadataBackendNameKey)
	if !ok {
		e.SendLocalReply(500, nil, []byte("backend name not found in upstream host metadata"))
		return sdk.RequestHeadersStatusStopIteration
	}
	b, ok := rf.runtimeFilterConfig.Backends[backend]
	if !ok {
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("backend %s not found in filter config", backend)))
		return sdk.RequestHeadersStatusStopIteration
	}

	switch rf.endpoint {
	case chatCompletionsEndpoint:
		f.typedFilter = &upstreamFilterTyped[openai.ChatCompletionRequest,
			openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk, endpointspec.ChatCompletionsEndpointSpec]{
			metrics: f.env.ChatCompletionMetricsFactory.NewMetrics(),
			onRetry: onRetry,
			backend: b,
		}
	case completionsEndpoint:
		f.typedFilter = &upstreamFilterTyped[openai.CompletionRequest,
			openai.CompletionResponse, openai.CompletionResponse, endpointspec.CompletionsEndpointSpec]{
			onRetry: onRetry,
			metrics: f.env.CompletionMetricsFactory.NewMetrics(),
			backend: b,
		}
	case embeddingsEndpoint:
		f.typedFilter = &upstreamFilterTyped[openai.EmbeddingRequest,
			openai.EmbeddingResponse, struct{}, endpointspec.EmbeddingsEndpointSpec]{
			onRetry: onRetry,
			metrics: f.env.EmbeddingsMetricsFactory.NewMetrics(),
			backend: b,
		}
	case imagesGenerationsEndpoint:
		f.typedFilter = &upstreamFilterTyped[openai.ImageGenerationRequest,
			openai.ImageGenerationResponse, struct{}, endpointspec.ImageGenerationEndpointSpec]{
			onRetry: onRetry,
			metrics: f.env.ImageGenerationMetricsFactory.NewMetrics(),
			backend: b,
		}
	case rerankEndpoint:
		f.typedFilter = &upstreamFilterTyped[cohereschema.RerankV2Request,
			cohereschema.RerankV2Response, struct{}, endpointspec.RerankEndpointSpec]{
			onRetry: onRetry,
			metrics: f.env.RerankMetricsFactory.NewMetrics(),
			backend: b,
		}
	case messagesEndpoint:
		f.typedFilter = &upstreamFilterTyped[anthropic.MessagesRequest,
			anthropic.MessagesResponse, anthropic.MessagesStreamChunk, endpointspec.MessagesEndpointSpec]{
			onRetry: onRetry,
			metrics: f.env.MessagesMetricsFactory.NewMetrics(),
			backend: b,
		}
	default:
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("unsupported endpoint type: %v", rf.endpoint)))
		return sdk.RequestHeadersStatusStopIteration
	}
	return f.typedFilter.RequestHeaders(e, false)
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) RequestHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.RequestHeadersStatus {
	var espec EndpointSpecT
	var err error
	f.translator, err = espec.GetTranslator(f.backend.Backend.Schema, f.backend.Backend.ModelNameOverride)
	if err != nil {
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to create translator: %v", err)))
		return sdk.RequestHeadersStatusStopIteration
	}

	f.reqHeaders = multiValueHeadersToSingleValue(e.GetRequestHeaders())

	// Now mutate the headers based on the backend configuration.
	be := f.backend.Backend
	if hm := be.HeaderMutation; hm != nil {
		sets, removes := headermutator.NewHeaderMutator(be.HeaderMutation, f.routerFilter.originalRequestHeaders).Mutate(f.reqHeaders, f.onRetry)
		for _, h := range sets {
			if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
				e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to set header %s", h.Key())))
				return sdk.RequestHeadersStatusStopIteration
			}
			f.reqHeaders[h.Key()] = h.Value()
		}
		for _, key := range removes {
			if !e.SetRequestHeader(key, nil) {
				e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to remove header %s", key)))
				return sdk.RequestHeadersStatusStopIteration
			}
			delete(f.reqHeaders, key)
		}
	}
	return sdk.RequestHeadersStatusContinue
}

// RequestBody implements [sdk.HTTPFilter].
func (f *upstreamFilter) RequestBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.RequestBodyStatus {
	return f.typedFilter.RequestBody(e, endOfStream)
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) RequestBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.RequestBodyStatus {
	if !endOfStream {
		// TODO: ideally, we should not buffer the entire body for the passthrough case.
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}

	b := f.backend
	newHeaders, newBody, err := f.translator.RequestBody(f.routerFilter.originalRequestBodyRaw,
		f.routerFilter.originalRequestBody, f.onRetry)

	if err != nil {
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to translate request body: %v", err)))
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}
	for _, h := range newHeaders {
		if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
			e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to set mutated header %s", h.Key())))
			return sdk.RequestBodyStatusStopIterationAndBuffer
		}
	}

	if bm := b.Backend.BodyMutation; bm != nil {
		// TODO: body mutation if needed.
		_ = bm
	}

	if newBody != nil {
		cur, ok := e.GetRequestBody()
		if !ok {
			e.SendLocalReply(500, nil, []byte("failed to get request body for upstream mutation"))
			return sdk.RequestBodyStatusStopIterationAndBuffer
		}
		_ = e.DrainResponseBody(cur.Len())
		_ = e.AppendRequestBody(newBody)
	}

	// Next is to do the upstream auth if needed.
	if b.Handler != nil {
		var originalOrNewBody []byte
		if newBody != nil {
			originalOrNewBody = newBody
		}

		authHeaders, err := b.Handler.Do(context.Background(), f.reqHeaders, originalOrNewBody)
		if err != nil {
			e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to do backend auth: %v", err)))
			return sdk.RequestBodyStatusStopIterationAndBuffer
		}
		for _, h := range authHeaders {
			if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
				e.SendLocalReply(500, nil, []byte(fmt.Sprintf("failed to set auth header %s", h.Key())))
				return sdk.RequestBodyStatusStopIterationAndBuffer
			}
		}
	}
	return sdk.RequestBodyStatusContinue
}

// ResponseHeaders implements [sdk.HTTPFilter].
func (f *upstreamFilter) ResponseHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.ResponseHeadersStatus {
	return f.typedFilter.ResponseHeaders(e, false)
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.ResponseHeadersStatus {
	_ = e
	return sdk.ResponseHeadersStatusContinue
}

// ResponseBody implements [sdk.HTTPFilter].
func (f *upstreamFilter) ResponseBody(sdk.EnvoyHTTPFilter, bool) sdk.ResponseBodyStatus {
	return f.typedFilter.ResponseBody(nil, false)
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseBody(sdk.EnvoyHTTPFilter, bool) sdk.ResponseBodyStatus {
	return sdk.ResponseBodyStatusContinue
}
