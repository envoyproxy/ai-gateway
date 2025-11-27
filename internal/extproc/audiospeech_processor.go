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
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

func AudioSpeechProcessorFactory(f metrics.AudioSpeechMetricsFactory) ProcessorFactory {
	return func(config *filterapi.RuntimeConfig, requestHeaders map[string]string, logger *slog.Logger, _ tracing.Tracing, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("processor", "audio-speech", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &audioSpeechProcessorRouterFilter{
				config:         config,
				requestHeaders: requestHeaders,
				logger:         logger,
			}, nil
		}
		return &audioSpeechProcessorUpstreamFilter{
			config:         config,
			requestHeaders: requestHeaders,
			logger:         logger,
			metrics:        f(),
		}, nil
	}
}

type audioSpeechProcessorRouterFilter struct {
	passThroughProcessor
	upstreamFilter         Processor
	logger                 *slog.Logger
	config                 *filterapi.RuntimeConfig
	requestHeaders         map[string]string
	originalRequestBody    *openai.AudioSpeechRequest
	originalRequestBodyRaw []byte
	upstreamFilterCount    int
}

func (a *audioSpeechProcessorRouterFilter) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	if a.upstreamFilter != nil {
		return a.upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return a.passThroughProcessor.ProcessResponseHeaders(ctx, headerMap)
}

func (a *audioSpeechProcessorRouterFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	if a.upstreamFilter != nil {
		return a.upstreamFilter.ProcessResponseBody(ctx, body)
	}
	return a.passThroughProcessor.ProcessResponseBody(ctx, body)
}

func (a *audioSpeechProcessorRouterFilter) ProcessRequestBody(_ context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	model, body, err := parseAudioSpeechBody(rawBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

	a.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = model

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: internalapi.ModelNameHeaderKeyDefault, RawValue: []byte(model)},
	}, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: originalPathHeader, RawValue: []byte(a.requestHeaders[":path"])},
	})

	a.originalRequestBody = body
	a.originalRequestBodyRaw = rawBody.Body

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

type audioSpeechProcessorUpstreamFilter struct {
	logger                 *slog.Logger
	config                 *filterapi.RuntimeConfig
	requestHeaders         map[string]string
	responseHeaders        map[string]string
	responseEncoding       string
	modelNameOverride      internalapi.ModelNameOverride
	backendName            string
	handler                filterapi.BackendAuthHandler
	headerMutator          *headermutator.HeaderMutator
	originalRequestBodyRaw []byte
	originalRequestBody    *openai.AudioSpeechRequest
	translator             translator.AudioSpeechTranslator
	onRetry                bool
	costs                  translator.LLMTokenUsage
	metrics                metrics.AudioSpeechMetrics
}

func (a *audioSpeechProcessorUpstreamFilter) selectTranslator(out filterapi.VersionedAPISchema) error {
	switch out.Name {
	case filterapi.APISchemaOpenAI:
		a.translator = translator.NewAudioSpeechOpenAIToOpenAITranslator(out.Version, a.modelNameOverride)
	case filterapi.APISchemaGCPVertexAI:
		a.translator = translator.NewAudioSpeechOpenAIToGCPVertexAITranslator(a.modelNameOverride)
	default:
		return fmt.Errorf("unsupported API schema: backend=%s", out)
	}
	return nil
}

func (a *audioSpeechProcessorUpstreamFilter) ProcessRequestHeaders(ctx context.Context, _ *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			a.metrics.RecordRequestCompletion(ctx, false, a.requestHeaders)
		}
	}()

	a.metrics.StartRequest(a.requestHeaders)
	a.metrics.SetOriginalModel(a.originalRequestBody.Model)
	reqModel := cmp.Or(a.requestHeaders[internalapi.ModelNameHeaderKeyDefault], a.originalRequestBody.Model)
	a.metrics.SetRequestModel(reqModel)

	newHeaders, mutatedBody, err := a.translator.RequestBody(a.originalRequestBodyRaw, a.originalRequestBody, a.onRetry)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}

	if mutatedBody != nil {
		a.logger.Info("translated request body",
			slog.String("backend", a.backendName),
			slog.String("original_model", a.originalRequestBody.Model),
			slog.String("translated_body", string(mutatedBody)),
		)
	}

	headerMutation := &extprocv3.HeaderMutation{}
	for _, h := range newHeaders {
		headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
			AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
			Header:       &corev3.HeaderValue{Key: h.Key(), RawValue: []byte(h.Value())},
		})
	}

	if h := a.headerMutator; h != nil {
		sets, removes := a.headerMutator.Mutate(a.requestHeaders, a.onRetry)
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
		a.requestHeaders[h.Header.Key] = string(h.Header.RawValue)
	}
	if h := a.handler; h != nil {
		var hdrs []internalapi.Header
		hdrs, err = h.Do(ctx, a.requestHeaders, mutatedBody)
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

	var bodyMutation *extprocv3.BodyMutation
	if mutatedBody != nil {
		bodyMutation = &extprocv3.BodyMutation{Mutation: &extprocv3.BodyMutation_Body{Body: mutatedBody}}
	}

	var dm *structpb.Struct
	if mutatedBody != nil {
		dm = buildContentLengthDynamicMetadataOnRequest(len(mutatedBody))
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

func (a *audioSpeechProcessorUpstreamFilter) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	panic("BUG: ProcessRequestBody should not be called in the upstream filter")
}

func (a *audioSpeechProcessorUpstreamFilter) ProcessResponseHeaders(ctx context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			a.metrics.RecordRequestCompletion(ctx, false, a.requestHeaders)
		}
	}()

	a.responseHeaders = headersToMap(headers)
	if enc := a.responseHeaders["content-encoding"]; enc != "" {
		a.responseEncoding = enc
	}
	newHeaders, err := a.translator.ResponseHeaders(a.responseHeaders)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response headers: %w", err)
	}

	var headerMutation *extprocv3.HeaderMutation
	if len(newHeaders) > 0 {
		headerMutation = &extprocv3.HeaderMutation{}
		for _, h := range newHeaders {
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
				Header:       &corev3.HeaderValue{Key: h.Key(), RawValue: []byte(h.Value())},
			})
		}
	}

	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{
		ResponseHeaders: &extprocv3.HeadersResponse{
			Response: &extprocv3.CommonResponse{HeaderMutation: headerMutation},
		},
	}}, nil
}

func (a *audioSpeechProcessorUpstreamFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	recordRequestCompletionErr := false
	defer func() {
		if err != nil || recordRequestCompletionErr {
			a.metrics.RecordRequestCompletion(ctx, false, a.requestHeaders)
			return
		}
		if body.EndOfStream {
			a.metrics.RecordRequestCompletion(ctx, true, a.requestHeaders)
		}
	}()

	decodingResult, err := decodeContentIfNeeded(body.Body, a.responseEncoding)
	if err != nil {
		return nil, err
	}

	if code, _ := strconv.Atoi(a.responseHeaders[":status"]); !isGoodStatusCode(code) {
		var newHeaders []internalapi.Header
		var mutatedBody []byte
		newHeaders, mutatedBody, err = a.translator.ResponseError(a.responseHeaders, decodingResult.reader)
		if err != nil {
			return nil, fmt.Errorf("failed to transform response error: %w", err)
		}
		recordRequestCompletionErr = true

		var headerMutation *extprocv3.HeaderMutation
		if len(newHeaders) > 0 {
			headerMutation = &extprocv3.HeaderMutation{}
			for _, h := range newHeaders {
				headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
					AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
					Header:       &corev3.HeaderValue{Key: h.Key(), RawValue: []byte(h.Value())},
				})
			}
		}

		var bodyMutation *extprocv3.BodyMutation
		if mutatedBody != nil {
			bodyMutation = &extprocv3.BodyMutation{Mutation: &extprocv3.BodyMutation_Body{Body: mutatedBody}}
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

	newHeaders, mutatedBody, tokenUsage, responseModel, err := a.translator.ResponseBody(a.responseHeaders, decodingResult.reader, body.EndOfStream, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}

	var headerMutation *extprocv3.HeaderMutation
	var bodyMutation *extprocv3.BodyMutation
	if mutatedBody != nil {
		bodyMutation = &extprocv3.BodyMutation{Mutation: &extprocv3.BodyMutation_Body{Body: mutatedBody}}
	}
	if len(newHeaders) > 0 || (bodyMutation != nil && decodingResult.isEncoded) {
		headerMutation = &extprocv3.HeaderMutation{}
		for _, h := range newHeaders {
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
				Header:       &corev3.HeaderValue{Key: h.Key(), RawValue: []byte(h.Value())},
			})
		}
		if bodyMutation != nil && decodingResult.isEncoded {
			headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, "content-encoding")
		}
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

	a.costs.InputTokens += tokenUsage.InputTokens
	a.costs.TotalTokens += tokenUsage.TotalTokens

	a.metrics.SetResponseModel(responseModel)
	a.metrics.RecordTokenUsage(ctx, tokenUsage.InputTokens, a.requestHeaders)

	if body.EndOfStream && len(a.config.RequestCosts) > 0 {
		resp.DynamicMetadata, err = buildDynamicMetadata(a.config, &a.costs, a.requestHeaders, a.backendName)
		if err != nil {
			return nil, fmt.Errorf("failed to build dynamic metadata: %w", err)
		}
	}

	return resp, nil
}

func (a *audioSpeechProcessorUpstreamFilter) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler filterapi.BackendAuthHandler, routeProcessor Processor) (err error) {
	defer func() {
		if err != nil {
			a.metrics.RecordRequestCompletion(ctx, false, a.requestHeaders)
		}
	}()
	rp, ok := routeProcessor.(*audioSpeechProcessorRouterFilter)
	if !ok {
		panic("BUG: expected routeProcessor to be of type *audioSpeechProcessorRouterFilter")
	}
	rp.upstreamFilterCount++
	a.metrics.SetBackend(b)
	a.modelNameOverride = b.ModelNameOverride
	a.backendName = b.Name
	a.originalRequestBody = rp.originalRequestBody
	a.originalRequestBodyRaw = rp.originalRequestBodyRaw
	a.onRetry = rp.upstreamFilterCount > 1
	if err = a.selectTranslator(b.Schema); err != nil {
		return fmt.Errorf("failed to select translator: %w", err)
	}

	a.handler = backendHandler
	a.headerMutator = headermutator.NewHeaderMutator(b.HeaderMutation, rp.requestHeaders)
	if a.modelNameOverride != "" {
		a.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = a.modelNameOverride
		a.metrics.SetRequestModel(a.modelNameOverride)
	}
	rp.upstreamFilter = a
	return
}

func parseAudioSpeechBody(body *extprocv3.HttpBody) (modelName string, rb *openai.AudioSpeechRequest, err error) {
	var req openai.AudioSpeechRequest
	if err := json.Unmarshal(body.Body, &req); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	return req.Model, &req, nil
}
