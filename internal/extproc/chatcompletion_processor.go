// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"cmp"
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
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// ChatCompletionProcessorFactory returns a factory method to instantiate the chat completion processor.
func ChatCompletionProcessorFactory(f metrics.ChatCompletionMetricsFactory) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, tracing tracing.Tracing, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("processor", "chat-completion", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &chatCompletionProcessorRouterFilter{
				config:         config,
				tracer:         tracing.ChatCompletionTracer(),
				requestHeaders: requestHeaders,
				logger:         logger,
			}, nil
		}
		return &chatCompletionProcessorUpstreamFilter{
			config:         config,
			requestHeaders: requestHeaders,
			logger:         logger,
			metrics:        f(),
		}, nil
	}
}

// chatCompletionProcessorRouterFilter implements [Processor] for the `/v1/chat/completion` endpoint.
//
// This is primarily used to select the route for the request based on the model name.
type chatCompletionProcessorRouterFilter struct {
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
	originalRequestBody    *openai.ChatCompletionRequest
	originalRequestBodyRaw []byte
	// forcedStreamOptionIncludeUsage is set to true if the original request is a streaming request and has the
	// stream_options.include_usage=false. In that case, we force the option to be true to ensure that the token usage is calculated correctly.
	forcedStreamOptionIncludeUsage bool
	// tracer is the tracer used for requests.
	tracer tracing.ChatCompletionTracer
	// span is the tracing span for this request, created in ProcessRequestBody.
	span tracing.ChatCompletionSpan
	// upstreamFilterCount is the number of upstream filters that have been processed.
	// This is used to determine if the request is a retry request.
	upstreamFilterCount int
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (c *chatCompletionProcessorRouterFilter) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// c.upstreamFilter can be nil.
	if c.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		return c.upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return c.passThroughProcessor.ProcessResponseHeaders(ctx, headerMap)
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (c *chatCompletionProcessorRouterFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (resp *extprocv3.ProcessingResponse, err error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// c.upstreamFilter can be nil.
	if c.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		resp, err = c.upstreamFilter.ProcessResponseBody(ctx, body)
	} else {
		resp, err = c.passThroughProcessor.ProcessResponseBody(ctx, body)
	}
	return
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (c *chatCompletionProcessorRouterFilter) ProcessRequestBody(ctx context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	originalModel, body, err := parseOpenAIChatCompletionBody(rawBody)
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
		// TODO: alternatively, we could just return 403 or 400 error here. That makes sense since configuring the
		// request cost metrics means that the gateway provisioners want to track the token usage for the request vs
		// setting this option to false means that clients are trying to escape that rule.
	}

	c.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = originalModel

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		// Set the original model to the request header with the key `x-ai-eg-model`.
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

// chatCompletionProcessorUpstreamFilter implements [Processor] for the `/v1/chat/completion` endpoint at the upstream filter.
//
// This is created per retry and handles the translation as well as the authentication of the request.
type chatCompletionProcessorUpstreamFilter struct {
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
	originalRequestBody    *openai.ChatCompletionRequest
	translator             translator.OpenAIChatCompletionTranslator
	// onRetry is true if this is a retry request at the upstream filter.
	onRetry bool
	// cost is the cost of the request that is accumulated during the processing of the response.
	costs translator.LLMTokenUsage
	// metrics tracking.
	metrics metrics.ChatCompletionMetrics
	// stream is set to true if the request is a streaming request.
	stream bool
	// See the comment on the `forcedStreamOptionIncludeUsage` field in the router filter.
	forcedStreamOptionIncludeUsage bool
	// span is the tracing span for this request, inherited from the router filter.
	span tracing.ChatCompletionSpan
}

// selectTranslator selects the translator based on the output schema.
func (c *chatCompletionProcessorUpstreamFilter) selectTranslator(out filterapi.VersionedAPISchema) error {
	switch out.Name {
	case filterapi.APISchemaOpenAI:
		c.translator = translator.NewChatCompletionOpenAIToOpenAITranslator(out.Version, c.modelNameOverride)
	case filterapi.APISchemaAWSBedrock:
		c.translator = translator.NewChatCompletionOpenAIToAWSBedrockTranslator(c.modelNameOverride)
	case filterapi.APISchemaAzureOpenAI:
		c.translator = translator.NewChatCompletionOpenAIToAzureOpenAITranslator(out.Version, c.modelNameOverride)
	case filterapi.APISchemaGCPVertexAI, filterapi.APISchemaGemini:
		c.translator = translator.NewChatCompletionOpenAIToGCPVertexAITranslator(c.modelNameOverride)
	case filterapi.APISchemaGCPAnthropic:
		c.translator = translator.NewChatCompletionOpenAIToGCPAnthropicTranslator(out.Version, c.modelNameOverride)
	default:
		return fmt.Errorf("unsupported API schema: backend=%s", out)
	}
	return nil
}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
//
// At the upstream filter, we already have the original request body at request headers phase.
// So, we simply do the translation and upstream auth at this stage, and send them back to Envoy
// with the status CONTINUE_AND_REPLACE. This allows Envoy to not send the request body again
// to the extproc.
func (c *chatCompletionProcessorUpstreamFilter) ProcessRequestHeaders(ctx context.Context, _ *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
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
	reqModel := cmp.Or(c.requestHeaders[internalapi.ModelNameHeaderKeyDefault], c.originalRequestBody.Model)
	c.metrics.SetRequestModel(reqModel)

	// We force the body mutation in the following cases:
	// * The request is a retry request because the body mutation might have happened the previous iteration.
	// * The request is a streaming request, and the IncludeUsage option is set to false since we need to ensure that
	//	the token usage is calculated correctly without being bypassed.
	forceBodyMutation := c.onRetry || c.forcedStreamOptionIncludeUsage
	headerMutation, bodyMutation, err := c.translator.RequestBody(c.originalRequestBodyRaw, c.originalRequestBody, forceBodyMutation)
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
func (c *chatCompletionProcessorUpstreamFilter) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	panic("BUG: ProcessRequestBody should not be called in the upstream filter")
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (c *chatCompletionProcessorUpstreamFilter) ProcessResponseHeaders(ctx context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
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
func (c *chatCompletionProcessorUpstreamFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
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

	headerMutation, bodyMutation, tokenUsage, responseModel, err := c.translator.ResponseBody(c.responseHeaders, decodingResult.reader, body.EndOfStream, c.span)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}

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

	// Update accumulated token usage.
	// TODO: we need to investigate if we need to accumulate the token usage for streaming responses.
	if c.stream {
		// For streaming, translators report cumulative usage; keep the latest totals.
		if tokenUsage != (translator.LLMTokenUsage{}) {
			c.costs = tokenUsage
		}
	} else {
		// Non-streaming: single-shot totals.
		c.costs = tokenUsage
	}

	// Set the response model for metrics
	c.metrics.SetResponseModel(responseModel)

	// Record metrics.
	if c.stream {
		// Token latency is only recorded for streaming responses, otherwise it doesn't make sense since
		// these metrics are defined as a difference between the two output events.
		c.metrics.RecordTokenLatency(ctx, tokenUsage.OutputTokens, body.EndOfStream, c.requestHeaders)
		// Emit usage once at end-of-stream using final totals.
		if body.EndOfStream {
			c.metrics.RecordTokenUsage(ctx, c.costs.InputTokens, c.costs.CachedInputTokens, c.costs.OutputTokens, c.requestHeaders)
		}
		// TODO: if c.forcedStreamOptionIncludeUsage is true, we should not include usage in the response body since
		// that's what the clients would expect. However, it is a little bit tricky as we simply just reading the streaming
		// chunk by chunk, we only want to drop a specific line before the last chunk.
	} else {
		c.metrics.RecordTokenUsage(ctx, tokenUsage.InputTokens, tokenUsage.CachedInputTokens, tokenUsage.OutputTokens, c.requestHeaders)
	}

	if body.EndOfStream && len(c.config.requestCosts) > 0 {
		metadata, err := buildDynamicMetadata(c.config, &c.costs, c.requestHeaders, c.backendName)
		if err != nil {
			return nil, fmt.Errorf("failed to build dynamic metadata: %w", err)
		}
		if c.stream {
			// Adding token latency information to metadata.
			c.mergeWithTokenLatencyMetadata(metadata)
		}
		resp.DynamicMetadata = metadata
	}

	if body.EndOfStream && c.span != nil {
		c.span.EndSpan()
	}
	return resp, nil
}

// SetBackend implements [Processor.SetBackend].
func (c *chatCompletionProcessorUpstreamFilter) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, routeProcessor Processor) (err error) {
	defer func() {
		if err != nil {
			c.metrics.RecordRequestCompletion(ctx, false, c.requestHeaders)
		}
	}()
	pickedEndpoint, isEndpointPicker := c.requestHeaders[internalapi.EndpointPickerHeaderKey]
	rp, ok := routeProcessor.(*chatCompletionProcessorRouterFilter)
	if !ok {
		panic("BUG: expected routeProcessor to be of type *chatCompletionProcessorRouterFilter")
	}
	rp.upstreamFilterCount++
	c.metrics.SetBackend(b)
	c.modelNameOverride = b.ModelNameOverride
	c.backendName = b.Name
	if err = c.selectTranslator(b.Schema); err != nil {
		return fmt.Errorf("failed to select translator: %w", err)
	}
	
	// Configure translator for Gemini API key vs GCP Vertex AI OAuth2 authentication
	if b.Schema.Name == filterapi.APISchemaGemini || b.Schema.Name == filterapi.APISchemaGCPVertexAI {
		// Check if using Gemini API key authentication (which uses /v1beta path)
		// vs GCP OAuth2 authentication (which uses /publishers path)
		useGeminiPath := b.Auth != nil && b.Auth.GeminiAPIKey != nil
		
		// Configure the translator to use the appropriate path
		type geminiPathSetter interface {
			SetUseGeminiDirectPath(bool)
		}
		if setter, ok := c.translator.(geminiPathSetter); ok {
			setter.SetUseGeminiDirectPath(useGeminiPath)
		}
	}
	
	c.handler = backendHandler
	c.headerMutator = headermutator.NewHeaderMutator(b.HeaderMutation, rp.requestHeaders)
	// Header-derived labels/CEL must be able to see the overridden request model.
	if c.modelNameOverride != "" {
		c.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = c.modelNameOverride
	}
	c.originalRequestBody = rp.originalRequestBody
	c.originalRequestBodyRaw = rp.originalRequestBodyRaw
	c.onRetry = rp.upstreamFilterCount > 1
	c.stream = c.originalRequestBody.Stream
	if isEndpointPicker {
		if c.logger.Enabled(ctx, slog.LevelDebug) {
			c.logger.Debug("selected backend", slog.String("picked_endpoint", pickedEndpoint), slog.String("backendName", b.Name), slog.String("modelNameOverride", c.modelNameOverride))
		}
	}
	rp.upstreamFilter = c
	c.forcedStreamOptionIncludeUsage = rp.forcedStreamOptionIncludeUsage
	c.span = rp.span
	return
}

func (c *chatCompletionProcessorUpstreamFilter) mergeWithTokenLatencyMetadata(metadata *structpb.Struct) {
	timeToFirstTokenMs := c.metrics.GetTimeToFirstTokenMs()
	interTokenLatencyMs := c.metrics.GetInterTokenLatencyMs()
	innerVal := metadata.Fields[internalapi.AIGatewayFilterMetadataNamespace].GetStructValue()
	if innerVal == nil {
		innerVal = &structpb.Struct{Fields: map[string]*structpb.Value{}}
		metadata.Fields[internalapi.AIGatewayFilterMetadataNamespace] = structpb.NewStructValue(innerVal)
	}
	innerVal.Fields["token_latency_ttft"] = &structpb.Value{Kind: &structpb.Value_NumberValue{NumberValue: timeToFirstTokenMs}}
	innerVal.Fields["token_latency_itl"] = &structpb.Value{Kind: &structpb.Value_NumberValue{NumberValue: interTokenLatencyMs}}
}

func parseOpenAIChatCompletionBody(body *extprocv3.HttpBody) (modelName string, rb *openai.ChatCompletionRequest, err error) {
	var openAIReq openai.ChatCompletionRequest
	if err := json.Unmarshal(body.Body, &openAIReq); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	return openAIReq.Model, &openAIReq, nil
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
func buildDynamicMetadata(config *processorConfig, costs *translator.LLMTokenUsage, requestHeaders map[string]string, backendName string) (*structpb.Struct, error) {
	metadata := make(map[string]*structpb.Value, len(config.requestCosts)+2)
	for i := range config.requestCosts {
		rc := &config.requestCosts[i]
		var cost uint32
		switch rc.Type {
		case filterapi.LLMRequestCostTypeInputToken:
			cost = costs.InputTokens
		case filterapi.LLMRequestCostTypeCachedInputToken:
			cost = costs.CachedInputTokens
		case filterapi.LLMRequestCostTypeOutputToken:
			cost = costs.OutputTokens
		case filterapi.LLMRequestCostTypeTotalToken:
			cost = costs.TotalTokens
		case filterapi.LLMRequestCostTypeCEL:
			costU64, err := llmcostcel.EvaluateProgram(
				rc.celProg,
				requestHeaders[internalapi.ModelNameHeaderKeyDefault],
				backendName,
				costs.InputTokens,
				costs.CachedInputTokens,
				costs.OutputTokens,
				costs.TotalTokens,
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
