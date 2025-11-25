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

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

// RealtimeClientSecretsProcessorFactory returns a ProcessorFactory for the realtime client_secrets endpoint.
func RealtimeClientSecretsProcessorFactory() ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, _ tracing.Tracing, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("processor", "realtime-client-secrets", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &realtimeClientSecretsProcessorRouterFilter{
				config:         config,
				requestHeaders: requestHeaders,
				logger:         logger,
			}, nil
		}
		return &realtimeClientSecretsProcessorUpstreamFilter{
			config:         config,
			requestHeaders: requestHeaders,
			logger:         logger,
		}, nil
	}
}

// realtimeClientSecretsProcessorRouterFilter implements [Processor] for the `/v1/realtime/client_secrets` endpoint.
type realtimeClientSecretsProcessorRouterFilter struct {
	passThroughProcessor
	upstreamFilter         Processor
	logger                 *slog.Logger
	config                 *processorConfig
	requestHeaders         map[string]string
	originalRequestBody    *openai.RealtimeClientSecretRequest
	originalRequestBodyRaw []byte
}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
func (r *realtimeClientSecretsProcessorRouterFilter) ProcessRequestHeaders(_ context.Context, _ *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{
		RequestHeaders: &extprocv3.HeadersResponse{},
	}}, nil
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (r *realtimeClientSecretsProcessorRouterFilter) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	if r.upstreamFilter != nil {
		return r.upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return r.passThroughProcessor.ProcessResponseHeaders(ctx, headerMap)
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (r *realtimeClientSecretsProcessorRouterFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	if r.upstreamFilter != nil {
		return r.upstreamFilter.ProcessResponseBody(ctx, body)
	}
	return r.passThroughProcessor.ProcessResponseBody(ctx, body)
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (r *realtimeClientSecretsProcessorRouterFilter) ProcessRequestBody(_ context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	// Parse the request body
	var req openai.RealtimeClientSecretRequest
	if err := json.Unmarshal(rawBody.Body, &req); err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

	r.originalRequestBody = &req
	r.originalRequestBodyRaw = rawBody.Body

	// Use a default model if not specified
	model := "gpt-realtime"
	if req.Session != nil && req.Session.Model != "" {
		model = req.Session.Model
	}

	// Set model header for routing and original path for upstream filter lookup
	headerMutation := &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{
				Header: &corev3.HeaderValue{
					Key:      internalapi.ModelNameHeaderKeyDefault,
					RawValue: []byte(model),
				},
			},
			{
				Header: &corev3.HeaderValue{
					Key:      originalPathHeader,
					RawValue: []byte(r.requestHeaders[":path"]),
				},
			},
		},
	}

	// Preserve the original body for the upstream filter to process
	bodyMutation := &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{
			Body: rawBody.Body,
		},
	}

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation:  headerMutation,
					BodyMutation:    bodyMutation,
					ClearRouteCache: true,
				},
			},
		},
	}, nil
}

// SetBackend implements [Processor.SetBackend].
func (r *realtimeClientSecretsProcessorRouterFilter) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, routeProcessor Processor) error {
	upstreamFilter, err := RealtimeClientSecretsProcessorFactory()(r.config, r.requestHeaders, r.logger, nil, true)
	if err != nil {
		return fmt.Errorf("failed to create upstream filter: %w", err)
	}
	if err := upstreamFilter.SetBackend(ctx, b, backendHandler, routeProcessor); err != nil {
		return err
	}
	r.upstreamFilter = upstreamFilter
	return nil
}

type realtimeClientSecretsProcessorUpstreamFilter struct {
	logger         *slog.Logger
	config         *processorConfig
	requestHeaders map[string]string
	translator     translator.RealtimeClientSecretsTranslator
	handler        backendauth.Handler
}

func (r *realtimeClientSecretsProcessorUpstreamFilter) SetBackend(_ context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, _ Processor) error {
	// Store the handler first - it may be nil for backends without auth
	r.handler = backendHandler

	// Select translator based on backend schema
	switch b.Schema.Name {
	case filterapi.APISchemaOpenAI, filterapi.APISchemaAzureOpenAI:
		r.translator = translator.NewRealtimeClientSecretsOpenAITranslator()
	case filterapi.APISchemaGemini, filterapi.APISchemaGCPVertexAI:
		// Determine if using Gemini API key or GCP OAuth2
		useGeminiPath := b.Auth != nil && b.Auth.GeminiAPIKey != nil
		r.translator = translator.NewRealtimeClientSecretsGeminiTranslator(useGeminiPath, r.logger)
	default:
		return fmt.Errorf("unsupported schema for realtime client_secrets: %s", b.Schema.Name)
	}

	return nil
}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
func (r *realtimeClientSecretsProcessorUpstreamFilter) ProcessRequestHeaders(_ context.Context, _ *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	// Override processing mode to enable request body processing in upstream filter
	// This is necessary because we need to translate the request body from OpenAI to Gemini format
	mode := &extprocv3http.ProcessingMode{
		RequestBodyMode: extprocv3http.ProcessingMode_BUFFERED,
	}

	return &extprocv3.ProcessingResponse{
		Response:     &extprocv3.ProcessingResponse_RequestHeaders{},
		ModeOverride: mode,
	}, nil
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (r *realtimeClientSecretsProcessorUpstreamFilter) ProcessRequestBody(ctx context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	// Parse the request
	var req openai.RealtimeClientSecretRequest
	if err := json.Unmarshal(rawBody.Body, &req); err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

	// Translate the request
	headerMutation, bodyMutation, err := r.translator.RequestBody(&req)
	if err != nil {
		return nil, fmt.Errorf("failed to translate request: %w", err)
	}

	// Update request headers with the translated path for auth handler
	// The translator sets the new path in headerMutation, we need to apply it to requestHeaders
	for _, setHeader := range headerMutation.SetHeaders {
		if setHeader.Header.Key == ":path" {
			r.requestHeaders[":path"] = string(setHeader.Header.RawValue)
			break
		}
	}

	// Apply authentication if handler exists (will append API key to the translated path)
	if r.handler != nil {
		authHeaders, err := r.handler.Do(ctx, r.requestHeaders, rawBody.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to apply authentication: %w", err)
		}

		// Replace the path header with the auth-modified one (which includes API key)
		// Remove the original translated path from headerMutation
		var filteredHeaders []*corev3.HeaderValueOption
		for _, setHeader := range headerMutation.SetHeaders {
			if setHeader.Header.Key != ":path" {
				filteredHeaders = append(filteredHeaders, setHeader)
			}
		}
		headerMutation.SetHeaders = filteredHeaders

		// Add auth headers (including the path with API key)
		for _, h := range authHeaders {
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
				Header: &corev3.HeaderValue{
					Key:      h.Key(),
					RawValue: []byte(h.Value()),
				},
			})
		}
	}

	// Remove Content-Length header since we're changing the body size
	// Envoy will recalculate it automatically
	headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, "content-length")

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: headerMutation,
					BodyMutation:   bodyMutation,
				},
			},
		},
	}, nil
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (r *realtimeClientSecretsProcessorUpstreamFilter) ProcessResponseHeaders(_ context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	// Log response headers for debugging
	headers := make(map[string]string)
	for _, h := range headerMap.Headers {
		headers[h.Key] = string(h.RawValue)
	}
	slog.Info("[Gemini Realtime] Received response headers",
		"processor", "realtime-client-secrets",
		"isUpstreamFilter", true,
		"headers", headers)

	// Pass through without modification
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extprocv3.HeadersResponse{},
		},
		ModeOverride: &extprocv3http.ProcessingMode{
			ResponseBodyMode: extprocv3http.ProcessingMode_BUFFERED,
		},
	}, nil
}

func (r *realtimeClientSecretsProcessorUpstreamFilter) ProcessResponseBody(_ context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	// Log the raw response body from Gemini for debugging
	slog.Info("[Gemini Realtime] Received response from backend",
		"processor", "realtime-client-secrets",
		"isUpstreamFilter", true,
		"raw_response_body", string(body.Body))

	// Translate the response
	headerMutation, bodyMutation, err := r.translator.ResponseBody(body.Body)
	if err != nil {
		slog.Error("[Gemini Realtime] Failed to translate response",
			"processor", "realtime-client-secrets",
			"isUpstreamFilter", true,
			"error", err,
			"raw_body", string(body.Body))
		return nil, fmt.Errorf("failed to translate response: %w", err)
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
