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
	"fmt"
	"io"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/extproc/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
)

// EmbeddingProcessorFactory returns a factory method to instantiate the embedding processor.
func EmbeddingProcessorFactory(em x.EmbeddingMetrics) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, isUpstreamFilter bool) (Processor, error) {
		if config.schema.Name != filterapi.APISchemaOpenAI {
			return nil, fmt.Errorf("unsupported API schema: %s", config.schema.Name)
		}
		logger = logger.With("processor", "embedding", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &embeddingProcessorRouterFilter{
				config:         config,
				requestHeaders: requestHeaders,
				logger:         logger,
			}, nil
		}
		return &embeddingProcessorUpstreamFilter{
			logger:  logger,
			config:  config,
			metrics: em,
		}, nil
	}
}

// embeddingProcessorRouterFilter implements [Processor] for the `/v1/embeddings` endpoint.
//
// This is primarily used to select the route for the request based on the model name.
type embeddingProcessorRouterFilter struct {
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

var _ Processor = (*embeddingProcessorRouterFilter)(nil)

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (e *embeddingProcessorRouterFilter) ProcessRequestBody(_ context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	model, body, err := parseOpenAIEmbeddingBody(rawBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

	e.originalRequestBody = body
	e.originalRequestBodyRaw = rawBody.Body

	e.requestHeaders[e.config.modelNameHeaderKey] = model
	routeName, err := e.config.router.Calculate(e.requestHeaders)
	if err != nil {
		return nil, fmt.Errorf("failed to route request: %w", err)
	}

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: e.config.modelNameHeaderKey, RawValue: []byte(model)},
	}, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: e.config.selectedRouteHeaderKey, RawValue: []byte(routeName)},
	})

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
func (e *embeddingProcessorRouterFilter) ProcessResponseHeaders(ctx context.Context, headers *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	if e.upstreamFilter != nil {
		return e.upstreamFilter.ProcessResponseHeaders(ctx, headers)
	}
	return e.passThroughProcessor.ProcessResponseHeaders(ctx, headers)
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (e *embeddingProcessorRouterFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	if e.upstreamFilter != nil {
		return e.upstreamFilter.ProcessResponseBody(ctx, body)
	}
	return e.passThroughProcessor.ProcessResponseBody(ctx, body)
}

// embeddingProcessorUpstreamFilter implements [Processor] for the `/v1/embeddings` endpoint at the upstream filter.
//
// This is created per retry and handles the translation as well as the authentication of the request.
type embeddingProcessorUpstreamFilter struct {
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
	metrics x.EmbeddingMetrics
}

var _ Processor = (*embeddingProcessorUpstreamFilter)(nil)

// selectTranslator selects the translator based on the output schema.
func (e *embeddingProcessorUpstreamFilter) selectTranslator(out filterapi.VersionedAPISchema) error {
	switch out.Name {
	case filterapi.APISchemaOpenAI:
		e.translator = translator.NewEmbeddingOpenAIToOpenAITranslator(out.Version, e.modelNameOverride)
	case filterapi.APISchemaAWSBedrock:
		e.translator = translator.NewEmbeddingOpenAIToAWSBedrockTranslator(e.modelNameOverride)
	case filterapi.APISchemaAzureOpenAI:
		e.translator = translator.NewEmbeddingOpenAIToAzureOpenAITranslator(out.Version, e.modelNameOverride)
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
func (e *embeddingProcessorUpstreamFilter) ProcessRequestHeaders(ctx context.Context, _ *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
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
					HeaderMutation: headerMutation,
					BodyMutation:   bodyMutation,
				},
			},
		},
	}, nil
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (e *embeddingProcessorUpstreamFilter) ProcessRequestBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	// This should not be called since we set RequestBodyMode to SKIP in ProcessRequestHeaders
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestBody{}}, nil
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (e *embeddingProcessorUpstreamFilter) ProcessResponseHeaders(_ context.Context, headers *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	e.responseHeaders = headersToMap(headers)
	e.responseEncoding = e.responseHeaders["content-encoding"]
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{}}, nil
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (e *embeddingProcessorUpstreamFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
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

	// Record token usage metrics if available
	if tokenUsage != nil {
		e.metrics.RecordTokenUsage(ctx, uint32(tokenUsage.PromptTokens), uint32(tokenUsage.TotalTokens)) // Embeddings don't have completion tokens
	}

	if bodyMutation != nil && isGzip {
		// Re-compress the body if it was originally gzipped
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		if _, err := gw.Write(bodyMutation.GetBody()); err != nil {
			return nil, fmt.Errorf("failed to re-compress response body: %w", err)
		}
		if err := gw.Close(); err != nil {
			return nil, fmt.Errorf("failed to close gzip writer: %w", err)
		}
		bodyMutation = &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: buf.Bytes()},
		}
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

// SetBackend implements [Processor.SetBackend].
func (e *embeddingProcessorUpstreamFilter) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, routeProcessor Processor) (err error) {
	defer func() {
		e.metrics.RecordRequestCompletion(ctx, err == nil)
	}()
	rp, ok := routeProcessor.(*embeddingProcessorRouterFilter)
	if !ok {
		panic("BUG: expected routeProcessor to be of type *embeddingProcessorRouterFilter")
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
	return nil
}

// parseOpenAIEmbeddingBody parses the request body and extracts the model name and the parsed body.
func parseOpenAIEmbeddingBody(rawBody *extprocv3.HttpBody) (model string, body *openai.EmbeddingRequest, err error) {
	body = &openai.EmbeddingRequest{}
	if err = json.Unmarshal(rawBody.Body, body); err != nil {
		return "", nil, err
	}
	return body.Model, body, nil
}
