// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
	"github.com/envoyproxy/ai-gateway/internal/extproc/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// MessagesProcessorFactory returns a factory for the Anthropic /v1/messages endpoint.
//
// Configuration: Accepts OpenAI schema config for infrastructure compatibility.
// Requests: Only accepts Anthropic format requests.
// Responses: Returns Anthropic format responses.
func MessagesProcessorFactory(ccm x.ChatCompletionMetrics) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, isUpstreamFilter bool) (Processor, error) {
		// Accept OpenAI schema config for infrastructure compatibility.
		if config.schema.Name != filterapi.APISchemaOpenAI {
			return nil, fmt.Errorf("unsupported API schema for messages endpoint: %s", config.schema.Name)
		}
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
			metrics:        ccm,
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
	model, err := parseAnthropicModelName(rawBody)
	if err != nil {
		return nil, fmt.Errorf("/v1/messages endpoint requires Anthropic format: %w", err)
	}

	c.requestHeaders[c.config.modelNameHeaderKey] = model

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		// Set the model name to the request header with the key `x-ai-eg-model`.
		Header: &corev3.HeaderValue{Key: c.config.modelNameHeaderKey, RawValue: []byte(model)},
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
	modelNameOverride      string
	backendName            string
	handler                backendauth.Handler
	originalRequestBodyRaw []byte
	translator             translator.OpenAIChatCompletionTranslator
	onRetry                bool
	stream                 bool
	metrics                x.ChatCompletionMetrics
	costs                  translator.LLMTokenUsage
}

// selectTranslator selects the translator based on the output schema.
func (c *messagesProcessorUpstreamFilter) selectTranslator(out filterapi.VersionedAPISchema) error {
	// Messages processor only supports Anthropic-preserving translators.
	switch out.Name {
	case filterapi.APISchemaGCPAnthropic:
		// Anthropic → GCP Anthropic (preserves Anthropic format).
		c.translator = translator.NewAnthropicToGCPAnthropicTranslator(c.modelNameOverride)
		return nil
	case filterapi.APISchemaGCPAnthropicToNative:
		// Anthropic → GCP Anthropic → Native Anthropic (ensures native Anthropic output).
		c.translator = translator.NewGCPAnthropicToNativeAnthropicTranslator(c.modelNameOverride)
		return nil
	default:
		return fmt.Errorf("/v1/messages endpoint only supports backends that preserve Anthropic format (GCPAnthropic, GCPAnthropicToNative). Backend %s uses different model format", out.Name)
	}
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
	c.metrics.SetModel(c.requestHeaders[c.config.modelNameHeaderKey])

	// Force body mutation for retry requests as the body mutation might have happened in previous iteration.
	forceBodyMutation := c.onRetry
	headerMutation, bodyMutation, err := c.translator.RequestBody(c.originalRequestBodyRaw, nil, forceBodyMutation)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}
	if headerMutation == nil {
		headerMutation = &extprocv3.HeaderMutation{}
	} else {
		for _, h := range headerMutation.SetHeaders {
			c.requestHeaders[h.Header.Key] = string(h.Header.RawValue)
		}
	}
	if h := c.handler; h != nil {
		if err = h.Do(ctx, c.requestHeaders, headerMutation, bodyMutation); err != nil {
			return nil, fmt.Errorf("failed to do auth request: %w", err)
		}
	}

	var dm *structpb.Struct
	if bm := bodyMutation.GetBody(); bm != nil {
		dm = buildContentLengthDynamicMetadataOnRequest(c.config, len(bm))
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
		c.metrics.RecordRequestCompletion(ctx, err == nil, c.requestHeaders)
	}()
	var br io.Reader
	var isGzip bool
	switch c.responseEncoding {
	case "gzip":
		br, err = gzip.NewReader(bytes.NewReader(body.Body))
		if err != nil {
			return nil, fmt.Errorf("failed to decode gzip: %w", err)
		}
		isGzip = true
	default:
		br = bytes.NewReader(body.Body)
	}

	headerMutation, bodyMutation, tokenUsage, err := c.translator.ResponseBody(c.responseHeaders, br, body.EndOfStream)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}
	if bodyMutation != nil && isGzip {
		if headerMutation == nil {
			headerMutation = &extprocv3.HeaderMutation{}
		}
		// TODO: this is a hotfix, we should update this to recompress since its in the header
		// If the response was gzipped, ensure we remove the content-encoding header.
		//
		// This is only needed when the transformation is actually modifying the body. When the backend
		// is in OpenAI format (and it's the first try before any retry), the response body is not modified,
		// so we don't need to remove the header in that case.
		headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, "content-encoding")
	}

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

	// TODO: we need to investigate if we need to accumulate the token usage for streaming responses.
	c.costs.InputTokens += tokenUsage.InputTokens
	c.costs.OutputTokens += tokenUsage.OutputTokens
	c.costs.TotalTokens += tokenUsage.TotalTokens

	// Update metrics with token usage.
	c.metrics.RecordTokenUsage(ctx, tokenUsage.InputTokens, tokenUsage.OutputTokens, tokenUsage.TotalTokens, c.requestHeaders)
	if c.stream {
		// Token latency is only recorded for streaming responses, otherwise it doesn't make sense since
		// these metrics are defined as a difference between the two output events.
		c.metrics.RecordTokenLatency(ctx, tokenUsage.OutputTokens, c.requestHeaders)

		// TODO: if c.forcedStreamOptionIncludeUsage is true, we should not include usage in the response body since
		// that's what the clients would expect. However, it is a little bit tricky as we simply just reading the streaming
		// chunk by chunk, we only want to drop a specific line before the last chunk.
	}

	if body.EndOfStream && len(c.config.requestCosts) > 0 {
		metadata, err := buildDynamicMetadata(c.config, &c.costs, c.requestHeaders, c.modelNameOverride, c.backendName)
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
		c.metrics.RecordRequestCompletion(ctx, err == nil, c.requestHeaders)
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
	c.originalRequestBodyRaw = rp.originalRequestBodyRaw
	c.onRetry = rp.upstreamFilterCount > 1

	// Determine if this is a streaming request by parsing the Anthropic request.
	c.stream = isStreamingRequest(c.originalRequestBodyRaw)
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
	ns := c.config.metadataNamespace
	innerVal := metadata.Fields[ns].GetStructValue()
	if innerVal == nil {
		innerVal = &structpb.Struct{Fields: map[string]*structpb.Value{}}
		metadata.Fields[ns] = structpb.NewStructValue(innerVal)
	}
	innerVal.Fields["token_latency_ttft"] = &structpb.Value{Kind: &structpb.Value_NumberValue{NumberValue: timeToFirstTokenMs}}
	innerVal.Fields["token_latency_itl"] = &structpb.Value{Kind: &structpb.Value_NumberValue{NumberValue: interTokenLatencyMs}}
}
