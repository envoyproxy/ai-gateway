// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"strconv"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ai-gateway/internal/bodymutator"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

type (
	routerProcessor[ReqT, RespT, RespChunkT any, EndpointHandlerT endpointHandler[ReqT, RespT, RespChunkT]] struct {
		eh EndpointHandlerT

		passThroughProcessor
		// upstreamFilter is the upstream filter that is used to process the request at the upstream filter.
		// This will be updated when the request is retried.
		//
		// On the response handling path, we don't need to do any operation until successful, so we use the implementation
		// of the upstream filter to handle the response at the router filter.
		//
		// TODO: this is a bit of a hack and dirty workaround, so revert this to a cleaner design later.
		upstreamFilter *upstreamProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]
		logger         *slog.Logger
		config         *filterapi.RuntimeConfig
		requestHeaders map[string]string
		// originalRequestBody is the original request body that is passed to the upstream filter.
		// This is used to perform the transformation of the request body on the original input
		// when the request is retried.
		originalRequestBody    *ReqT
		originalRequestBodyRaw []byte
		originalModel          internalapi.OriginalModel
		forceBodyMutation      bool
		// tracer is the tracer used for requests.
		tracer tracing.RequestTracer[ReqT, RespT, RespChunkT]
		// span is the tracing span for this request, created in ProcessRequestBody.
		span tracing.Span[RespT, RespChunkT]
		// upstreamFilterCount is the number of upstream filters that have been processed.
		// This is used to determine if the request is a retry request.
		upstreamFilterCount int
		stream              bool
	}
	upstreamProcessor[ReqT, RespT, RespChunkT any, EndpointHandlerT endpointHandler[ReqT, RespT, RespChunkT]] struct {
		parent *routerProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]

		logger            *slog.Logger
		requestHeaders    map[string]string
		responseHeaders   map[string]string
		responseEncoding  string
		translator        translator.Translator[ReqT, tracing.Span[RespT, RespChunkT]]
		modelNameOverride internalapi.ModelNameOverride
		headerMutator     *headermutator.HeaderMutator
		bodyMutator       *bodymutator.BodyMutator
		backendName       string
		handler           filterapi.BackendAuthHandler
		// cost is the cost of the request that is accumulated during the processing of the response.
		costs metrics.TokenUsage
		// metrics tracking.
		metrics metrics.Metrics
	}
)

func newRouterProcessor[ReqT, RespT, RespChunkT any, EndpointHandlerT endpointHandler[ReqT, RespT, RespChunkT]](
	config *filterapi.RuntimeConfig,
	requestHeaders map[string]string,
	logger *slog.Logger,
	tracer tracing.RequestTracer[ReqT, RespT, RespChunkT],
) *routerProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT] {
	return &routerProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]{
		config:            config,
		requestHeaders:    requestHeaders,
		logger:            logger,
		tracer:            tracer,
		forceBodyMutation: false,
	}
}

func newUpstreamProcessor[ReqT, RespT, RespChunkT any, EndpointHandlerT endpointHandler[ReqT, RespT, RespChunkT]](
	reqHeader map[string]string, metrics metrics.Metrics,
	logger *slog.Logger,
) *upstreamProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT] {
	return &upstreamProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]{
		requestHeaders: reqHeader,
		metrics:        metrics,
		logger:         logger,
	}
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (r *routerProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// r.upstreamFilter can be nil.
	if r.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		return r.upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return r.passThroughProcessor.ProcessResponseHeaders(ctx, headerMap)
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (r *routerProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (resp *extprocv3.ProcessingResponse, err error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// r.upstreamFilter can be nil.
	if r.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		resp, err = r.upstreamFilter.ProcessResponseBody(ctx, body)
	} else {
		resp, err = r.passThroughProcessor.ProcessResponseBody(ctx, body)
	}
	return
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (r *routerProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]) ProcessRequestBody(ctx context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	originalModel, body, stream, mutatedOriginalBody, err := r.eh.ParseBody(rawBody.Body, len(r.config.RequestCosts) > 0)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}
	if mutatedOriginalBody != nil {
		r.originalRequestBodyRaw = mutatedOriginalBody
		r.forceBodyMutation = true
	} else {
		r.originalRequestBodyRaw = rawBody.Body
	}

	r.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = originalModel

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		// Set the original model to the request header with the key `x-ai-eg-model`.
		Header: &corev3.HeaderValue{Key: internalapi.ModelNameHeaderKeyDefault, RawValue: []byte(originalModel)},
	}, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: originalPathHeader, RawValue: []byte(r.requestHeaders[":path"])},
	})
	r.originalModel = originalModel
	r.originalRequestBody = body
	r.stream = stream

	// Tracing may need to inject headers, so create a header mutation here.
	headerMutation := &extprocv3.HeaderMutation{
		SetHeaders: additionalHeaders,
	}
	r.span = r.tracer.StartSpanAndInjectHeaders(
		ctx,
		r.requestHeaders,
		&headerMutationCarrier{m: headerMutation},
		body,
		rawBody.Body,
	)

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation:  headerMutation,
					ClearRouteCache: true,
				},
			},
		},
	}, nil
}

func (u *upstreamProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]) onRetry() bool {
	return u.parent.upstreamFilterCount > 1
}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
//
// At the upstream filter, we already have the original request body at request headers phase.
// So, we simply do the translation and upstream auth at this stage, and send them back to Envoy
// with the status CONTINUE_AND_REPLACE. This allows Envoy to not send the request body again
// to the extproc.
func (u *upstreamProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]) ProcessRequestHeaders(ctx context.Context, _ *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			u.metrics.RecordRequestCompletion(ctx, false, u.requestHeaders)
		}
	}()

	// Start tracking metrics for this request.
	u.metrics.StartRequest(u.requestHeaders)
	// Set the original model from the request body before any overrides
	u.metrics.SetOriginalModel(u.parent.originalModel)
	// Set the request model for metrics from the original model or override if applied.
	reqModel := cmp.Or(u.requestHeaders[internalapi.ModelNameHeaderKeyDefault], u.parent.originalModel)
	u.metrics.SetRequestModel(reqModel)

	// We force the body mutation in the following cases:
	// * The request is a retry request because the body mutation might have happened the previous iteration.
	// * The request is a streaming request, and the IncludeUsage option is set to false since we need to ensure that
	//	the token usage is calculated correctly without being bypassed.
	forceBodyMutation := u.onRetry() || u.parent.forceBodyMutation
	newHeaders, newBody, err := u.translator.RequestBody(u.parent.originalRequestBodyRaw, u.parent.originalRequestBody, forceBodyMutation)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}

	headerMutation, bodyMutation := mutationsFromTranslationResult(newHeaders, newBody)

	// Apply header mutations from the route and also restore original headers on retry.
	if h := u.headerMutator; h != nil {
		sets, removes := u.headerMutator.Mutate(u.requestHeaders, u.onRetry())
		headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, removes...)
		for _, hdr := range sets {
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
				Header: &corev3.HeaderValue{
					Key:      hdr.Key(),
					RawValue: []byte(hdr.Value()),
				},
			})
		}
	}

	// Apply body mutations from the route and also restore original body on retry.
	bodyMutation = applyBodyMutation(u.bodyMutator, bodyMutation,
		u.parent.originalRequestBodyRaw, forceBodyMutation, u.logger)

	// Ensure bodyMutation is not nil for subsequent processing
	if bodyMutation == nil {
		bodyMutation = &extprocv3.BodyMutation{}
	}

	for _, h := range headerMutation.SetHeaders {
		u.requestHeaders[h.Header.Key] = string(h.Header.RawValue)
	}

	if h := u.handler; h != nil {
		var hdrs []internalapi.Header
		hdrs, err = h.Do(ctx, u.requestHeaders, bodyMutation.GetBody())
		if err != nil {
			return nil, fmt.Errorf("failed to do auth request: %w", err)
		}
		for _, h := range hdrs {
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
				Header:       &corev3.HeaderValue{Key: h.Key(), RawValue: []byte(h.Value())},
			})
		}
	}

	var dm *structpb.Struct
	if bm := bodyMutation.GetBody(); bm != nil {
		dm = buildContentLengthDynamicMetadataOnRequest(len(bm))
	}
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: headerMutation, BodyMutation: bodyMutation,
					Status: extprocv3.CommonResponse_CONTINUE_AND_REPLACE,
				},
			},
		},
		DynamicMetadata: dm,
	}, nil
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (u *upstreamProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	panic("BUG: ProcessRequestBody should not be called in the upstream filter")
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (u *upstreamProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]) ProcessResponseHeaders(ctx context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			u.metrics.RecordRequestCompletion(ctx, false, u.requestHeaders)
		}
	}()

	u.responseHeaders = headersToMap(headers)
	if enc := u.responseHeaders["content-encoding"]; enc != "" {
		u.responseEncoding = enc
	}
	newHeaders, err := u.translator.ResponseHeaders(u.responseHeaders)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response headers: %w", err)
	}
	var mode *extprocv3http.ProcessingMode
	if u.parent.stream && u.responseHeaders[":status"] == "200" {
		// We only stream the response if the status code is 200 and the response is a stream.
		mode = &extprocv3http.ProcessingMode{ResponseBodyMode: extprocv3http.ProcessingMode_STREAMED}
	}
	headerMutation, _ := mutationsFromTranslationResult(newHeaders, nil)
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{
		ResponseHeaders: &extprocv3.HeadersResponse{
			Response: &extprocv3.CommonResponse{HeaderMutation: headerMutation},
		},
	}, ModeOverride: mode}, nil
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (u *upstreamProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	recordRequestCompletionErr := false
	defer func() {
		if err != nil || recordRequestCompletionErr {
			u.metrics.RecordRequestCompletion(ctx, false, u.requestHeaders)
			return
		}
		if body.EndOfStream {
			u.metrics.RecordRequestCompletion(ctx, true, u.requestHeaders)
		}
	}()

	// Decompress the body if needed using common utility.
	decodingResult, err := decodeContentIfNeeded(body.Body, u.responseEncoding)
	if err != nil {
		return nil, err
	}

	// Assume all responses have a valid status code header.
	if code, _ := strconv.Atoi(u.responseHeaders[":status"]); !isGoodStatusCode(code) {
		var newHeaders []internalapi.Header
		var newBody []byte
		newHeaders, newBody, err = u.translator.ResponseError(u.responseHeaders, decodingResult.reader)
		if err != nil {
			return nil, fmt.Errorf("failed to transform response error: %w", err)
		}
		headerMutation, bodyMutation := mutationsFromTranslationResult(newHeaders, newBody)
		if u.parent.span != nil {
			b := bodyMutation.GetBody()
			if b == nil {
				b = body.Body
			}
			u.parent.span.EndSpanOnError(code, b)
		}
		// Mark so the deferred handler records failure.
		recordRequestCompletionErr = true
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{
					Response: &extprocv3.CommonResponse{
						HeaderMutation: headerMutation,
						BodyMutation:   bodyMutation,
					},
				},
			},
		}, nil
	}

	newHeaders, newBody, tokenUsage, responseModel, err := u.translator.ResponseBody(u.responseHeaders, decodingResult.reader, body.EndOfStream, u.parent.span)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}
	headerMutation, bodyMutation := mutationsFromTranslationResult(newHeaders, newBody)

	// Remove content-encoding header if original body encoded but was mutated in the processor.
	headerMutation = removeContentEncodingIfNeeded(headerMutation, bodyMutation, decodingResult.isEncoded)

	resp := &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseBody{
			ResponseBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: headerMutation,
					BodyMutation:   bodyMutation,
				},
			},
		},
	}

	// Translator reports the latest cumulative token usage which we use to override existing costs.
	u.costs.Override(tokenUsage)

	// Set the response model for metrics
	u.metrics.SetResponseModel(responseModel)

	// Record metrics.
	if u.parent.stream {
		// Token latency is only recorded for streaming responses, otherwise it doesn't make sense since
		// these metrics are defined as a difference between the two output events.
		out, _ := u.costs.OutputTokens()
		u.metrics.RecordTokenLatency(ctx, out, body.EndOfStream, u.requestHeaders)
		// Emit usage once at end-of-stream using final totals.
		if body.EndOfStream {
			u.metrics.RecordTokenUsage(ctx, u.costs, u.requestHeaders)
		}
	} else {
		u.metrics.RecordTokenUsage(ctx, u.costs, u.requestHeaders)
	}

	if body.EndOfStream && len(u.parent.config.RequestCosts) > 0 {
		metadata, err := buildDynamicMetadata(u.parent.config, &u.costs, u.requestHeaders, u.backendName)
		if err != nil {
			return nil, fmt.Errorf("failed to build dynamic metadata: %w", err)
		}
		if u.parent.stream {
			// Adding token latency information to metadata.
			u.mergeWithTokenLatencyMetadata(metadata)
		}
		resp.DynamicMetadata = metadata
	}

	if body.EndOfStream && u.parent.span != nil {
		u.parent.span.EndSpan()
	}
	return resp, nil
}

// SetBackend implements [Processor.SetBackend].
func (u *upstreamProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler filterapi.BackendAuthHandler, routeProcessor Processor) (err error) {
	defer func() {
		if err != nil {
			u.metrics.RecordRequestCompletion(ctx, false, u.requestHeaders)
		}
	}()
	rp, ok := routeProcessor.(*routerProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT])
	if !ok {
		panic("BUG: expected routeProcessor to be of type *chatCompletionProcessorRouterFilter")
	}
	rp.upstreamFilterCount++
	u.metrics.SetBackend(b)
	u.modelNameOverride = b.ModelNameOverride
	u.backendName = b.Name
	u.handler = backendHandler
	u.headerMutator = headermutator.NewHeaderMutator(b.HeaderMutation, rp.requestHeaders)
	u.bodyMutator = bodymutator.NewBodyMutator(b.BodyMutation, rp.originalRequestBodyRaw)
	// Header-derived labels/CEL must be able to see the overridden request model.
	if u.modelNameOverride != "" {
		u.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = u.modelNameOverride
	}
	rp.upstreamFilter = u
	u.parent = rp

	u.translator, err = u.parent.eh.GetTranslator(b.Schema, u.modelNameOverride)
	if err != nil {
		return fmt.Errorf("failed to create translator for backend %s: %w", b.Name, err)
	}
	return
}

func (u *upstreamProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT]) mergeWithTokenLatencyMetadata(metadata *structpb.Struct) {
	timeToFirstTokenMs := u.metrics.GetTimeToFirstTokenMs()
	interTokenLatencyMs := u.metrics.GetInterTokenLatencyMs()
	innerVal := metadata.Fields[internalapi.AIGatewayFilterMetadataNamespace].GetStructValue()
	if innerVal == nil {
		innerVal = &structpb.Struct{Fields: map[string]*structpb.Value{}}
		metadata.Fields[internalapi.AIGatewayFilterMetadataNamespace] = structpb.NewStructValue(innerVal)
	}
	innerVal.Fields["token_latency_ttft"] = &structpb.Value{Kind: &structpb.Value_NumberValue{NumberValue: timeToFirstTokenMs}}
	innerVal.Fields["token_latency_itl"] = &structpb.Value{Kind: &structpb.Value_NumberValue{NumberValue: interTokenLatencyMs}}
}

// buildContentLengthDynamicMetadataOnRequest builds dynamic metadata for the request with content length.
//
// This is necessary to ensure that the content length can be set after the extproc filter has processed the request,
// which will happen in the header mutation filter.
//
// This is needed since the content length header is unconditionally cleared by Envoy as we use REPLACE_AND_CONTINUE
// processing mode in the request headers phase at upstream filter. This is sort of a workaround, and it is necessary
// for now.
func buildContentLengthDynamicMetadataOnRequest(contentLength int) *structpb.Struct {
	metadata := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			internalapi.AIGatewayFilterMetadataNamespace: {
				Kind: &structpb.Value_StructValue{
					StructValue: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							"content_length": {
								Kind: &structpb.Value_NumberValue{NumberValue: float64(contentLength)},
							},
						},
					},
				},
			},
		},
	}
	return metadata
}

// buildDynamicMetadata creates metadata for rate limiting and cost tracking.
// This function is called by the upstream filter only at the end of the stream (body.EndOfStream=true)
// when the response is successfully completed. It is not called for failed requests or partial responses.
// The metadata includes token usage costs and model information for downstream processing.
func buildDynamicMetadata(config *filterapi.RuntimeConfig, costs *metrics.TokenUsage, requestHeaders map[string]string, backendName string) (*structpb.Struct, error) {
	metadata := make(map[string]*structpb.Value, len(config.RequestCosts)+2)
	for i := range config.RequestCosts {
		rc := &config.RequestCosts[i]
		var cost uint32
		switch rc.Type {
		case filterapi.LLMRequestCostTypeInputToken:
			cost, _ = costs.InputTokens()
		case filterapi.LLMRequestCostTypeCachedInputToken:
			cost, _ = costs.CachedInputTokens()
		case filterapi.LLMRequestCostTypeOutputToken:
			cost, _ = costs.OutputTokens()
		case filterapi.LLMRequestCostTypeTotalToken:
			cost, _ = costs.TotalTokens()
		case filterapi.LLMRequestCostTypeCEL:
			in, _ := costs.InputTokens()
			cachedIn, _ := costs.CachedInputTokens()
			out, _ := costs.OutputTokens()
			total, _ := costs.TotalTokens()
			costU64, err := llmcostcel.EvaluateProgram(
				rc.CELProg,
				requestHeaders[internalapi.ModelNameHeaderKeyDefault],
				backendName,
				in,
				cachedIn,
				out,
				total,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate CEL expression: %w", err)
			}
			cost = uint32(costU64) //nolint:gosec
		default:
			return nil, fmt.Errorf("unknown request cost kind: %s", rc.Type)
		}
		metadata[rc.MetadataKey] = &structpb.Value{Kind: &structpb.Value_NumberValue{NumberValue: float64(cost)}}
	}

	// Add the actual request model that was used (after any backend overrides were applied).
	// At this point, the header contains the final model that was sent to the upstream.
	actualModel := requestHeaders[internalapi.ModelNameHeaderKeyDefault]
	metadata["model_name_override"] = &structpb.Value{Kind: &structpb.Value_StringValue{StringValue: actualModel}}

	if backendName != "" {
		metadata["backend_name"] = &structpb.Value{Kind: &structpb.Value_StringValue{StringValue: backendName}}
	}

	if len(metadata) == 0 {
		return nil, nil
	}

	return &structpb.Struct{
		Fields: map[string]*structpb.Value{
			internalapi.AIGatewayFilterMetadataNamespace: {
				Kind: &structpb.Value_StructValue{
					StructValue: &structpb.Struct{Fields: metadata},
				},
			},
		},
	}, nil
}
