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
	"github.com/envoyproxy/ai-gateway/internal/extproc/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// CompletionsProcessorFactory returns a factory method to instantiate the completions processor.
func CompletionsProcessorFactory(_ interface{}) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, _ tracing.Tracing, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("processor", "completions", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &completionsProcessorRouterFilter{
				config:         config,
				requestHeaders: requestHeaders,
				logger:         logger,
			}, nil
		}
		return &completionsProcessorUpstreamFilter{
			config:         config,
			requestHeaders: requestHeaders,
			logger:         logger,
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
func (c *completionsProcessorRouterFilter) ProcessRequestBody(_ context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
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
		rawBody.Body, err = sjson.SetBytesOptions(rawBody.Body, "stream_options.include_usage", true, translator.SJSONOptions)
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

	// Create a header mutation without tracing.
	headerMutation := &extprocv3.HeaderMutation{
		SetHeaders: additionalHeaders,
	}

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
	headerMutation, bodyMutation, err := c.translator.RequestBody(c.originalRequestBodyRaw, c.originalRequestBody, c.onRetry)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}
	if headerMutation == nil {
		headerMutation = &extprocv3.HeaderMutation{}
	}

	// Apply header mutations from the route and also restore original headers on retry.
	if h := c.headerMutator; h != nil {
		if hm := c.headerMutator.Mutate(c.requestHeaders, c.onRetry); hm != nil {
			headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, hm.RemoveHeaders...)
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, hm.SetHeaders...)
		}
	}

	for _, h := range headerMutation.SetHeaders {
		c.requestHeaders[h.Header.Key] = string(h.Header.RawValue)
	}
	if h := c.handler; h != nil {
		if err = h.Do(ctx, c.requestHeaders, headerMutation, bodyMutation); err != nil {
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
func (c *completionsProcessorUpstreamFilter) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	return nil, fmt.Errorf("%w: ProcessRequestBody", errUnexpectedCall)
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (c *completionsProcessorUpstreamFilter) ProcessResponseHeaders(_ context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
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
func (c *completionsProcessorUpstreamFilter) ProcessResponseBody(_ context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
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

	headerMutation, bodyMutation, tokenUsage, responseModel, err := c.translator.ResponseBody(c.responseHeaders, decodingResult.reader, body.EndOfStream)
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

	// Accumulate token usage for completions.
	c.costs.InputTokens += tokenUsage.InputTokens
	c.costs.OutputTokens += tokenUsage.OutputTokens
	c.costs.TotalTokens += tokenUsage.TotalTokens

	// Log the response model for debugging
	if responseModel != "" {
		c.logger.Debug("completion response model", "model", responseModel)
	}

	if body.EndOfStream && len(c.config.requestCosts) > 0 {
		resp.DynamicMetadata, err = buildDynamicMetadata(c.config, &c.costs, c.requestHeaders, c.backendName)
		if err != nil {
			return nil, fmt.Errorf("failed to build dynamic metadata: %w", err)
		}
	}

	return resp, nil
}

// SetBackend implements [Processor.SetBackend].
func (c *completionsProcessorUpstreamFilter) SetBackend(_ context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, routeProcessor Processor) (err error) {
	rp, ok := routeProcessor.(*completionsProcessorRouterFilter)
	if !ok {
		panic("BUG: expected routeProcessor to be of type *completionsProcessorRouterFilter")
	}
	rp.upstreamFilterCount++
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
	return
}

func parseOpenAICompletionBody(body *extprocv3.HttpBody) (modelName string, rb *openai.CompletionRequest, err error) {
	var openAIReq openai.CompletionRequest
	if err := json.Unmarshal(body.Body, &openAIReq); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	return openAIReq.Model, &openAIReq, nil
}
