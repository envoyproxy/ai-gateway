// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dynamicmodule

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/dynamicmodule/sdk"
	"github.com/envoyproxy/ai-gateway/internal/endpointspec"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

const internalRequestIDMetadataKey = "internal_request_id"

type (
	// routerFilterConfig implements [sdk.HTTPFilterConfig].
	//
	// This is mostly for debugging purposes, it does not do anything except
	// setting a response header with the version of the dynamic module.
	routerFilterConfig struct {
		fcr              **filterapi.RuntimeConfig
		prefixToEndpoint map[string]endpoint
		env              *Env
	}
	// routerFilter implements [sdk.HTTPFilter].
	routerFilter struct {
		// prefixToEndpoint maps request path prefixes to endpoints. Shallow copy of
		// the one in routerFilterConfig at the time of filter creation.
		prefixToEndpoint map[string]endpoint
		// runtimeFilterConfig is the snapshot of the runtime filter configuration at the time of filter creation.
		runtimeFilterConfig *filterapi.RuntimeConfig
		// tracing is the tracing implementation inherited from the environment.
		tracing      tracing.Tracing
		attemptCount int
		// endpoint is the endpoint that the current request is targeting.
		endpoint endpoint
		// typedFilter is the typed router filter for the current request.
		typedFilter routerFilterTypedIface

		// Inherited from the environment.
		routerFilters *RouterFilters
		// This indicates whether the request was 200 on the response headers phase.
		success           bool
		internalRequestID string
	}

	// routerFilterTypedIface is the interface for the typed router filter.
	routerFilterTypedIface interface {
		RequestBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.RequestBodyStatus
		UpstreamTypedFilter() upstreamFilterTypedIface
		Stream() bool
	}

	// routerFilter typed is the typed implementation of the router filter for a specific endpoint.
	routerFilterTyped[ReqT, RespT, RespChunkT any, EndpointSpec endpointspec.Spec[ReqT, RespT, RespChunkT]] struct {
		runtimeFilterConfig    *filterapi.RuntimeConfig
		ep                     EndpointSpec
		originalRequestHeaders map[string]string
		originalRequestBody    *ReqT
		originalRequestBodyRaw []byte
		originalModel          internalapi.OriginalModel
		forceBodyMutation      bool
		stream                 bool
		tracer                 tracing.RequestTracer[ReqT, RespT, RespChunkT]
		span                   tracing.Span[RespT, RespChunkT]
		upstreamFilter         upstreamFilterTypedIface
	}
)

// NewRouterFilterConfig creates a new instance of an implementation of [sdk.HTTPFilterConfig] for the router filter.
func NewRouterFilterConfig(env *Env, fcr **filterapi.RuntimeConfig) sdk.HTTPFilterConfig {
	prefixToEndpoint := map[string]endpoint{
		path.Join(env.RootPrefix, env.EndpointPrefixes.OpenAI, "/v1/chat/completions"):   chatCompletionsEndpoint,
		path.Join(env.RootPrefix, env.EndpointPrefixes.OpenAI, "/v1/completions"):        completionsEndpoint,
		path.Join(env.RootPrefix, env.EndpointPrefixes.OpenAI, "/v1/embeddings"):         embeddingsEndpoint,
		path.Join(env.RootPrefix, env.EndpointPrefixes.OpenAI, "/v1/images/generations"): imagesGenerationsEndpoint,
		path.Join(env.RootPrefix, env.EndpointPrefixes.Cohere, "/v2/rerank"):             rerankEndpoint,
		path.Join(env.RootPrefix, env.EndpointPrefixes.OpenAI, "/v1/models"):             modelsEndpoint,
		path.Join(env.RootPrefix, env.EndpointPrefixes.Anthropic, "/v1/messages"):        messagesEndpoint,
		path.Join(env.RootPrefix, env.EndpointPrefixes.OpenAI, "/v1/responses"):          responsesEndpoint,
	}
	return &routerFilterConfig{
		fcr:              fcr,
		prefixToEndpoint: prefixToEndpoint,
		env:              env,
	}
}

// NewFilter implements [sdk.HTTPFilterConfig].
func (f *routerFilterConfig) NewFilter() sdk.HTTPFilter {
	return &routerFilter{
		prefixToEndpoint:    f.prefixToEndpoint,
		runtimeFilterConfig: *f.fcr,
		tracing:             f.env.Tracing,
		routerFilters:       f.env.RouterFilters,
	}
}

// RequestHeaders implements [sdk.HTTPFilter].
func (f *routerFilter) RequestHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.RequestHeadersStatus {
	p, _ := e.GetRequestHeader(":path") // The :path pseudo header is always present.
	// Strip query parameters for processor lookup.
	if queryIndex := strings.Index(p, "?"); queryIndex != -1 {
		p = p[:queryIndex]
	}
	ep, ok := f.prefixToEndpoint[p]
	if !ok {
		e.SendLocalReply(404, nil, []byte(fmt.Sprintf("unsupported path: %s", p)))
		return sdk.RequestHeadersStatusStopIteration
	}
	if sdk.LogDebugEnabled {
		sdk.Log(sdk.LogLevelDebug, "router filter: continuing to request body phase for endpoint %s", f.endpoint)
	}
	f.endpoint = ep
	if f.endpoint == modelsEndpoint {
		return f.handleModelsEndpoint(e)
	}

	originalReqID, ok := e.GetRequestHeader("x-request-id")
	if !ok {
		e.SendLocalReply(400, nil, []byte("missing x-request-id header"))
		return sdk.RequestHeadersStatusStopIteration
	}
	internalReqID := originalReqID + uuid.NewString()
	if !e.SetDynamicMetadataString(internalapi.AIGatewayFilterMetadataNamespace, internalRequestIDMetadataKey, internalReqID) {
		e.SendLocalReply(500, nil, []byte("failed to set x-internal-request-id metadata"))
		return sdk.RequestHeadersStatusStopIteration
	}
	f.routerFilters.Lock.Lock()
	f.routerFilters.Filters[internalReqID] = f
	f.routerFilters.Lock.Unlock()
	if sdk.LogDebugEnabled {
		sdk.Log(sdk.LogLevelDebug,
			"router filter: registered filter for internal request ID %s (original request ID: %s)",
			internalReqID, originalReqID)
	}
	f.internalRequestID = internalReqID
	return sdk.RequestHeadersStatusStopIteration // Do not invoke the subsequent filters but continue to the body phase on this filter.
}

// RequestBody implements [sdk.HTTPFilter].
func (f *routerFilter) RequestBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.RequestBodyStatus {
	switch f.endpoint {
	case chatCompletionsEndpoint:
		f.typedFilter = &routerFilterTyped[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk, endpointspec.ChatCompletionsEndpointSpec]{
			runtimeFilterConfig: f.runtimeFilterConfig,
			tracer:              f.tracing.ChatCompletionTracer(),
		}
	case completionsEndpoint:
		f.typedFilter = &routerFilterTyped[openai.CompletionRequest, openai.CompletionResponse, openai.CompletionResponse, endpointspec.CompletionsEndpointSpec]{
			runtimeFilterConfig: f.runtimeFilterConfig,
			tracer:              f.tracing.CompletionTracer(),
		}
	case embeddingsEndpoint:
		f.typedFilter = &routerFilterTyped[openai.EmbeddingRequest, openai.EmbeddingResponse, struct{}, endpointspec.EmbeddingsEndpointSpec]{
			runtimeFilterConfig: f.runtimeFilterConfig,
			tracer:              f.tracing.EmbeddingsTracer(),
		}
	case imagesGenerationsEndpoint:
		f.typedFilter = &routerFilterTyped[openai.ImageGenerationRequest, openai.ImageGenerationResponse, struct{}, endpointspec.ImageGenerationEndpointSpec]{
			runtimeFilterConfig: f.runtimeFilterConfig,
			tracer:              f.tracing.ImageGenerationTracer(),
		}
	case rerankEndpoint:
		f.typedFilter = &routerFilterTyped[cohereschema.RerankV2Request, cohereschema.RerankV2Response, struct{}, endpointspec.RerankEndpointSpec]{
			runtimeFilterConfig: f.runtimeFilterConfig,
			tracer:              f.tracing.RerankTracer(),
		}
	case messagesEndpoint:
		f.typedFilter = &routerFilterTyped[anthropic.MessagesRequest, anthropic.MessagesResponse, anthropic.MessagesStreamChunk, endpointspec.MessagesEndpointSpec]{
			runtimeFilterConfig: f.runtimeFilterConfig,
			tracer:              f.tracing.MessageTracer(),
		}
	case responsesEndpoint:
		f.typedFilter = &routerFilterTyped[openai.ResponseRequest, openai.Response, openai.ResponseStreamEventUnion, endpointspec.ResponsesEndpointSpec]{
			runtimeFilterConfig: f.runtimeFilterConfig,
			tracer:              f.tracing.ResponsesTracer(),
		}
	default:
		e.SendLocalReply(500, nil, []byte("BUG: unsupported endpoint at body parsing: "+fmt.Sprintf("%d", f.endpoint)))
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}
	return f.typedFilter.RequestBody(e, endOfStream)
}

func (f *routerFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) UpstreamTypedFilter() upstreamFilterTypedIface {
	return f.upstreamFilter
}

func (f *routerFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) Stream() bool {
	return f.stream
}

func (f *routerFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) RequestBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.RequestBodyStatus {
	if !endOfStream {
		if sdk.LogDebugEnabled {
			sdk.Log(sdk.LogLevelDebug,
				"router filter: waiting for end of stream to parse request body")
		}
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}
	b, ok := e.GetBufferedRequestBody()
	if !ok {
		e.SendLocalReply(400, nil, []byte("failed to read request body"))
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}
	raw, err := io.ReadAll(b)
	if err != nil {
		e.SendLocalReply(400, nil, []byte("failed to read request body: "+err.Error()))
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}

	f.originalRequestBodyRaw = raw
	var maybeMutatedOriginalBodyRaw []byte
	f.originalModel, f.originalRequestBody, f.stream, maybeMutatedOriginalBodyRaw, err = f.ep.ParseBody(raw, len(f.runtimeFilterConfig.RequestCosts) > 0)
	if err != nil {
		e.SendLocalReply(400, nil, []byte("failed to parse request body: "+err.Error()))
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}
	if len(maybeMutatedOriginalBodyRaw) > 0 {
		f.originalRequestBodyRaw = maybeMutatedOriginalBodyRaw
		f.forceBodyMutation = true
	}
	if !e.SetRequestHeader(internalapi.ModelNameHeaderKeyDefault, []byte(f.originalModel)) {
		e.SendLocalReply(500, nil, []byte("failed to set model name header"))
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}

	f.originalRequestHeaders = multiValueHeadersToSingleValue(e.GetRequestHeaders())

	f.span = f.tracer.StartSpanAndInjectHeaders(
		context.Background(),
		f.originalRequestHeaders,
		&headerMutationCarrier{e: e},
		f.originalRequestBody,
		f.originalRequestBodyRaw,
	)
	return sdk.RequestBodyStatusContinue
}

// ResponseHeaders implements [sdk.HTTPFilter].
func (f *routerFilter) ResponseHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.ResponseHeadersStatus {
	typedFilter := f.typedFilter.UpstreamTypedFilter()
	if typedFilter == nil {
		return sdk.ResponseHeadersStatusContinue
	}
	if err := typedFilter.ResponseHeaders(e, false); err != nil {
		sdk.Log(sdk.LogLevelError, "response headers error: %v", err)
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.ResponseHeadersStatusStopIteration
	}
	status, _ := e.GetResponseHeader(":status")
	f.success = status == "200"
	if sdk.LogDebugEnabled {
		sdk.Log(sdk.LogLevelDebug,
			"upstream response headers processed, :status header=%v", status)
	}
	if f.typedFilter.Stream() && f.success {
		return sdk.ResponseHeadersStatusContinue
	}
	return sdk.ResponseHeadersStatusStopIteration
}

// ResponseBody implements [sdk.HTTPFilter].
func (f *routerFilter) ResponseBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.ResponseBodyStatus {
	typedFilter := f.typedFilter.UpstreamTypedFilter()
	if typedFilter == nil {
		return sdk.ResponseBodyStatusContinue
	}
	if (!f.success || !f.typedFilter.Stream()) && !endOfStream {
		// Buffer the entire body if not streaming.
		if sdk.LogDebugEnabled {
			sdk.Log(sdk.LogLevelDebug,
				"upstream response body buffering as not streaming or error response: success: %v, stream: %v, endOfStream: %v",
				f.success, f.typedFilter.Stream(), endOfStream)
		}
		return sdk.ResponseBodyStatusStopIterationAndBuffer
	}
	if !f.success {
		if sdk.LogDebugEnabled {
			sdk.Log(sdk.LogLevelDebug, "upstream response body error handling started, endOfStream: %v", endOfStream)
		}
		if err := typedFilter.ResponseBodyOnError(e); err != nil {
			sdk.Log(sdk.LogLevelError, "response body on error handling failed: %v", err)
			e.SendLocalReply(500, nil, []byte("internal server error"))
		}
		return sdk.ResponseBodyStatusContinue
	}

	if err := typedFilter.ResponseBody(e, endOfStream); err != nil {
		sdk.Log(sdk.LogLevelError, "response body on error handling failed: %v", err)
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.ResponseBodyStatusStopIterationAndBuffer
	}
	if sdk.LogDebugEnabled {
		sdk.Log(sdk.LogLevelDebug, "upstream response body processed, endOfStream: %v",
			endOfStream)
	}
	return sdk.ResponseBodyStatusContinue
}

func (f *routerFilter) OnDestroy() {
	f.routerFilters.Lock.Lock()
	delete(f.routerFilters.Filters, f.internalRequestID)
	f.routerFilters.Lock.Unlock()
	if sdk.LogDebugEnabled {
		sdk.Log(sdk.LogLevelDebug,
			"router filter: cleaned up filter for internal request ID %s", f.internalRequestID)
	}
}

// handleModelsEndpoint handles the /v1/models endpoint by returning the list of declared models in the filter configuration.
//
// This is called on request headers phase.
func (f *routerFilter) handleModelsEndpoint(e sdk.EnvoyHTTPFilter) sdk.RequestHeadersStatus {
	config := f.runtimeFilterConfig
	models := openai.ModelList{
		Object: "list",
		Data:   make([]openai.Model, 0, len(config.DeclaredModels)),
	}
	for _, m := range config.DeclaredModels {
		models.Data = append(models.Data, openai.Model{
			ID:      m.Name,
			Object:  "model",
			OwnedBy: m.OwnedBy,
			Created: openai.JSONUNIXTime(m.CreatedAt),
		})
	}

	body, _ := json.Marshal(models)
	e.SendLocalReply(200, [][2]string{
		{"content-type", "application/json"},
	}, body)
	return sdk.RequestHeadersStatusStopIteration
}

// multiValueHeadersToSingleValue converts a map of headers with multiple values to a map of headers with single values by taking the first value for each header.
//
// TODO: this is purely for feature parity with the old filter where we ignore the case of multiple header values.
func multiValueHeadersToSingleValue(headers map[string][]string) map[string]string {
	singleValueHeaders := make(map[string]string, len(headers))
	for k, v := range headers {
		singleValueHeaders[k] = v[0]
	}
	return singleValueHeaders
}

// headerMutationCarrier implements [propagation.TextMapCarrier].
type headerMutationCarrier struct {
	e sdk.EnvoyHTTPFilter
}

// Get implements the same method as defined on propagation.TextMapCarrier.
func (c *headerMutationCarrier) Get(string) string {
	panic("unexpected as this carrier is write-only for injection")
}

// Set adds a key-value pair to the HeaderMutation.
func (c *headerMutationCarrier) Set(key, value string) {
	_ = c.e.SetResponseHeader(key, []byte(value))
}

// Keys implements the same method as defined on propagation.TextMapCarrier.
func (c *headerMutationCarrier) Keys() []string {
	panic("unexpected as this carrier is write-only for injection")
}
