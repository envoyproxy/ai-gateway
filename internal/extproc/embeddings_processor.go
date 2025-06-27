// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/extproc/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
)

// EmbeddingsProcessorFactory returns a factory method to instantiate the embeddings processor.
func EmbeddingsProcessorFactory(em x.EmbeddingsMetrics) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, isUpstreamFilter bool) (Processor, error) {
		if config.schema.Name != filterapi.APISchemaOpenAI {
			return nil, fmt.Errorf("unsupported API schema: %s", config.schema.Name)
		}
		logger = logger.With("processor", "embeddings", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &embeddingsProcessorRouterFilter{
				config:         config,
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
func (e *embeddingsProcessorRouterFilter) ProcessRequestBody(_ context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	model, body, err := parseOpenAIEmbeddingBody(rawBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

	e.requestHeaders[e.config.modelNameHeaderKey] = model
	routeName, err := e.config.router.Calculate(e.requestHeaders)
	if err != nil {
		if errors.Is(err, x.ErrNoMatchingRule) {
			return &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_ImmediateResponse{
					ImmediateResponse: &extprocv3.ImmediateResponse{
						Status: &typev3.HttpStatus{Code: typev3.StatusCode_NotFound},
						Body:   []byte(err.Error()),
					},
				},
			}, nil
		}
		return nil, fmt.Errorf("failed to calculate route: %w", err)
	}

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		// Set the model name to the request header with the key `x-ai-eg-model`.
		Header: &corev3.HeaderValue{Key: e.config.modelNameHeaderKey, RawValue: []byte(model)},
	}, &corev3.HeaderValueOption{
		// Also set the selected backend to the request header with the key specified in the config.
		Header: &corev3.HeaderValue{Key: e.config.selectedRouteHeaderKey, RawValue: []byte(routeName)},
	}, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: originalPathHeader, RawValue: []byte(e.requestHeaders[":path"])},
	})
	e.originalRequestBody = body
	e.originalRequestBodyRaw = rawBody.Body
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

// embeddingsProcessorUpstreamFilter implements [Processor] for the `/v1/embeddings` endpoint at the upstream filter.
//
// This is created per retry and handles the translation as well as the authentication of the request.
type embeddingsProcessorUpstreamFilter struct {
	logger                 *slog.Logger
	config                 *processorConfig
	requestHeaders         map[string]string
	responseHeaders        map[string]string
	responseEncoding       string
	modelNameOverride      string
	handler                backendauth.Handler
	originalRequestBodyRaw []byte
	originalRequestBody    *openai.EmbeddingRequest
	translator             translator.OpenAIEmbeddingTranslator
	// onRetry is true if this is a retry request at the upstream filter.
	onRetry bool
	// cost is the cost of the request that is accumulated during the processing of the response.
	costs translator.LLMTokenUsage
	// metrics tracking.
	metrics x.EmbeddingsMetrics
}

// selectTranslator selects the translator based on the output schema.
func (e *embeddingsProcessorUpstreamFilter) selectTranslator(out filterapi.VersionedAPISchema) error {
	switch out.Name {
	case filterapi.APISchemaOpenAI:
		e.translator = translator.NewEmbeddingOpenAIToOpenAITranslator(out.Version, e.modelNameOverride)
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
			e.metrics.RecordRequestCompletion(ctx, false)
		}
	}()

	// Start tracking metrics for this request.
	e.metrics.StartRequest(e.requestHeaders)
	e.metrics.SetModel(e.requestHeaders[e.config.modelNameHeaderKey])

	headerMutation, bodyMutation, err := e.translator.RequestBody(e.originalRequestBodyRaw, e.originalRequestBody, e.onRetry)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}
	if headerMutation == nil {
		headerMutation = &extprocv3.HeaderMutation{}
	} else {
		for _, h := range headerMutation.SetHeaders {
			e.requestHeaders[h.Header.Key] = string(h.Header.RawValue)
		}
	}
	if h := e.handler; h != nil {
		if err = h.Do(ctx, e.requestHeaders, headerMutation, bodyMutation); err != nil {
			return nil, fmt.Errorf("failed to do auth request: %w", err)
		}
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
			e.metrics.RecordRequestCompletion(ctx, false)
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
	defer func() {
		e.metrics.RecordRequestCompletion(ctx, err == nil)
	}()
	var br io.Reader
	var isGzip bool
	switch e.responseEncoding {
	case "gzip":
		br, err = gzip.NewReader(bytes.NewReader(body.Body))
		if err != nil {
			return nil, fmt.Errorf("failed to decode gzip: %w", err)
		}
		isGzip = true
	default:
		br = bytes.NewReader(body.Body)
	}

	headerMutation, bodyMutation, tokenUsage, err := e.translator.ResponseBody(e.responseHeaders, br, body.EndOfStream)
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

	// Accumulate token usage for embeddings (only input and total tokens are relevant)
	e.costs.InputTokens += tokenUsage.InputTokens
	e.costs.TotalTokens += tokenUsage.TotalTokens

	// Update metrics with token usage.
	e.metrics.RecordTokenUsage(ctx, tokenUsage.InputTokens, tokenUsage.TotalTokens)

	if body.EndOfStream && len(e.config.requestCosts) > 0 {
		resp.DynamicMetadata, err = buildDynamicMetadata(e.config, &e.costs, e.requestHeaders)
		if err != nil {
			return nil, fmt.Errorf("failed to build dynamic metadata: %w", err)
		}
	}

	return resp, nil
}

// SetBackend implements [Processor.SetBackend].
func (e *embeddingsProcessorUpstreamFilter) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, routeProcessor Processor) (err error) {
	defer func() {
		e.metrics.RecordRequestCompletion(ctx, err == nil)
	}()
	rp, ok := routeProcessor.(*embeddingsProcessorRouterFilter)
	if !ok {
		panic("BUG: expected routeProcessor to be of type *embeddingsProcessorRouterFilter")
	}
	rp.upstreamFilterCount++
	e.metrics.SetBackend(b)
	e.modelNameOverride = b.ModelNameOverride
	if err = e.selectTranslator(b.Schema); err != nil {
		return fmt.Errorf("failed to select translator: %w", err)
	}
	e.handler = backendHandler
	e.originalRequestBody = rp.originalRequestBody
	e.originalRequestBodyRaw = rp.originalRequestBodyRaw
	e.onRetry = rp.upstreamFilterCount > 1
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
