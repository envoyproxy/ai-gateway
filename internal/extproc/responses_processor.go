// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/extproc/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// ResponsesProcessorFactory returns a factory method to instantiate the responses processor.
func ResponsesProcessorFactory(rm metrics.ResponsesMetrics) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, tracing tracing.Tracing, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("processor", "responses", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &responsesProcessorRouterFilter{
				config:         config,
				tracer:         tracing.ResponsesTracer(),
				requestHeaders: requestHeaders,
				logger:         logger,
			}, nil
		}
		return &responsesProcessorUpstreamFilter{
			config:         config,
			requestHeaders: requestHeaders,
			logger:         logger,
			metrics:        rm,
		}, nil
	}
}

// responsesProcessorRouterFilter implements [Processor] for the `/v1/responses` endpoint.
//
// This is primarily used to select the route for the request based on the model name.
type responsesProcessorRouterFilter struct {
	passThroughProcessor
	// upstreamFilter is the upstream filter that is used to process the request at the upstream filter.
	// This will be updated when the request is retried.
	//
	// On the response handling path, we don't need to do any operation until successful, so we use the implementation
	// of the upstream filter to handle the response at the router filter.
	//
	// TODO: this is a bit of a hack and dirty workaround, so revert this to a cleaner design later.
	upstreamFilter Processor
	logger         *slog.Logger
	config         *processorConfig
	requestHeaders map[string]string
	// originalRequestBody is the original request body that is passed to the upstream filter.
	// This is used to perform the transformation of the request body on the original input
	// when the request is retried.
	originalRequestBody    *openai.ResponseRequest
	originalRequestBodyRaw []byte
	// tracer is the tracer used for requests.
	tracer tracing.ResponsesTracer
	// span is the tracing span for this request, created in ProcessRequestBody.
	span tracing.ResponsesSpan
	// upstreamFilterCount is the number of upstream filters that have been processed.
	// This is used to determine if the request is a retry request.
	upstreamFilterCount int
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (r *responsesProcessorRouterFilter) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// r.upstreamFilter can be nil.
	if r.upstreamFilter != nil {
		return r.upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return r.passThroughProcessor.ProcessResponseHeaders(ctx, headerMap)
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (r *responsesProcessorRouterFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (resp *extprocv3.ProcessingResponse, err error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// r.upstreamFilter can be nil.
	if r.upstreamFilter != nil {
		resp, err = r.upstreamFilter.ProcessResponseBody(ctx, body)
	} else {
		resp, err = r.passThroughProcessor.ProcessResponseBody(ctx, body)
	}
	return
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (r *responsesProcessorRouterFilter) ProcessRequestBody(ctx context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	// Parse the request body to extract the ResponseRequest
	modelName, body, err := parseOpenAIResponseBody(rawBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

	// Set the model name in the request headers for routing
	r.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = modelName

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		// Set the original model to the request header with the key `x-ai-eg-model`.
		Header: &corev3.HeaderValue{Key: internalapi.ModelNameHeaderKeyDefault, RawValue: []byte(modelName)},
	}, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: originalPathHeader, RawValue: []byte(r.requestHeaders[":path"])},
	})
	r.originalRequestBody = body
	r.originalRequestBodyRaw = rawBody.Body

	// Tracing may need to inject headers, so create a header mutation here.
	headerMutation := &extprocv3.HeaderMutation{
		SetHeaders: additionalHeaders,
	}
	r.span = r.tracer.StartSpanAndInjectHeaders(
		ctx,
		r.requestHeaders,
		headerMutation,
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

// responsesProcessorUpstreamFilter implements [Processor] for the `/v1/responses` endpoint at the upstream filter level.
//
// This handles the request and response translation for different backend providers.
// This is created per retry and handles the translation as well as the authentication of the request
type responsesProcessorUpstreamFilter struct {
	logger                 *slog.Logger
	config                 *processorConfig
	requestHeaders         map[string]string
	responseHeaders        map[string]string
	responseEncoding       string
	modelNameOverride      internalapi.ModelNameOverride
	backendName            string
	handler                backendauth.Handler
	headerMutator          *headermutator.HeaderMutator
	originalRequestBodyRaw []byte
	originalRequestBody    *openai.ResponseRequest
	translator             translator.OpenAIResponsesTranslator
	// onRetry is true if this is a retry request at the upstream filter.
	onRetry bool
	// costs tracks token usage accumulated during response processing.
	costs translator.LLMTokenUsage
	// metrics tracking.
	metrics metrics.ResponsesMetrics
	// stream is set to true if the request is a streaming request.
	stream bool
	// span is the tracing span for this request, inherited from the router filter.
	span tracing.ResponsesSpan
}

// selectTranslator selects the translator based on the output schema.
func (u *responsesProcessorUpstreamFilter) selectTranslator(out filterapi.VersionedAPISchema) error {
	switch out.Name {
	case filterapi.APISchemaOpenAI:
		u.translator = translator.NewResponsesOpenAIToOpenAITranslator(out.Version, u.modelNameOverride)
	// TODO: Add support for other backends (Azure, AWS Bedrock, etc.)
	// case filterapi.APISchemaAzureOpenAI:
	//     u.translator = translator.NewResponsesOpenAIToAzureOpenAITranslator(out.Version, u.modelNameOverride)
	default:
		return fmt.Errorf("unsupported API schema for Responses API: backend=%s", out)
	}
	return nil
}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
//
// At the upstream filter, we already have the original request body at request headers phase.
// So, we simply do the translation and upstream auth at this stage, and send them back to Envoy
// with the status CONTINUE_AND_REPLACE. This allows Envoy to not send the request body again
// to the extproc.
func (u *responsesProcessorUpstreamFilter) ProcessRequestHeaders(ctx context.Context, _ *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			u.metrics.RecordRequestCompletion(ctx, false, u.requestHeaders)
			u.logger.Error("failed to process request headers", slog.String("error", err.Error()))
		}
	}()

	// Start tracking metrics for this request.
	u.metrics.StartRequest(u.requestHeaders)
	// Set the original model from the request body before any overrides
	u.metrics.SetOriginalModel(u.originalRequestBody.Model)
	// Set the request model for metrics from the original model or override if applied.
	reqModel := cmp.Or(u.requestHeaders[internalapi.ModelNameHeaderKeyDefault], u.originalRequestBody.Model)
	u.metrics.SetRequestModel(reqModel)

	// We force the body mutation if this is a retry request
	// * The request is a retry request because the body mutation might have happened in the previous iteration.
	forceBodyMutation := u.onRetry
	headerMutation, bodyMutation, err := u.translator.RequestBody(u.originalRequestBodyRaw, u.originalRequestBody, forceBodyMutation)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}

	if headerMutation == nil {
		headerMutation = &extprocv3.HeaderMutation{}
	}

	// Apply header mutations from the route and also restore original headers on retry.
	if h := u.headerMutator; h != nil {
		if hm := u.headerMutator.Mutate(u.requestHeaders, u.onRetry); hm != nil {
			headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, hm.RemoveHeaders...)
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, hm.SetHeaders...)
		}
	}

	for _, h := range headerMutation.SetHeaders {
		u.requestHeaders[h.Header.Key] = string(h.Header.RawValue)
	}

	// Apply backend authentication if handler is configured
	if u.handler != nil {
		if err := u.handler.Do(ctx, u.requestHeaders, headerMutation, bodyMutation); err != nil {
			return nil, fmt.Errorf("failed to apply backend auth: %w", err)
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
					HeaderMutation: headerMutation,
					BodyMutation:   bodyMutation,
					Status:         extprocv3.CommonResponse_CONTINUE_AND_REPLACE,
				},
			},
		},
		DynamicMetadata: dm,
	}, nil
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (u *responsesProcessorUpstreamFilter) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	panic("BUG: ProcessRequestBody should not be called in the upstream filter")
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (u *responsesProcessorUpstreamFilter) ProcessResponseHeaders(ctx context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			u.metrics.RecordRequestCompletion(ctx, false, u.requestHeaders)
		}
	}()

	u.responseHeaders = headersToMap(headers)
	if enc := u.responseHeaders["content-encoding"]; enc != "" {
		u.responseEncoding = enc
	}

	headerMutation, err := u.translator.ResponseHeaders(u.responseHeaders)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response headers: %w", err)
	}

	var mode *extprocv3http.ProcessingMode
	if u.stream && u.responseHeaders[":status"] == "200" {
		// We only stream the response if the status code is 200 and the response is a stream.
		mode = &extprocv3http.ProcessingMode{ResponseBodyMode: extprocv3http.ProcessingMode_STREAMED}
	}

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{HeaderMutation: headerMutation},
			},
		},
		ModeOverride: mode,
	}, nil
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (u *responsesProcessorUpstreamFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
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
		var headerMutation *extprocv3.HeaderMutation
		var bodyMutation *extprocv3.BodyMutation
		headerMutation, bodyMutation, err = u.translator.ResponseError(u.responseHeaders, decodingResult.reader)
		if err != nil {
			return nil, fmt.Errorf("failed to transform response error: %w", err)
		}
		if u.span != nil {
			b := bodyMutation.GetBody()
			if b == nil {
				b = body.Body
			}
			u.span.EndSpanOnError(code, b)
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

	headerMutation, bodyMutation, tokenUsage, responseModel, err := u.translator.ResponseBody(u.responseHeaders, decodingResult.reader, body.EndOfStream, u.span)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}

	// Remove content-encoding header if original body was encoded but was mutated in the processor.
	headerMutation = removeContentEncodingIfNeeded(headerMutation, bodyMutation, decodingResult.isEncoded)

	// Set the response model for metrics if provided
	u.metrics.SetResponseModel(responseModel)
	if u.stream {
		if tokenUsage != (translator.LLMTokenUsage{}) {
			u.costs = tokenUsage
		}
	} else {
		// Non-streaming: single-shot totals.
		u.costs = tokenUsage
	}

	if u.stream {
		// Token latency is only recorded for streaming responses, otherwise it doesn't make sense since
		// these metrics are defined as a difference between the two output events.
		u.metrics.RecordTokenLatency(ctx, tokenUsage.OutputTokens, body.EndOfStream, u.requestHeaders)

		// Record token usage at end of stream
		if body.EndOfStream {
			u.metrics.RecordTokenUsage(ctx, u.costs.InputTokens, u.costs.CachedInputTokens, u.costs.OutputTokens, u.requestHeaders)
		}
	} else {
		u.metrics.RecordTokenUsage(ctx, u.costs.InputTokens, tokenUsage.CachedInputTokens, tokenUsage.OutputTokens, u.requestHeaders)
	}
	var metadata *structpb.Struct
	if body.EndOfStream && len(u.config.requestCosts) > 0 {
		metadata, err = buildDynamicMetadata(u.config, &u.costs, u.requestHeaders, u.backendName)
		if err != nil {
			return nil, fmt.Errorf("failed to build dynamic metadata: %w", err)
		}
		if u.stream {
			// Adding token latency information to metadata.
			u.mergeWithTokenLatencyMetadata(metadata)
		}
	}

	if body.EndOfStream && u.span != nil {
		u.span.EndSpan()
	}

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseBody{
			ResponseBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: headerMutation,
					BodyMutation:   bodyMutation,
				},
			},
		},
		DynamicMetadata: metadata,
	}, nil
}

// SetBackend implements [Processor.SetBackend].
func (u *responsesProcessorUpstreamFilter) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, routeProcessor Processor) (err error) {
	defer func() {
		if err != nil {
			u.metrics.RecordRequestCompletion(ctx, false, u.requestHeaders)
		}
	}()
	// TODO: Add Endponint Picker support
	rp, ok := routeProcessor.(*responsesProcessorRouterFilter)
	if !ok {
		panic("BUG: expected routeProcessor to be of type *responsesProcessorRouterFilter")
	}
	rp.upstreamFilterCount++
	u.metrics.SetBackend(b)
	u.modelNameOverride = b.ModelNameOverride
	u.backendName = b.Name
	u.handler = backendHandler
	u.headerMutator = headermutator.NewHeaderMutator(b.HeaderMutation, rp.requestHeaders)
	// Header-derived labels/CEL must be able to see the overridden request model.
	if u.modelNameOverride != "" {
		u.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = u.modelNameOverride
	}

	if err = u.selectTranslator(b.Schema); err != nil {
		return fmt.Errorf("failed to select translator: %w", err)
	}
	// Extract the original request body from the router processor
	u.originalRequestBody = rp.originalRequestBody
	u.originalRequestBodyRaw = rp.originalRequestBodyRaw
	u.onRetry = rp.upstreamFilterCount > 1
	u.stream = rp.originalRequestBody.Stream
	rp.upstreamFilter = u
	u.span = rp.span

	return nil
}

func (u *responsesProcessorUpstreamFilter) mergeWithTokenLatencyMetadata(metadata *structpb.Struct) {
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

// parseOpenAIResponseBody parses the responses api request body and extracts the model name.
func parseOpenAIResponseBody(body *extprocv3.HttpBody) (modelName string, rb *openai.ResponseRequest, err error) {
	var req openai.ResponseRequest
	if err := json.Unmarshal(body.Body, &req); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal body: %w", err)
	}

	modelName = req.Model

	// Handle the case where model is not specified. In responses api model field can be omitted if reusable prompt is used.
	// https://community.openai.com/t/responses-api-is-badly-documented/1304643
	// TODO: figure out a way to lookup model if prompt is specified.
	if modelName == "" {
		return "", nil, errors.New("model not specified in request")
	}

	return modelName, &req, nil
}
