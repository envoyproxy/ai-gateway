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
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// MessagesProcessorFactory returns a factory for the Anthropic /v1/messages endpoint.
//
// Requests: Only accepts Anthropic format requests.
// Responses: Returns Anthropic format responses.
func MessagesProcessorFactory(f metrics.MessagesMetricsFactory) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, _ tracing.Tracing, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("processor", "anthropic-messages", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &messagesProcessorRouterFilter{
				config:         config,
				requestHeaders: requestHeaders,
				logger:         logger,
			}, nil
		}
		return &messagesProcessorUpstreamFilter{
			config:         config,
			requestHeaders: requestHeaders,
			logger:         logger,
			metrics:        f(),
		}, nil
	}
}

// messagesProcessorRouterFilter implements [Processor] for the `/v1/messages` endpoint.
//
// This is primarily used to select the route for the request based on the model name.
type messagesProcessorRouterFilter struct {
	passThroughProcessor
	upstreamFilter         Processor
	logger                 *slog.Logger
	config                 *processorConfig
	requestHeaders         map[string]string
	originalRequestBody    *anthropic.MessagesRequest
	originalRequestBodyRaw []byte
	upstreamFilterCount    int
}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
func (c *messagesProcessorRouterFilter) ProcessRequestHeaders(_ context.Context, _ *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{}}, nil
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (c *messagesProcessorRouterFilter) ProcessRequestBody(_ context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	// Parse Anthropic request - natural validation.
	body, err := parseAnthropicMessagesBody(rawBody)
	if err != nil {
		return nil, fmt.Errorf("/v1/messages endpoint requires Anthropic format: %w", err)
	}
	originalModel := body.Model

	c.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = originalModel
	c.originalRequestBody = body

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		// Set the model name to the request header with the key `x-ai-eg-model`.
		Header: &corev3.HeaderValue{Key: internalapi.ModelNameHeaderKeyDefault, RawValue: []byte(originalModel)},
	}, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: originalPathHeader, RawValue: []byte(c.requestHeaders[":path"])},
	})
	c.originalRequestBodyRaw = rawBody.Body
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: &extprocv3.HeaderMutation{
						SetHeaders: additionalHeaders,
					},
					ClearRouteCache: true,
				},
			},
		},
	}, nil
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (c *messagesProcessorRouterFilter) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// c.upstreamFilter can be nil.
	if c.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		return c.upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return c.passThroughProcessor.ProcessResponseHeaders(ctx, headerMap)
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (c *messagesProcessorRouterFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// c.upstreamFilter can be nil.
	if c.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		return c.upstreamFilter.ProcessResponseBody(ctx, body)
	}
	return c.passThroughProcessor.ProcessResponseBody(ctx, body)
}

// SetBackend implements [Processor.SetBackend].
func (c *messagesProcessorRouterFilter) SetBackend(_ context.Context, _ *filterapi.Backend, _ backendauth.Handler, _ Processor) error {
	return nil
}

// messagesProcessorUpstreamFilter implements [Processor] for the `/v1/messages` endpoint.
//
// This transforms Anthropic requests to various backend formats.
type messagesProcessorUpstreamFilter struct {
	logger                 *slog.Logger
	config                 *processorConfig
	requestHeaders         map[string]string
	responseHeaders        map[string]string
	responseEncoding       string
	modelNameOverride      internalapi.ModelNameOverride
	backendName            string
	handler                backendauth.Handler
	headerMutator          *headermutator.HeaderMutator
	originalRequestBody    *anthropic.MessagesRequest
	originalRequestBodyRaw []byte
	translator             translator.AnthropicMessagesTranslator
	onRetry                bool
	stream                 bool
	metrics                metrics.MessagesMetrics
	costs                  translator.LLMTokenUsage
}

// selectTranslator selects the translator based on the output schema.
func (c *messagesProcessorUpstreamFilter) selectTranslator(out filterapi.VersionedAPISchema) error {
	// Messages processor only supports Anthropic-native translators.
	switch out.Name {
	case filterapi.APISchemaGCPAnthropic:
		// Anthropic → GCP Anthropic (request direction translator).
		// Uses backend config version (GCP Vertex AI requires specific versions like "vertex-2023-10-16").
		c.translator = translator.NewAnthropicToGCPAnthropicTranslator(out.Version, c.modelNameOverride)
	case filterapi.APISchemaAWSAnthropic:
		// Anthropic → AWS Bedrock Anthropic (request direction translator).
		c.translator = translator.NewAnthropicToAWSAnthropicTranslator(out.Version, c.modelNameOverride)
	case filterapi.APISchemaAnthropic:
		c.translator = translator.NewAnthropicToAnthropicTranslator(out.Version, c.modelNameOverride)
	default:
		return fmt.Errorf("/v1/messages endpoint only supports backends that return native Anthropic format (Anthropic, GCPAnthropic, AWSAnthropic). Backend %s uses different model format", out.Name)
	}
	return nil
}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
func (c *messagesProcessorUpstreamFilter) ProcessRequestHeaders(ctx context.Context, _ *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
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

	// Force body mutation for retry requests as the body mutation might have happened in previous iteration.
	forceBodyMutation := c.onRetry
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
func (c *messagesProcessorUpstreamFilter) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	panic("BUG: ProcessRequestBody should not be called in the upstream filter")
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (c *messagesProcessorUpstreamFilter) ProcessResponseHeaders(ctx context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
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
func (c *messagesProcessorUpstreamFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			c.metrics.RecordRequestCompletion(ctx, false, c.requestHeaders)
			return
		}
		if body.EndOfStream {
			c.metrics.RecordRequestCompletion(ctx, true, c.requestHeaders)
		}
	}()
	if code, _ := strconv.Atoi(c.responseHeaders[":status"]); !isGoodStatusCode(code) {
		// For now, simply pass through error responses without modification.
		// TODO: do the error conversion like other processors, to be able to capture any error in the proper
		// format expected by the client.
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{ResponseBody: &extprocv3.BodyResponse{}},
		}, nil
	}

	// Decompress the body if needed using common utility.
	decodingResult, err := decodeContentIfNeeded(body.Body, c.responseEncoding)
	if err != nil {
		return nil, err
	}

	// headerMutation, bodyMutation, tokenUsage, err := c.translator.ResponseBody(c.responseHeaders, br, body.EndOfStream).
	headerMutation, bodyMutation, tokenUsage, responseModel, err := c.translator.ResponseBody(c.responseHeaders, decodingResult.reader, body.EndOfStream)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}

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

	// Token usages are cumulative for streaming responses, so we update the stored costs.
	c.costs.InputTokens = tokenUsage.InputTokens
	c.costs.OutputTokens = tokenUsage.OutputTokens
	c.costs.TotalTokens = tokenUsage.TotalTokens
	c.costs.CachedInputTokens = tokenUsage.CachedInputTokens

	// Update metrics with token usage.
	c.metrics.RecordTokenUsage(ctx, tokenUsage.InputTokens, tokenUsage.CachedInputTokens, tokenUsage.OutputTokens, c.requestHeaders)
	if c.stream {
		c.metrics.RecordTokenLatency(ctx, tokenUsage.OutputTokens, body.EndOfStream, c.requestHeaders)
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

	return resp, nil
}

// SetBackend implements [Processor.SetBackend].
func (c *messagesProcessorUpstreamFilter) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, routeProcessor Processor) (err error) {
	defer func() {
		if err != nil {
			c.metrics.RecordRequestCompletion(ctx, false, c.requestHeaders)
		}
	}()
	pickedEndpoint, isEndpointPicker := c.requestHeaders[internalapi.EndpointPickerHeaderKey]
	rp, ok := routeProcessor.(*messagesProcessorRouterFilter)
	if !ok {
		panic("BUG: expected routeProcessor to be of type *messagesProcessorRouterFilter")
	}
	rp.upstreamFilterCount++
	c.metrics.SetBackend(b)
	c.modelNameOverride = b.ModelNameOverride
	c.backendName = b.Name
	if err = c.selectTranslator(b.Schema); err != nil {
		return fmt.Errorf("failed to select translator: %w", err)
	}
	c.handler = backendHandler
	c.headerMutator = headermutator.NewHeaderMutator(b.HeaderMutation, rp.requestHeaders)
	// Header-derived labels/CEL must be able to see the overridden request model.
	if c.modelNameOverride != "" {
		c.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = c.modelNameOverride
		// Update metrics with the overridden model
		c.metrics.SetRequestModel(c.modelNameOverride)
	}
	c.originalRequestBody = rp.originalRequestBody
	c.originalRequestBodyRaw = rp.originalRequestBodyRaw
	c.onRetry = rp.upstreamFilterCount > 1

	// Determine if this is a streaming request from the parsed body.
	c.stream = rp.originalRequestBody.Stream
	if isEndpointPicker {
		if c.logger.Enabled(ctx, slog.LevelDebug) {
			c.logger.Debug("selected backend", slog.String("picked_endpoint", pickedEndpoint), slog.String("backendName", b.Name), slog.String("modelNameOverride", c.modelNameOverride))
		}
	}
	rp.upstreamFilter = c
	return
}

func (c *messagesProcessorUpstreamFilter) mergeWithTokenLatencyMetadata(metadata *structpb.Struct) {
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

// parseAnthropicMessagesBody parses the Anthropic Messages API request body.
func parseAnthropicMessagesBody(body *extprocv3.HttpBody) (req *anthropic.MessagesRequest, err error) {
	var anthropicReq anthropic.MessagesRequest
	if err := json.Unmarshal(body.Body, &anthropicReq); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Anthropic Messages body: %w", err)
	}
	return &anthropicReq, nil
}
