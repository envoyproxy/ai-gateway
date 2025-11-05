// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/tidwall/sjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// CompletionsProcessorFactory returns a factory method to instantiate the completions processor.
func CompletionsProcessorFactory(f metrics.CompletionMetricsFactory) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, tracing tracing.Tracing, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("processor", "completions", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &completionsProcessorRouterFilter{
				config:         config,
				tracer:         tracing.CompletionTracer(),
				requestHeaders: requestHeaders,
				logger:         logger,
			}, nil
		}
		return &completionsProcessorUpstreamFilter{
			config:         config,
			requestHeaders: requestHeaders,
			logger:         logger,
			metrics:        f(),
		}, nil
	}
}

// completionsProcessorRouterFilter implements [Processor] for the `/v1/completions` endpoint.
//
// This is primarily used to select the route for the request based on the model name.
type completionsProcessorRouterFilter struct {
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
	originalRequestBody    *openai.CompletionRequest
	originalRequestBodyRaw []byte
	// forcedStreamOptionIncludeUsage is set to true if the original request is a streaming request and has the
	// stream_options.include_usage=false. In that case, we force the option to be true to ensure that the token usage is calculated correctly.
	forcedStreamOptionIncludeUsage bool
	// tracer is the tracer used for requests.
	tracer tracing.CompletionTracer
	// span is the tracing span for this request, created in ProcessRequestBody.
	span tracing.CompletionSpan
	// upstreamFilterCount is the number of upstream filters that have been processed.
	// This is used to determine if the request is a retry request.
	upstreamFilterCount int
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (c *completionsProcessorRouterFilter) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// c.upstreamFilter can be nil.
	if c.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		return c.upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return c.passThroughProcessor.ProcessResponseHeaders(ctx, headerMap)
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (c *completionsProcessorRouterFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// c.upstreamFilter can be nil.
	if c.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		return c.upstreamFilter.ProcessResponseBody(ctx, body)
	}
	return c.passThroughProcessor.ProcessResponseBody(ctx, body)
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (c *completionsProcessorRouterFilter) ProcessRequestBody(ctx context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	originalModel, body, err := parseOpenAICompletionBody(rawBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

	if body.Stream && (body.StreamOptions == nil || !body.StreamOptions.IncludeUsage) && len(c.config.requestCosts) > 0 {
		// If the request is a streaming request and cost metrics are configured, we need to include usage in the response
		// to avoid the bypassing of the token usage calculation.
		body.StreamOptions = &openai.StreamOptions{IncludeUsage: true}
		// Rewrite the original bytes to include the stream_options.include_usage=true so that forcing the request body
		// mutation, which uses this raw body, will also result in the stream_options.include_usage=true.
		rawBody.Body, err = sjson.SetBytesOptions(rawBody.Body, "stream_options.include_usage", true, &sjson.Options{
			Optimistic: true,
			// Note: it is safe to do in-place replacement since this route level processor is executed once per request,
			// and the result can be safely shared among possible multiple retries.
			ReplaceInPlace: true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to set stream_options: %w", err)
		}
		c.forcedStreamOptionIncludeUsage = true
	}

	c.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = originalModel

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		// Set the model name to the request header with the key `x-ai-eg-model`.
		Header: &corev3.HeaderValue{Key: internalapi.ModelNameHeaderKeyDefault, RawValue: []byte(originalModel)},
	}, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: originalPathHeader, RawValue: []byte(c.requestHeaders[":path"])},
	})
	c.originalRequestBody = body
	c.originalRequestBodyRaw = rawBody.Body

	// Tracing may need to inject headers, so create a header mutation here.
	headerMutation := &extprocv3.HeaderMutation{
		SetHeaders: additionalHeaders,
	}
	c.span = c.tracer.StartSpanAndInjectHeaders(
		ctx,
		c.requestHeaders,
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

// completionsProcessorUpstreamFilter implements [Processor] for the `/v1/completions` endpoint at the upstream filter.
//
// This is created per retry and handles the translation as well as the authentication of the request.
type completionsProcessorUpstreamFilter struct {
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
	originalRequestBody    *openai.CompletionRequest
	translator             translator.OpenAICompletionTranslator
	// onRetry is true if this is a retry request at the upstream filter.
	onRetry bool
	// stream is set to true if the request is a streaming request.
	stream bool
	// See the comment on the `forcedStreamOptionIncludeUsage` field in the router filter.
	forcedStreamOptionIncludeUsage bool
	// cost is the cost of the request that is accumulated during the processing of the response.
	costs translator.LLMTokenUsage
	// span is the tracing span for this request, inherited from the router filter.
	span tracing.CompletionSpan
	// metrics tracking.
	metrics metrics.CompletionMetrics
}

// selectTranslator selects the translator based on the output schema.
func (c *completionsProcessorUpstreamFilter) selectTranslator(out filterapi.VersionedAPISchema) error {
	switch out.Name {
	case filterapi.APISchemaOpenAI:
		c.translator = translator.NewCompletionOpenAIToOpenAITranslator(out.Version, c.modelNameOverride)
	default:
		return fmt.Errorf("unsupported API schema: backend=%s", out)
	}
	return nil
}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
//
// At the upstream filter, we already have the original request body at request headers phase.
// So, we simply do the translation and upstream auth at this stage, and send them back to Envoy
// with the status CONTINUE_AND_REPLACE. This will allows Envoy to not send the request body again
// to the extproc.
func (c *completionsProcessorUpstreamFilter) ProcessRequestHeaders(ctx context.Context, _ *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			c.metrics.RecordRequestCompletion(ctx, false, c.requestHeaders)
		}
	}()
	// Start tracking metrics for this request.
	c.metrics.StartRequest(c.requestHeaders)
	// Set the original model from the request body before any overrides
	c.metrics.SetOriginalModel(c.originalRequestBody.Model)
	// Set the request model for metrics from the original model or override if applied.
	reqModel := c.originalRequestBody.Model
	if override := c.requestHeaders[internalapi.ModelNameHeaderKeyDefault]; override != "" {
		reqModel = override
	}
	c.metrics.SetRequestModel(reqModel)

	headerMutation, bodyMutation, err := c.translator.RequestBody(c.originalRequestBodyRaw, c.originalRequestBody, c.onRetry)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}
	if headerMutation == nil {
		headerMutation = &extprocv3.HeaderMutation{}
	}

	// Apply header mutations from the route and also restore original headers on retry.
	if h := c.headerMutator; h != nil {
		sets, removes := c.headerMutator.Mutate(c.requestHeaders, c.onRetry)
		headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, removes...)
		for _, pair := range sets {
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
				Header: &corev3.HeaderValue{
					Key:      pair[0],
					RawValue: []byte(pair[1]),
				},
			})
		}
	}

	for _, h := range headerMutation.SetHeaders {
		c.requestHeaders[h.Header.Key] = string(h.Header.RawValue)
	}
	if h := c.handler; h != nil {
		var hdrs []internalapi.Header
		hdrs, err = h.Do(ctx, c.requestHeaders, bodyMutation.GetBody())
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
func (c *completionsProcessorUpstreamFilter) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	return nil, fmt.Errorf("%w: ProcessRequestBody", errUnexpectedCall)
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (c *completionsProcessorUpstreamFilter) ProcessResponseHeaders(ctx context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			c.metrics.RecordRequestCompletion(ctx, false, c.requestHeaders)
		}
	}()
	c.responseHeaders = headersToMap(headers)
	if enc := c.responseHeaders["content-encoding"]; enc != "" {
		c.responseEncoding = enc
	}
	headerMutation, err := c.translator.ResponseHeaders(c.responseHeaders)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response headers: %w", err)
	}
	var mode *extprocv3http.ProcessingMode
	if c.stream && c.responseHeaders[":status"] == "200" {
		// We only stream the response if the status code is 200 and the response is a stream.
		mode = &extprocv3http.ProcessingMode{ResponseBodyMode: extprocv3http.ProcessingMode_STREAMED}
	}
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{
		ResponseHeaders: &extprocv3.HeadersResponse{
			Response: &extprocv3.CommonResponse{HeaderMutation: headerMutation},
		},
	}, ModeOverride: mode}, nil
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (c *completionsProcessorUpstreamFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	// Track whether we need to record request completion on error.
	recordRequestCompletionErr := false
	defer func() {
		if err != nil || recordRequestCompletionErr {
			c.metrics.RecordRequestCompletion(ctx, false, c.requestHeaders)
			return
		}
		if body.EndOfStream {
			c.metrics.RecordRequestCompletion(ctx, true, c.requestHeaders)
		}
	}()

	// Decompress the body if needed using common utility.
	decodingResult, err := decodeContentIfNeeded(body.Body, c.responseEncoding)
	if err != nil {
		return nil, err
	}

	// Assume all responses have a valid status code header.
	if code, _ := strconv.Atoi(c.responseHeaders[":status"]); !isGoodStatusCode(code) {
		recordRequestCompletionErr = true
		var headerMutation *extprocv3.HeaderMutation
		var bodyMutation *extprocv3.BodyMutation
		headerMutation, bodyMutation, err = c.translator.ResponseError(c.responseHeaders, decodingResult.reader)
		if err != nil {
			return nil, fmt.Errorf("failed to transform response error: %w", err)
		}
		if c.span != nil {
			b := bodyMutation.GetBody()
			if b == nil {
				b = body.Body
			}
			c.span.EndSpanOnError(code, b)
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
		}, nil
	}

	headerMutation, bodyMutation, tokenUsage, responseModel, err := c.translator.ResponseBody(c.responseHeaders, decodingResult.reader, body.EndOfStream, c.span)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}

	// Set the response model for metrics
	c.metrics.SetResponseModel(responseModel)

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

	// Accumulate token usage for completions.
	c.costs.InputTokens += tokenUsage.InputTokens
	c.costs.OutputTokens += tokenUsage.OutputTokens
	c.costs.TotalTokens += tokenUsage.TotalTokens

	// Record metrics.
	if c.stream {
		// Token latency is only recorded for streaming responses
		c.metrics.RecordTokenLatency(ctx, tokenUsage.OutputTokens, body.EndOfStream, c.requestHeaders)
		// Emit usage once at end-of-stream using final totals.
		if body.EndOfStream {
			c.metrics.RecordTokenUsage(ctx, c.costs.InputTokens, c.costs.OutputTokens, c.requestHeaders)
		}
	} else {
		c.metrics.RecordTokenUsage(ctx, tokenUsage.InputTokens, tokenUsage.OutputTokens, c.requestHeaders)
	}

	// Log the response model for debugging
	if responseModel != "" {
		c.logger.Debug("completion response model", "model", responseModel)
	}

	if body.EndOfStream && len(c.config.requestCosts) > 0 {
		resp.DynamicMetadata, err = buildDynamicMetadata(c.config, &c.costs, c.requestHeaders, c.backendName)
		if err != nil {
			return nil, fmt.Errorf("failed to build dynamic metadata: %w", err)
		}
		// Merge token latency metadata if streaming.
		if c.stream {
			c.mergeWithTokenLatencyMetadata(resp.DynamicMetadata)
		}
	}

	if body.EndOfStream && c.span != nil {
		c.span.EndSpan()
	}

	return resp, nil
}

// SetBackend implements [Processor.SetBackend].
func (c *completionsProcessorUpstreamFilter) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, routeProcessor Processor) (err error) {
	defer func() {
		if err != nil {
			c.metrics.RecordRequestCompletion(ctx, false, c.requestHeaders)
		}
	}()
	rp, ok := routeProcessor.(*completionsProcessorRouterFilter)
	if !ok {
		panic("BUG: expected routeProcessor to be of type *completionsProcessorRouterFilter")
	}
	rp.upstreamFilterCount++
	c.metrics.SetBackend(b)
	c.modelNameOverride = b.ModelNameOverride
	c.backendName = b.Name
	c.originalRequestBody = rp.originalRequestBody
	c.originalRequestBodyRaw = rp.originalRequestBodyRaw
	c.onRetry = rp.upstreamFilterCount > 1
	c.stream = c.originalRequestBody.Stream
	c.forcedStreamOptionIncludeUsage = rp.forcedStreamOptionIncludeUsage
	if err = c.selectTranslator(b.Schema); err != nil {
		return fmt.Errorf("failed to select translator: %w", err)
	}
	c.handler = backendHandler
	c.headerMutator = headermutator.NewHeaderMutator(b.HeaderMutation, rp.requestHeaders)
	// Header-derived labels/CEL must be able to see the overridden request model.
	if c.modelNameOverride != "" {
		c.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = c.modelNameOverride
	}
	rp.upstreamFilter = c
	c.span = rp.span
	return
}

func (c *completionsProcessorUpstreamFilter) mergeWithTokenLatencyMetadata(metadata *structpb.Struct) {
	timeToFirstTokenMs := c.metrics.GetTimeToFirstTokenMs()
	interTokenLatencyMs := c.metrics.GetInterTokenLatencyMs()
	innerVal := metadata.Fields[internalapi.AIGatewayFilterMetadataNamespace].GetStructValue()
	if innerVal == nil {
		innerVal = &structpb.Struct{Fields: make(map[string]*structpb.Value)}
		metadata.Fields[internalapi.AIGatewayFilterMetadataNamespace] = structpb.NewStructValue(innerVal)
	}
	innerVal.Fields["token_latency_ttft"] = &structpb.Value{Kind: &structpb.Value_NumberValue{NumberValue: timeToFirstTokenMs}}
	innerVal.Fields["token_latency_itl"] = &structpb.Value{Kind: &structpb.Value_NumberValue{NumberValue: interTokenLatencyMs}}
}

func parseOpenAICompletionBody(body *extprocv3.HttpBody) (modelName string, rb *openai.CompletionRequest, err error) {
	var openAIReq openai.CompletionRequest
	if err := json.Unmarshal(body.Body, &openAIReq); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	return openAIReq.Model, &openAIReq, nil
}
