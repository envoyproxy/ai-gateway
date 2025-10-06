// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ai-gateway/internal/extproc/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	openaisdk "github.com/openai/openai-go/v2"
)

// ImageGenerationProcessorFactory returns a factory method to instantiate the image generation processor.
func ImageGenerationProcessorFactory(igm metrics.ImageGenerationMetrics) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, tracing tracing.Tracing, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("processor", "image-generation", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &imageGenerationProcessorRouterFilter{
				config:         config,
				tracer:         tracing.ImageGenerationTracer(),
				requestHeaders: requestHeaders,
				logger:         logger,
			}, nil
		}
		return &imageGenerationProcessorUpstreamFilter{
			config:         config,
			requestHeaders: requestHeaders,
			logger:         logger,
			metrics:        igm,
		}, nil
	}
}

// imageGenerationProcessorRouterFilter implements [Processor] for the `/v1/images/generations` endpoint.
//
// This is primarily used to select the route for the request based on the model name.
type imageGenerationProcessorRouterFilter struct {
	passThroughProcessor
	// upstreamFilter is the upstream filter that is used to process the request at the upstream filter.
	// This will be updated when the request is retried.
	//
	// On the response handling path, we don't need to do any operation until successful, so we use the implementation
	// of the upstream filter to handle the response at the router filter.
	//
	upstreamFilter Processor
	logger         *slog.Logger
	config         *processorConfig
	requestHeaders map[string]string
	// originalRequestBody is the original request body that is passed to the upstream filter.
	// This is used to perform the transformation of the request body on the original input
	// when the request is retried.
	originalRequestBody    *openaisdk.ImageGenerateParams
	originalRequestBodyRaw []byte
	// tracer is the tracer used for requests.
	tracer tracing.ImageGenerationTracer
	// span is the tracing span for this request, created in ProcessRequestBody.
	span tracing.ImageGenerationSpan
	// upstreamFilterCount is the number of upstream filters that have been processed.
	// This is used to determine if the request is a retry request.
	upstreamFilterCount int
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (i *imageGenerationProcessorRouterFilter) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// i.upstreamFilter can be nil.
	if i.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		return i.upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return i.passThroughProcessor.ProcessResponseHeaders(ctx, headerMap)
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (i *imageGenerationProcessorRouterFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (resp *extprocv3.ProcessingResponse, err error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// i.upstreamFilter can be nil.
	if i.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		resp, err = i.upstreamFilter.ProcessResponseBody(ctx, body)
	} else {
		resp, err = i.passThroughProcessor.ProcessResponseBody(ctx, body)
	}
	return
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (i *imageGenerationProcessorRouterFilter) ProcessRequestBody(ctx context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	model, body, err := parseOpenAIImageGenerationBody(rawBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

	// OpenAI SDK doesn't expose a generic Stream flag for image generation; keep false for now.
	isStreamingRequest := false

	i.requestHeaders[i.config.modelNameHeaderKey] = model

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		// Set the model name to the request header with the key `x-ai-eg-model`.
		Header: &corev3.HeaderValue{Key: i.config.modelNameHeaderKey, RawValue: []byte(model)},
	}, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: originalPathHeader, RawValue: []byte(i.requestHeaders[":path"])},
	})

	// Add streaming indicator header for downstream processing
	if isStreamingRequest {
		additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{Key: "x-ai-eg-streaming", RawValue: []byte("true")},
		})
	}

	i.originalRequestBody = body
	i.originalRequestBodyRaw = rawBody.Body

	// Tracing may need to inject headers, so create a header mutation here.
	headerMutation := &extprocv3.HeaderMutation{
		SetHeaders: additionalHeaders,
	}
	i.span = i.tracer.StartSpanAndInjectHeaders(
		ctx,
		i.requestHeaders,
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

// imageGenerationProcessorUpstreamFilter implements [Processor] for the `/v1/images/generations` endpoint at the upstream filter.
//
// This is created per retry and handles the translation as well as the authentication of the request.
type imageGenerationProcessorUpstreamFilter struct {
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
	originalRequestBody    *openaisdk.ImageGenerateParams
	translator             translator.ImageGenerationTranslator
	// onRetry is true if this is a retry request at the upstream filter.
	onRetry bool
	// stream is set to true if the request is a streaming request (for GPT-Image-1).
	stream bool
	// cost is the cost of the request that is accumulated during the processing of the response.
	costs translator.LLMTokenUsage
	// metrics tracking.
	metrics metrics.ImageGenerationMetrics
	// span is the tracing span for this request, inherited from the router filter.
	span tracing.ImageGenerationSpan
}

// selectTranslator selects the translator based on the output schema.
// TODO: Implement proper translator selection once ImageGenerationTranslator is implemented
func (i *imageGenerationProcessorUpstreamFilter) selectTranslator(out filterapi.VersionedAPISchema) error {
	switch out.Name {
	case filterapi.APISchemaOpenAI:
		i.translator = translator.NewImageGenerationOpenAIToOpenAITranslator(out.Version, i.modelNameOverride, i.span)
	case filterapi.APISchemaAWSBedrock:
		// i.translator = translator.NewImageGenerationOpenAIToAWSBedrockTranslator(i.modelNameOverride)
		i.translator = nil // Placeholder
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
func (i *imageGenerationProcessorUpstreamFilter) ProcessRequestHeaders(ctx context.Context, _ *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			i.metrics.RecordRequestCompletion(ctx, false, i.requestHeaders)
		}
	}()

	// Start tracking metrics for this request.
	i.metrics.StartRequest(i.requestHeaders)
	// Set the original model from the request body before any overrides
	i.metrics.SetOriginalModel(internalapi.OriginalModel(i.originalRequestBody.Model))
	// Set the request model for metrics from the original model or override if applied.
	reqModel := cmp.Or(i.requestHeaders[i.config.modelNameHeaderKey], string(i.originalRequestBody.Model))
	i.metrics.SetRequestModel(internalapi.RequestModel(reqModel))
	i.metrics.SetResponseModel(internalapi.ResponseModel(reqModel))

	// We force the body mutation in the following cases:
	// * The request is a retry request because the body mutation might have happened the previous iteration.
	forceBodyMutation := i.onRetry
	headerMutation, bodyMutation, err := i.translator.RequestBody(i.originalRequestBodyRaw, i.originalRequestBody, forceBodyMutation)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}
	if headerMutation == nil {
		headerMutation = &extprocv3.HeaderMutation{}
	}

	// Apply header mutations from the route and also restore original headers on retry.
	if h := i.headerMutator; h != nil {
		if hm := i.headerMutator.Mutate(i.requestHeaders, i.onRetry); hm != nil {
			headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, hm.RemoveHeaders...)
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, hm.SetHeaders...)
		}
	}

	for _, h := range headerMutation.SetHeaders {
		i.requestHeaders[h.Header.Key] = string(h.Header.RawValue)
	}

	if h := i.handler; h != nil {
		if err = h.Do(ctx, i.requestHeaders, headerMutation, bodyMutation); err != nil {
			return nil, fmt.Errorf("failed to do auth request: %w", err)
		}
	}

	var dm *structpb.Struct
	if bm := bodyMutation.GetBody(); bm != nil {
		dm = buildContentLengthDynamicMetadataOnRequest(i.config, len(bm))
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
func (i *imageGenerationProcessorUpstreamFilter) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	panic("BUG: ProcessRequestBody should not be called in the upstream filter")
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (i *imageGenerationProcessorUpstreamFilter) ProcessResponseHeaders(ctx context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			i.metrics.RecordRequestCompletion(ctx, false, i.requestHeaders)
		}
	}()

	i.responseHeaders = headersToMap(headers)
	if enc := i.responseHeaders["content-encoding"]; enc != "" {
		i.responseEncoding = enc
	}

	// Debug logging for response headers
	if i.logger.Enabled(ctx, slog.LevelDebug) {
		i.logger.Debug("response headers received",
			slog.String("content-type", i.responseHeaders["content-type"]),
			slog.String("content-encoding", i.responseHeaders["content-encoding"]),
			slog.String("status", i.responseHeaders[":status"]),
			slog.String("response_encoding", i.responseEncoding))
	}

	headerMutation, err := i.translator.ResponseHeaders(i.responseHeaders)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response headers: %w", err)
	}

	var mode *extprocv3http.ProcessingMode
	if i.stream && i.responseHeaders[":status"] == "200" {
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
func (i *imageGenerationProcessorUpstreamFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	recordRequestCompletionErr := false
	defer func() {
		if err != nil || recordRequestCompletionErr {
			i.metrics.RecordRequestCompletion(ctx, false, i.requestHeaders)
			return
		}
		if body.EndOfStream {
			i.metrics.RecordRequestCompletion(ctx, true, i.requestHeaders)
		}
	}()

	// Debug logging for raw response body
	if i.logger.Enabled(ctx, slog.LevelDebug) {
		bodyPreview := string(body.Body)
		if len(bodyPreview) > 100 {
			bodyPreview = bodyPreview[:100] + "..."
		}
		i.logger.Debug("raw response body received",
			slog.Int("body_length", len(body.Body)),
			slog.String("body_preview", bodyPreview),
			slog.String("content_encoding", i.responseEncoding),
			slog.Bool("end_of_stream", body.EndOfStream))
	}

	// Decompress the body if needed using common utility.
	decodingResult, err := decodeContentIfNeeded(body.Body, i.responseEncoding)
	if err != nil {
		return nil, err
	}

	// Debug logging for decoded response body
	if i.logger.Enabled(ctx, slog.LevelDebug) {
		decodedBytes, _ := io.ReadAll(decodingResult.reader)
		decodedPreview := string(decodedBytes)
		if len(decodedPreview) > 100 {
			decodedPreview = decodedPreview[:100] + "..."
		}
		i.logger.Debug("decoded response body",
			slog.Int("decoded_length", len(decodedBytes)),
			slog.String("decoded_preview", decodedPreview),
			slog.Bool("was_encoded", decodingResult.isEncoded))

		// Reset reader for translator
		decodingResult.reader = bytes.NewReader(decodedBytes)
	}

	// Assume all responses have a valid status code header.
	if code, _ := strconv.Atoi(i.responseHeaders[":status"]); !isGoodStatusCode(code) {
		var headerMutation *extprocv3.HeaderMutation
		var bodyMutation *extprocv3.BodyMutation
		headerMutation, bodyMutation, err = i.translator.ResponseError(i.responseHeaders, decodingResult.reader)
		if err != nil {
			return nil, fmt.Errorf("failed to transform response error: %w", err)
		}
		if i.span != nil {
			b := bodyMutation.GetBody()
			if b == nil {
				b = body.Body
			}
			i.span.EndSpanOnError(code, b)
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

	// Translator response body transformation (if available)
	var headerMutation *extprocv3.HeaderMutation
	var bodyMutation *extprocv3.BodyMutation
	var tokenUsage translator.LLMTokenUsage
	var imageMetadata translator.ImageGenerationMetadata
	headerMutation, bodyMutation, tokenUsage, imageMetadata, err = i.translator.ResponseBody(i.responseHeaders, decodingResult.reader, body.EndOfStream)
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

	// TODO: we need to investigate if we need to accumulate the token usage for streaming responses.
	i.costs.InputTokens += tokenUsage.InputTokens
	i.costs.OutputTokens += tokenUsage.OutputTokens
	i.costs.TotalTokens += tokenUsage.TotalTokens

	// Update metrics with token usage (input/output only per OTEL spec).
	i.metrics.RecordTokenUsage(ctx, tokenUsage.InputTokens, tokenUsage.OutputTokens, i.requestHeaders)

	// Record image generation metrics
	i.metrics.RecordImageGeneration(ctx, imageMetadata.ImageCount, imageMetadata.Model, imageMetadata.Size, i.requestHeaders)

	if body.EndOfStream && len(i.config.requestCosts) > 0 {
		metadata, err := buildDynamicMetadata(i.config, &i.costs, i.requestHeaders, i.backendName)
		if err != nil {
			return nil, fmt.Errorf("failed to build dynamic metadata: %w", err)
		}
		resp.DynamicMetadata = metadata
	}

	if body.EndOfStream && i.span != nil {
		i.span.EndSpan()
	}
	return resp, nil
}

// SetBackend implements [Processor.SetBackend].
func (i *imageGenerationProcessorUpstreamFilter) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, routeProcessor Processor) (err error) {
	defer func() {
		if err != nil {
			i.metrics.RecordRequestCompletion(ctx, false, i.requestHeaders)
		}
	}()
	pickedEndpoint, isEndpointPicker := i.requestHeaders[internalapi.EndpointPickerHeaderKey]
	rp, ok := routeProcessor.(*imageGenerationProcessorRouterFilter)
	if !ok {
		panic("BUG: expected routeProcessor to be of type *imageGenerationProcessorRouterFilter")
	}
	rp.upstreamFilterCount++
	i.metrics.SetBackend(b)
	i.modelNameOverride = b.ModelNameOverride
	i.backendName = b.Name
	if err = i.selectTranslator(b.Schema); err != nil {
		return fmt.Errorf("failed to select translator: %w", err)
	}

	// Debug logging for backend selection
	if i.logger.Enabled(ctx, slog.LevelDebug) {
		i.logger.Debug("backend selected for image generation",
			slog.String("backend_name", b.Name),
			slog.String("schema_name", string(b.Schema.Name)),
			slog.String("schema_version", b.Schema.Version),
			slog.String("model_override", i.modelNameOverride),
			slog.Bool("translator_set", i.translator != nil))
	}
	i.handler = backendHandler
	i.headerMutator = headermutator.NewHeaderMutator(b.HeaderMutation, rp.requestHeaders)
	// Sync header with backend model so header-derived labels/CEL use the actual model.
	if i.modelNameOverride != "" {
		i.requestHeaders[i.config.modelNameHeaderKey] = string(i.modelNameOverride)
		// Update metrics with the overridden model
		i.metrics.SetRequestModel(internalapi.RequestModel(i.modelNameOverride))
	}
	i.originalRequestBody = rp.originalRequestBody
	i.originalRequestBodyRaw = rp.originalRequestBodyRaw
	i.onRetry = rp.upstreamFilterCount > 1

	// Set streaming flag for GPT-Image-1 requests
	// Image generation streaming not supported in current SDK params; keep false.
	i.stream = false

	if isEndpointPicker {
		if i.logger.Enabled(ctx, slog.LevelDebug) {
			i.logger.Debug("selected backend", slog.String("picked_endpoint", pickedEndpoint), slog.String("backendName", b.Name), slog.String("modelNameOverride", i.modelNameOverride), slog.Bool("stream", i.stream))
		}
	}
	rp.upstreamFilter = i
	i.span = rp.span
	return
}

func parseOpenAIImageGenerationBody(body *extprocv3.HttpBody) (modelName string, rb *openaisdk.ImageGenerateParams, err error) {
	var openAIReq openaisdk.ImageGenerateParams
	if err := json.Unmarshal(body.Body, &openAIReq); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	return string(openAIReq.Model), &openAIReq, nil
}
