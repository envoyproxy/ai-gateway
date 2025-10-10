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

// EmbeddingsProcessorFactory returns a factory method to instantiate the embeddings processor.
func EmbeddingsProcessorFactory(em metrics.EmbeddingsMetrics) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, tracing tracing.Tracing, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("processor", "embeddings", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &embeddingsProcessorRouterFilter{
				config:         config,
				tracer:         tracing.EmbeddingsTracer(),
				requestHeaders: requestHeaders,
				logger:         logger,
			}, nil
		}
		return &embeddingsProcessorUpstreamFilter{
			config:         config,
			requestHeaders: requestHeaders,
			logger:         logger,
			metrics:        em,
		}, nil
	}
}

// embeddingsProcessorRouterFilter implements [Processor] for the `/v1/embeddings` endpoint.
//
// This is primarily used to select the route for the request based on the model name.
type embeddingsProcessorRouterFilter struct {
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
	originalRequestBody    *openai.EmbeddingRequest
	originalRequestBodyRaw []byte
	// tracer is the tracer used for requests.
	tracer tracing.EmbeddingsTracer
	// span is the tracing span for this request, created in ProcessRequestBody.
	span tracing.EmbeddingsSpan
	// upstreamFilterCount is the number of upstream filters that have been processed.
	// This is used to determine if the request is a retry request.
	upstreamFilterCount int
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (e *embeddingsProcessorRouterFilter) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// e.upstreamFilter can be nil.
	if e.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		return e.upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return e.passThroughProcessor.ProcessResponseHeaders(ctx, headerMap)
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (e *embeddingsProcessorRouterFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// e.upstreamFilter can be nil.
	if e.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		return e.upstreamFilter.ProcessResponseBody(ctx, body)
	}
	return e.passThroughProcessor.ProcessResponseBody(ctx, body)
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (e *embeddingsProcessorRouterFilter) ProcessRequestBody(ctx context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	originalModel, body, err := parseOpenAIEmbeddingBody(rawBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

	e.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = originalModel

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		// Set the model name to the request header with the key `x-ai-eg-model`.
		Header: &corev3.HeaderValue{Key: internalapi.ModelNameHeaderKeyDefault, RawValue: []byte(originalModel)},
	}, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: originalPathHeader, RawValue: []byte(e.requestHeaders[":path"])},
	})
	e.originalRequestBody = body
	e.originalRequestBodyRaw = rawBody.Body

	// Tracing may need to inject headers, so create a header mutation here.
	headerMutation := &extprocv3.HeaderMutation{
		SetHeaders: additionalHeaders,
	}
	e.span = e.tracer.StartSpanAndInjectHeaders(
		ctx,
		e.requestHeaders,
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

// embeddingsProcessorUpstreamFilter implements [Processor] for the `/v1/embeddings` endpoint at the upstream filter.
//
// This is created per retry and handles the translation as well as the authentication of the request.
type embeddingsProcessorUpstreamFilter struct {
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
	originalRequestBody    *openai.EmbeddingRequest
	translator             translator.OpenAIEmbeddingTranslator
	// onRetry is true if this is a retry request at the upstream filter.
	onRetry bool
	// cost is the cost of the request that is accumulated during the processing of the response.
	costs translator.LLMTokenUsage
	// metrics tracking.
	metrics metrics.EmbeddingsMetrics
	// span is the tracing span for this request, inherited from the router filter.
	span tracing.EmbeddingsSpan
}

// selectTranslator selects the translator based on the output schema.
func (e *embeddingsProcessorUpstreamFilter) selectTranslator(out filterapi.VersionedAPISchema) error {
	switch out.Name {
	case filterapi.APISchemaOpenAI:
		e.translator = translator.NewEmbeddingOpenAIToOpenAITranslator(out.Version, e.modelNameOverride, e.span)
	case filterapi.APISchemaAzureOpenAI:
		e.translator = translator.NewEmbeddingOpenAIToAzureOpenAITranslator(out.Version, e.modelNameOverride, e.span)
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
func (e *embeddingsProcessorUpstreamFilter) ProcessRequestHeaders(ctx context.Context, _ *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			e.metrics.RecordRequestCompletion(ctx, false, e.requestHeaders)
		}
	}()

	// Start tracking metrics for this request.
	e.metrics.StartRequest(e.requestHeaders)
	// Set the original model from the request body before any overrides
	e.metrics.SetOriginalModel(e.originalRequestBody.Model)
	// Set the request model for metrics from the original model or override if applied.
	reqModel := cmp.Or(e.requestHeaders[internalapi.ModelNameHeaderKeyDefault], e.originalRequestBody.Model)
	e.metrics.SetRequestModel(reqModel)

	headerMutation, bodyMutation, err := e.translator.RequestBody(e.originalRequestBodyRaw, e.originalRequestBody, e.onRetry)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}
	if headerMutation == nil {
		headerMutation = &extprocv3.HeaderMutation{}
	}

	// Apply header mutations from the route and also restore original headers on retry.
	if h := e.headerMutator; h != nil {
		if hm := e.headerMutator.Mutate(e.requestHeaders, e.onRetry); hm != nil {
			headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, hm.RemoveHeaders...)
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, hm.SetHeaders...)
		}
	}

	for _, h := range headerMutation.SetHeaders {
		e.requestHeaders[h.Header.Key] = string(h.Header.RawValue)
	}
	if h := e.handler; h != nil {
		if err = h.Do(ctx, e.requestHeaders, headerMutation, bodyMutation); err != nil {
			return nil, fmt.Errorf("failed to do auth request: %w", err)
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
func (e *embeddingsProcessorUpstreamFilter) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	panic("BUG: ProcessRequestBody should not be called in the upstream filter")
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (e *embeddingsProcessorUpstreamFilter) ProcessResponseHeaders(ctx context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			e.metrics.RecordRequestCompletion(ctx, false, e.requestHeaders)
		}
	}()

	e.responseHeaders = headersToMap(headers)
	if enc := e.responseHeaders["content-encoding"]; enc != "" {
		e.responseEncoding = enc
	}
	headerMutation, err := e.translator.ResponseHeaders(e.responseHeaders)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response headers: %w", err)
	}
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{
		ResponseHeaders: &extprocv3.HeadersResponse{
			Response: &extprocv3.CommonResponse{HeaderMutation: headerMutation},
		},
	}}, nil
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (e *embeddingsProcessorUpstreamFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	recordRequestCompletionErr := false
	defer func() {
		if err != nil || recordRequestCompletionErr {
			e.metrics.RecordRequestCompletion(ctx, false, e.requestHeaders)
			return
		}
		if body.EndOfStream {
			e.metrics.RecordRequestCompletion(ctx, true, e.requestHeaders)
		}
	}()

	// Decompress the body if needed using common utility.
	decodingResult, err := decodeContentIfNeeded(body.Body, e.responseEncoding)
	if err != nil {
		return nil, err
	}

	// Assume all responses have a valid status code header.
	if code, _ := strconv.Atoi(e.responseHeaders[":status"]); !isGoodStatusCode(code) {
		var headerMutation *extprocv3.HeaderMutation
		var bodyMutation *extprocv3.BodyMutation
		headerMutation, bodyMutation, err = e.translator.ResponseError(e.responseHeaders, decodingResult.reader)
		if err != nil {
			return nil, fmt.Errorf("failed to transform response error: %w", err)
		}
		if e.span != nil {
			b := bodyMutation.GetBody()
			if b == nil {
				b = body.Body
			}
			e.span.EndSpanOnError(code, b)
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

	headerMutation, bodyMutation, tokenUsage, responseModel, err := e.translator.ResponseBody(e.responseHeaders, decodingResult.reader, body.EndOfStream)
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

	// Accumulate token usage for embeddings (only input and total tokens are relevant).
	e.costs.InputTokens += tokenUsage.InputTokens
	e.costs.TotalTokens += tokenUsage.TotalTokens

	e.metrics.SetResponseModel(responseModel)

	// Update metrics with token usage.
	e.metrics.RecordTokenUsage(ctx, tokenUsage.InputTokens, e.requestHeaders)

	if body.EndOfStream && len(e.config.requestCosts) > 0 {
		resp.DynamicMetadata, err = buildDynamicMetadata(e.config, &e.costs, e.requestHeaders, e.backendName)
		if err != nil {
			return nil, fmt.Errorf("failed to build dynamic metadata: %w", err)
		}
	}

	if body.EndOfStream && e.span != nil {
		e.span.EndSpan()
	}
	return resp, nil
}

// SetBackend implements [Processor.SetBackend].
func (e *embeddingsProcessorUpstreamFilter) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, routeProcessor Processor) (err error) {
	defer func() {
		if err != nil {
			e.metrics.RecordRequestCompletion(ctx, false, e.requestHeaders)
		}
	}()
	rp, ok := routeProcessor.(*embeddingsProcessorRouterFilter)
	if !ok {
		panic("BUG: expected routeProcessor to be of type *embeddingsProcessorRouterFilter")
	}
	rp.upstreamFilterCount++
	e.metrics.SetBackend(b)
	e.modelNameOverride = b.ModelNameOverride
	e.backendName = b.Name
	e.originalRequestBody = rp.originalRequestBody
	e.originalRequestBodyRaw = rp.originalRequestBodyRaw
	e.onRetry = rp.upstreamFilterCount > 1
	e.span = rp.span
	if err = e.selectTranslator(b.Schema); err != nil {
		return fmt.Errorf("failed to select translator: %w", err)
	}
	e.handler = backendHandler
	e.headerMutator = headermutator.NewHeaderMutator(b.HeaderMutation, rp.requestHeaders)
	// Header-derived labels/CEL must be able to see the overridden request model.
	if e.modelNameOverride != "" {
		e.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = e.modelNameOverride
		// Update metrics with the overridden model
		e.metrics.SetRequestModel(e.modelNameOverride)
	}
	rp.upstreamFilter = e
	return
}

func parseOpenAIEmbeddingBody(body *extprocv3.HttpBody) (modelName string, rb *openai.EmbeddingRequest, err error) {
	var openAIReq openai.EmbeddingRequest
	if err := json.Unmarshal(body.Body, &openAIReq); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	return openAIReq.Model, &openAIReq, nil
}
