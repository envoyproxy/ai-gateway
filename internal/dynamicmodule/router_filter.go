// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dynamicmodule

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/dynamicmodule/sdk"
	"github.com/envoyproxy/ai-gateway/internal/endpointspec"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
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
		logger           *slog.Logger
	}
	// routerFilter implements [sdk.HTTPFilter].
	routerFilter[ReqT, RespT, RespChunkT any, EndpointSpec endpointspec.Spec[ReqT, RespT, RespChunkT]] struct {
		logger          *slog.Logger
		debugLogEnabled bool
		// runtimeFilterConfig is the snapshot of the runtime filter configuration at the time of filter creation.
		runtimeFilterConfig *filterapi.RuntimeConfig
		// tracing is the tracing implementation inherited from the environment.
		tracing      tracingapi.Tracing
		attemptCount int
		// endpoint is the endpoint that the current request is targeting.
		endpoint endpoint
		// Inherited from the environment.
		routerFilters *RouterFilters
		// This indicates whether the request was 200 on the response headers phase.
		success           bool
		internalRequestID string

		originalRequestHeaders map[string]string
		originalRequestBody    *ReqT
		originalRequestBodyRaw *bytes.Buffer
		originalModel          internalapi.OriginalModel
		forceBodyMutation      bool
		stream                 bool
		tracer                 tracingapi.RequestTracer[ReqT, RespT, RespChunkT]
		span                   tracingapi.Span[RespT, RespChunkT]

		upstreamFilter *upstreamFilter[ReqT, RespT, RespChunkT, EndpointSpec]
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
		logger:           env.Logger.With(slog.String("component", "router_filter")),
	}
}

// NewFilter implements [sdk.HTTPFilterConfig].
func (f *routerFilterConfig) NewFilter(e sdk.EnvoyHTTPFilter) sdk.HTTPFilter {
	if f.env.DebugLogEnabled {
		f.logger.Debug("router filter NewFilter called")
	}
	p, ok := e.GetRequestHeader(":path") // The :path pseudo header is always present.
	if !ok {
		f.logger.Error("missing :path header in request")
		e.SendLocalReply(400, nil, []byte("missing :path header"))
		return sdk.NoopHTTPFilter{}
	}
	// Strip query parameters for processor lookup.
	if queryIndex := strings.Index(p, "?"); queryIndex != -1 {
		p = p[:queryIndex]
	}
	ep, ok := f.prefixToEndpoint[p]
	if !ok {
		if f.env.DebugLogEnabled {
			f.logger.Debug("unsupported path requested", slog.String("path", p))
		}
		e.SendLocalReply(404, nil, []byte(fmt.Sprintf("unsupported path: %s", p)))
		return sdk.NoopHTTPFilter{}
	}
	if f.env.DebugLogEnabled {
		f.logger.Debug("continuing to request body phase for endpoint",
			slog.String("endpoint", ep.String()))
	}
	switch ep {
	case chatCompletionsEndpoint:
		return &routerFilter[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk, endpointspec.ChatCompletionsEndpointSpec]{
			endpoint:            ep,
			runtimeFilterConfig: *f.fcr, // This is racy but we don't care.
			tracing:             f.env.Tracing,
			routerFilters:       f.env.RouterFilters,
			logger:              f.logger,
			debugLogEnabled:     f.env.DebugLogEnabled,
			tracer:              f.env.Tracing.ChatCompletionTracer(),
		}
	case completionsEndpoint:
		return &routerFilter[openai.CompletionRequest, openai.CompletionResponse, openai.CompletionResponse, endpointspec.CompletionsEndpointSpec]{
			endpoint:            ep,
			runtimeFilterConfig: *f.fcr,
			tracing:             f.env.Tracing,
			routerFilters:       f.env.RouterFilters,
			logger:              f.logger,
			debugLogEnabled:     f.env.DebugLogEnabled,
			tracer:              f.env.Tracing.CompletionTracer(),
		}
	case embeddingsEndpoint:
		return &routerFilter[openai.EmbeddingRequest, openai.EmbeddingResponse, struct{}, endpointspec.EmbeddingsEndpointSpec]{
			endpoint:            ep,
			runtimeFilterConfig: *f.fcr,
			tracing:             f.env.Tracing,
			routerFilters:       f.env.RouterFilters,
			logger:              f.logger,
			debugLogEnabled:     f.env.DebugLogEnabled,
			tracer:              f.env.Tracing.EmbeddingsTracer(),
		}
	case imagesGenerationsEndpoint:
		return &routerFilter[openai.ImageGenerationRequest, openai.ImageGenerationResponse, struct{}, endpointspec.ImageGenerationEndpointSpec]{
			endpoint:            ep,
			runtimeFilterConfig: *f.fcr,
			tracing:             f.env.Tracing,
			routerFilters:       f.env.RouterFilters,
			logger:              f.logger,
			debugLogEnabled:     f.env.DebugLogEnabled,
			tracer:              f.env.Tracing.ImageGenerationTracer(),
		}
	case rerankEndpoint:
		return &routerFilter[cohereschema.RerankV2Request, cohereschema.RerankV2Response, struct{}, endpointspec.RerankEndpointSpec]{
			endpoint:            ep,
			runtimeFilterConfig: *f.fcr,
			tracing:             f.env.Tracing,
			routerFilters:       f.env.RouterFilters,
			logger:              f.logger,
			debugLogEnabled:     f.env.DebugLogEnabled,
			tracer:              f.env.Tracing.RerankTracer(),
		}
	case messagesEndpoint:
		return &routerFilter[anthropic.MessagesRequest, anthropic.MessagesResponse, anthropic.MessagesStreamChunk, endpointspec.MessagesEndpointSpec]{
			endpoint:            ep,
			runtimeFilterConfig: *f.fcr,
			tracing:             f.env.Tracing,
			routerFilters:       f.env.RouterFilters,
			logger:              f.logger,
			debugLogEnabled:     f.env.DebugLogEnabled,
			tracer:              f.env.Tracing.MessageTracer(),
		}
	case responsesEndpoint:
		return &routerFilter[openai.ResponseRequest, openai.Response, openai.ResponseStreamEventUnion, endpointspec.ResponsesEndpointSpec]{
			endpoint:            ep,
			runtimeFilterConfig: *f.fcr,
			tracing:             f.env.Tracing,
			routerFilters:       f.env.RouterFilters,
			logger:              f.logger,
			debugLogEnabled:     f.env.DebugLogEnabled,
			tracer:              f.env.Tracing.ResponsesTracer(),
		}
	case modelsEndpoint:
		handleModelsEndpoint(e, *f.fcr)
		return sdk.NoopHTTPFilter{}
	default:
		e.SendLocalReply(500, nil, []byte("BUG: unsupported endpoint at body parsing: "+ep.String()))
		return nil
	}
}

// Endpoint returns the endpoint that the filter is targeting.
func (f *routerFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) Endpoint() endpoint {
	return f.endpoint
}

// RequestHeaders implements [sdk.HTTPFilter].
func (f *routerFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) RequestHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.RequestHeadersStatus {
	originalReqID, ok := e.GetRequestHeader("x-request-id")
	if !ok {
		e.SendLocalReply(400, nil, []byte("missing x-request-id header"))
		return sdk.RequestHeadersStatusStopIteration
	}
	internalReqID := originalReqID + uuid.NewString()
	e.SetDynamicMetadataString(internalapi.AIGatewayFilterMetadataNamespace, internalRequestIDMetadataKey, internalReqID)
	f.routerFilters.Lock.Lock()
	f.routerFilters.Filters[internalReqID] = f
	f.routerFilters.Lock.Unlock()
	if f.debugLogEnabled {
		f.logger.Debug("registered filter for internal request ID",
			slog.String("internal_request_id", internalReqID),
			slog.String("original_request_id", originalReqID))
	}
	f.internalRequestID = internalReqID
	contentLengthRaw, _ := e.GetRequestHeader("content-length")
	if f.debugLogEnabled {
		f.logger.Debug("request headers received",
			slog.String("content_length", contentLengthRaw),
			slog.String("internal_request_id", internalReqID))
	}
	contentLength, _ := strconv.Atoi(contentLengthRaw)
	f.originalRequestBodyRaw = bytes.NewBuffer(make([]byte, 0, contentLength))
	return sdk.RequestHeadersStatusStopIteration // Do not invoke the subsequent filters but continue to the body phase on this filter.
}

// RequestBody implements [sdk.HTTPFilter].
func (f *routerFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) RequestBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.RequestBodyStatus {
	if b, ok := e.GetReceivedRequestBody(); ok {
		_, err := b.WriteTo(f.originalRequestBodyRaw)
		if err != nil {
			if f.debugLogEnabled {
				f.logger.Debug("failed to buffer request body", slog.String("error", err.Error()))
			}
			e.SendLocalReply(500, nil, []byte("failed to buffer request body: "+err.Error()))
			return sdk.RequestBodyStatusStopIterationAndBuffer
		}
	}

	if !endOfStream {
		if f.debugLogEnabled {
			f.logger.Debug("waiting for end of stream to parse request body")
		}
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}

	var maybeMutatedOriginalBodyRaw []byte
	var ep EndpointSpecT
	var err error
	f.originalModel, f.originalRequestBody, f.stream, maybeMutatedOriginalBodyRaw, err = ep.ParseBody(f.originalRequestBodyRaw.Bytes(), len(f.runtimeFilterConfig.RequestCosts) > 0)
	if err != nil {
		if f.debugLogEnabled {
			f.logger.Debug("failed to parse request body", slog.String("error", err.Error()))
		}
		e.SendLocalReply(400, nil, []byte("failed to parse request body: "+err.Error()))
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}
	if len(maybeMutatedOriginalBodyRaw) > 0 {
		f.originalRequestBodyRaw = bytes.NewBuffer(maybeMutatedOriginalBodyRaw)
		f.forceBodyMutation = true
	}
	if f.debugLogEnabled {
		f.logger.Debug("parsed request body",
			slog.Any("original_model", f.originalModel),
			slog.Bool("stream", f.stream),
			slog.Bool("force_body_mutation", f.forceBodyMutation))
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
		f.originalRequestBodyRaw.Bytes(),
	)
	e.ClearRouteCache()
	return sdk.RequestBodyStatusContinue
}

// ResponseHeaders implements [sdk.HTTPFilter].
func (f *routerFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.ResponseHeadersStatus {
	// Ideally we should handle the upstream specific logic at the upstream filter layer.
	// However, because of the bug in Envoy's upstream filter handling, we cannot do that until it is fixed.
	// So, we embark the upstream filter logic here for now.
	if f.upstreamFilter == nil {
		return sdk.ResponseHeadersStatusContinue
	}
	if err := f.upstreamFilter.responseHeadersImpl(e, false); err != nil {
		f.logger.Error("response headers error", slog.String("error", err.Error()))
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.ResponseHeadersStatusStopIteration
	}
	status, _ := e.GetResponseHeader(":status")
	f.success = status == "200"
	if f.debugLogEnabled {
		f.logger.Debug("upstream response headers processed",
			slog.String(":status", status))
	}
	if f.stream && f.success {
		return sdk.ResponseHeadersStatusContinue
	}
	return sdk.ResponseHeadersStatusStopIteration
}

// ResponseBody implements [sdk.HTTPFilter].
func (f *routerFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.ResponseBodyStatus {
	// Ideally we should handle the upstream specific logic at the upstream filter layer.
	// However, because of the bug in Envoy's upstream filter handling, we cannot do that until it is fixed.
	// So, we embark the upstream filter logic here for now.
	if f.upstreamFilter == nil {
		return sdk.ResponseBodyStatusContinue
	}
	if (!f.success || !f.stream) && !endOfStream {
		// Buffer the entire body if not streaming.
		if f.debugLogEnabled {
			f.logger.Debug("upstream response body buffering as not streaming or error response",
				slog.Bool("success", f.success),
				slog.Bool("stream", f.stream),
				slog.Bool("end_of_stream", endOfStream))
		}
		return sdk.ResponseBodyStatusStopIterationAndBuffer
	}
	if !f.success {
		if f.debugLogEnabled {
			f.logger.Debug("upstream response body error handling started",
				slog.Bool("end_of_stream", endOfStream))
		}
		if err := f.upstreamFilter.ResponseBodyOnError(e); err != nil {
			f.logger.Error("response body on error handling failed", slog.String("error", err.Error()))
			e.SendLocalReply(500, nil, []byte("internal server error"))
		}
		return sdk.ResponseBodyStatusContinue
	}

	if err := f.upstreamFilter.responseBodyImpl(e, endOfStream); err != nil {
		f.logger.Error("response body handling failed", slog.String("error", err.Error()))
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.ResponseBodyStatusStopIterationAndBuffer
	}
	if f.debugLogEnabled {
		f.logger.Debug("upstream response body processed",
			slog.Bool("end_of_stream", endOfStream))
	}
	return sdk.ResponseBodyStatusContinue
}

func (f *routerFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) OnDestroy() {
	f.routerFilters.Lock.Lock()
	delete(f.routerFilters.Filters, f.internalRequestID)
	f.routerFilters.Lock.Unlock()
	if f.debugLogEnabled {
		f.logger.Debug("cleaned up filter for internal request ID",
			slog.String("internal_request_id", f.internalRequestID))
	}
}

// handleModelsEndpoint handles the /v1/models endpoint by returning the list of declared models in the filter configuration.
//
// This is called on request headers phase.
func handleModelsEndpoint(e sdk.EnvoyHTTPFilter, config *filterapi.RuntimeConfig) sdk.RequestHeadersStatus {
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
