// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func TestEmbedding_Schema(t *testing.T) {
	t.Run("unsupported", func(t *testing.T) {
		cfg := &processorConfig{schema: filterapi.VersionedAPISchema{Name: "Foo", Version: "v123"}}
		_, err := EmbeddingProcessorFactory(nil)(cfg, nil, slog.Default(), false)
		require.ErrorContains(t, err, "unsupported API schema: Foo")
	})
	t.Run("supported openai / on route", func(t *testing.T) {
		cfg := &processorConfig{schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v123"}}
		routeFilter, err := EmbeddingProcessorFactory(nil)(cfg, nil, slog.Default(), false)
		require.NoError(t, err)
		require.NotNil(t, routeFilter)
		require.IsType(t, &embeddingProcessorRouterFilter{}, routeFilter)
	})
	t.Run("supported openai / on upstream", func(t *testing.T) {
		cfg := &processorConfig{schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v123"}}
		routeFilter, err := EmbeddingProcessorFactory(nil)(cfg, nil, slog.Default(), true)
		require.NoError(t, err)
		require.NotNil(t, routeFilter)
		require.IsType(t, &embeddingProcessorUpstreamFilter{}, routeFilter)
	})
}

func TestEmbeddingProcessorUpstreamFilter_selectTranslator(t *testing.T) {
	e := &embeddingProcessorUpstreamFilter{}
	t.Run("unsupported", func(t *testing.T) {
		err := e.selectTranslator(filterapi.VersionedAPISchema{Name: "foo"})
		require.ErrorContains(t, err, "unsupported API schema: backend=")
	})
	t.Run("supported openai", func(t *testing.T) {
		err := e.selectTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI})
		require.NoError(t, err)
		require.NotNil(t, e.translator)
	})
	t.Run("supported aws bedrock", func(t *testing.T) {
		err := e.selectTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock})
		require.NoError(t, err)
		require.NotNil(t, e.translator)
	})
	t.Run("supported azure openai", func(t *testing.T) {
		err := e.selectTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI})
		require.NoError(t, err)
		require.NotNil(t, e.translator)
	})
}

func Test_embeddingProcessorRouterFilter_ProcessRequestBody(t *testing.T) {
	t.Run("body parser error", func(t *testing.T) {
		p := &embeddingProcessorRouterFilter{}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: []byte("nonjson")})
		require.ErrorContains(t, err, "invalid character 'o' in literal null")
	})

	t.Run("router error", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rt := mockRouter{t: t, expHeaders: headers, retErr: errors.New("some error")}
		p := &embeddingProcessorRouterFilter{
			config:         &processorConfig{router: rt},
			requestHeaders: headers,
		}
		someBody := []byte(`{"model": "text-embedding-ada-002", "input": "hello world"}`)
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: someBody})
		require.ErrorContains(t, err, "some error")
	})

	t.Run("ok", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rt := mockRouter{t: t, expHeaders: headers, retRouteName: "some-route"}
		const modelKey = "x-ai-gateway-model-key"
		const modelRouteKey = "x-ai-gateway-route-key"
		p := &embeddingProcessorRouterFilter{
			config:         &processorConfig{router: rt, modelNameHeaderKey: modelKey, selectedRouteHeaderKey: modelRouteKey},
			requestHeaders: headers,
			logger:         slog.Default(),
		}
		someBody := []byte(`{"model": "text-embedding-ada-002", "input": "hello world"}`)
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: someBody})
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Check that the model and route headers are set
		reqBody := resp.GetRequestBody()
		require.NotNil(t, reqBody)
		headerMutation := reqBody.GetResponse().GetHeaderMutation()
		require.NotNil(t, headerMutation)
		require.Len(t, headerMutation.SetHeaders, 2)

		// Verify headers
		var modelHeader, routeHeader *corev3.HeaderValueOption
		for _, h := range headerMutation.SetHeaders {
			if h.Header.Key == modelKey {
				modelHeader = h
			} else if h.Header.Key == modelRouteKey {
				routeHeader = h
			}
		}
		require.NotNil(t, modelHeader)
		require.NotNil(t, routeHeader)
		require.Equal(t, "text-embedding-ada-002", string(modelHeader.Header.RawValue))
		require.Equal(t, "some-route", string(routeHeader.Header.RawValue))

		// Check that the original request body is stored
		require.NotNil(t, p.originalRequestBody)
		require.Equal(t, "text-embedding-ada-002", p.originalRequestBody.Model)
		require.Equal(t, "hello world", p.originalRequestBody.Input.Value)
	})
}

func Test_parseOpenAIEmbeddingBody(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		_, _, err := parseOpenAIEmbeddingBody(&extprocv3.HttpBody{Body: []byte("invalid")})
		require.Error(t, err)
	})

	t.Run("valid single input", func(t *testing.T) {
		body := []byte(`{"model": "text-embedding-ada-002", "input": "hello world"}`)
		model, req, err := parseOpenAIEmbeddingBody(&extprocv3.HttpBody{Body: body})
		require.NoError(t, err)
		require.Equal(t, "text-embedding-ada-002", model)
		require.Equal(t, "text-embedding-ada-002", req.Model)
		require.Equal(t, "hello world", req.Input.Value)
	})

	t.Run("valid array input", func(t *testing.T) {
		body := []byte(`{"model": "text-embedding-ada-002", "input": ["hello", "world"]}`)
		model, req, err := parseOpenAIEmbeddingBody(&extprocv3.HttpBody{Body: body})
		require.NoError(t, err)
		require.Equal(t, "text-embedding-ada-002", model)
		require.Equal(t, "text-embedding-ada-002", req.Model)
		require.Equal(t, []string{"hello", "world"}, req.Input.Value)
	})

	t.Run("with optional fields", func(t *testing.T) {
		body := []byte(`{
			"model": "text-embedding-3-small",
			"input": "hello world",
			"encoding_format": "float",
			"dimensions": 512,
			"user": "test-user"
		}`)
		model, req, err := parseOpenAIEmbeddingBody(&extprocv3.HttpBody{Body: body})
		require.NoError(t, err)
		require.Equal(t, "text-embedding-3-small", model)
		require.Equal(t, "text-embedding-3-small", req.Model)
		require.Equal(t, "hello world", req.Input.Value)
		require.Equal(t, openai.EmbeddingEncodingFormatFloat, req.EncodingFormat)
		require.NotNil(t, req.Dimensions)
		require.Equal(t, 512, *req.Dimensions)
		require.Equal(t, "test-user", req.User)
	})
}

// mockEmbeddingMetrics implements [x.EmbeddingMetrics] for testing.
type mockEmbeddingMetrics struct {
	requestSuccessCount int
	requestErrorCount   int
	tokenUsageCount     int
	model               string
	backend             string
}

// StartRequest implements [x.EmbeddingMetrics].
func (m *mockEmbeddingMetrics) StartRequest(_ map[string]string) {}

// SetModel implements [x.EmbeddingMetrics].
func (m *mockEmbeddingMetrics) SetModel(model string) { m.model = model }

// SetBackend implements [x.EmbeddingMetrics].
func (m *mockEmbeddingMetrics) SetBackend(backend *filterapi.Backend) { m.backend = backend.Name }

// RecordTokenUsage implements [x.EmbeddingMetrics].
func (m *mockEmbeddingMetrics) RecordTokenUsage(_ context.Context, _, _ uint32, _ ...attribute.KeyValue) {
	m.tokenUsageCount++
}

// RecordRequestCompletion implements [x.EmbeddingMetrics].
func (m *mockEmbeddingMetrics) RecordRequestCompletion(_ context.Context, success bool, _ ...attribute.KeyValue) {
	if success {
		m.requestSuccessCount++
	} else {
		m.requestErrorCount++
	}
}

// RequireSelectedModel asserts the model and backend set on the metrics.
func (m *mockEmbeddingMetrics) RequireSelectedModel(t *testing.T, model string) {
	require.Equal(t, model, m.model)
}

// mockEmbeddingTranslator implements [translator.OpenAIEmbeddingTranslator] for testing.
type mockEmbeddingTranslator struct {
	t                 *testing.T
	expRequestBody    *openai.EmbeddingRequest
	retHeaderMutation *extprocv3.HeaderMutation
	retBodyMutation   *extprocv3.BodyMutation
	retTokenUsage     *openai.EmbeddingUsage
	retErr            error
}

// RequestBody implements [translator.OpenAIEmbeddingTranslator].
func (m mockEmbeddingTranslator) RequestBody(_ []byte, body *openai.EmbeddingRequest, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	if m.expRequestBody != nil {
		require.Equal(m.t, m.expRequestBody.Model, body.Model)
		require.Equal(m.t, m.expRequestBody.Input.Value, body.Input.Value)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

// ResponseHeaders implements [translator.OpenAIEmbeddingTranslator].
func (m mockEmbeddingTranslator) ResponseHeaders(_ map[string]string) (
	headerMutation *extprocv3.HeaderMutation, err error,
) {
	return m.retHeaderMutation, m.retErr
}

// ResponseBody implements [translator.OpenAIEmbeddingTranslator].
func (m mockEmbeddingTranslator) ResponseBody(_ map[string]string, _ io.Reader, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage *openai.EmbeddingUsage, err error,
) {
	return m.retHeaderMutation, m.retBodyMutation, m.retTokenUsage, m.retErr
}

func Test_embeddingProcessorUpstreamFilter_ProcessRequestHeaders(t *testing.T) {
	t.Run("translator error", func(t *testing.T) {
		mt := mockEmbeddingTranslator{t: t, retErr: errors.New("translator error")}
		mm := &mockEmbeddingMetrics{}
		p := &embeddingProcessorUpstreamFilter{
			config:                 &processorConfig{modelNameHeaderKey: "x-model"},
			requestHeaders:         map[string]string{"x-model": "test-model"},
			logger:                 slog.Default(),
			metrics:                mm,
			translator:             mt,
			originalRequestBodyRaw: []byte(`{"model": "test-model", "input": "test"}`),
			originalRequestBody:    &openai.EmbeddingRequest{Model: "test-model"},
		}
		_, err := p.ProcessRequestHeaders(context.Background(), &corev3.HeaderMap{})
		require.ErrorContains(t, err, "translator error")
		require.Equal(t, 1, mm.requestErrorCount)
	})

	t.Run("success", func(t *testing.T) {
		headerMut := &extprocv3.HeaderMutation{
			SetHeaders: []*corev3.HeaderValueOption{
				{Header: &corev3.HeaderValue{Key: "test-header", RawValue: []byte("test-value")}},
			},
		}
		bodyMut := &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: []byte("test-body")},
		}
		mt := mockEmbeddingTranslator{t: t, retHeaderMutation: headerMut, retBodyMutation: bodyMut}
		mm := &mockEmbeddingMetrics{}
		p := &embeddingProcessorUpstreamFilter{
			config:                 &processorConfig{modelNameHeaderKey: "x-model"},
			requestHeaders:         map[string]string{"x-model": "test-model"},
			logger:                 slog.Default(),
			metrics:                mm,
			translator:             mt,
			originalRequestBodyRaw: []byte(`{"model": "test-model", "input": "test"}`),
			originalRequestBody:    &openai.EmbeddingRequest{Model: "test-model"},
		}
		resp, err := p.ProcessRequestHeaders(context.Background(), &corev3.HeaderMap{})
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Check response structure
		reqHeaders := resp.GetRequestHeaders()
		require.NotNil(t, reqHeaders)
		require.NotNil(t, reqHeaders.GetResponse())
		require.Equal(t, headerMut, reqHeaders.GetResponse().GetHeaderMutation())
		require.Equal(t, bodyMut, reqHeaders.GetResponse().GetBodyMutation())
	})
}

func Test_embeddingProcessorUpstreamFilter_ProcessResponseBody(t *testing.T) {
	t.Run("translator error", func(t *testing.T) {
		mt := mockEmbeddingTranslator{t: t, retErr: errors.New("translator error")}
		mm := &mockEmbeddingMetrics{}
		p := &embeddingProcessorUpstreamFilter{
			logger:          slog.Default(),
			metrics:         mm,
			translator:      mt,
			responseHeaders: map[string]string{},
		}
		_, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{Body: []byte("test")})
		require.ErrorContains(t, err, "translator error")
		require.Equal(t, 1, mm.requestErrorCount)
	})

	t.Run("success with token usage", func(t *testing.T) {
		tokenUsage := &openai.EmbeddingUsage{
			PromptTokens: 10,
			TotalTokens:  10,
		}
		bodyMut := &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: []byte("response-body")},
		}
		mt := mockEmbeddingTranslator{t: t, retBodyMutation: bodyMut, retTokenUsage: tokenUsage}
		mm := &mockEmbeddingMetrics{}
		p := &embeddingProcessorUpstreamFilter{
			logger:          slog.Default(),
			metrics:         mm,
			translator:      mt,
			responseHeaders: map[string]string{},
		}
		resp, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{Body: []byte("test")})
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Check response structure
		respBody := resp.GetResponseBody()
		require.NotNil(t, respBody)

		// Check body mutation in response
		require.NotNil(t, respBody.GetResponse())
		require.Equal(t, bodyMut, respBody.GetResponse().GetBodyMutation())

		// Check metrics
		require.Equal(t, 1, mm.requestSuccessCount)
		require.Equal(t, 1, mm.tokenUsageCount)
	})
}

func Test_embeddingProcessorUpstreamFilter_SetBackend(t *testing.T) {
	t.Run("wrong router processor type", func(t *testing.T) {
		p := &embeddingProcessorUpstreamFilter{
			logger:  slog.Default(),
			metrics: &mockEmbeddingMetrics{},
		}
		backend := &filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}}
		require.Panics(t, func() {
			_ = p.SetBackend(context.Background(), backend, nil, &chatCompletionProcessorRouterFilter{})
		})
	})

	t.Run("translator selection error", func(t *testing.T) {
		mm := &mockEmbeddingMetrics{}
		p := &embeddingProcessorUpstreamFilter{
			logger:  slog.Default(),
			metrics: mm,
		}
		rp := &embeddingProcessorRouterFilter{}
		backend := &filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: "unsupported"}}
		err := p.SetBackend(context.Background(), backend, nil, rp)
		require.ErrorContains(t, err, "unsupported API schema")
		require.Equal(t, 1, mm.requestErrorCount)
	})

	t.Run("success", func(t *testing.T) {
		mm := &mockEmbeddingMetrics{}
		p := &embeddingProcessorUpstreamFilter{
			logger:  slog.Default(),
			metrics: mm,
		}
		originalBody := &openai.EmbeddingRequest{Model: "test-model"}
		originalBodyRaw := []byte(`{"model": "test-model", "input": "test"}`)
		rp := &embeddingProcessorRouterFilter{
			originalRequestBody:    originalBody,
			originalRequestBodyRaw: originalBodyRaw,
		}
		backend := &filterapi.Backend{
			Name:              "test-backend",
			Schema:            filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			ModelNameOverride: "override-model",
		}
		err := p.SetBackend(context.Background(), backend, nil, rp)
		require.NoError(t, err)

		// Check that values are set correctly
		require.Equal(t, "override-model", p.modelNameOverride)
		require.Equal(t, originalBody, p.originalRequestBody)
		require.Equal(t, originalBodyRaw, p.originalRequestBodyRaw)
		require.NotNil(t, p.translator)
		require.Equal(t, p, rp.upstreamFilter)
		require.Equal(t, 1, rp.upstreamFilterCount)
	})
}
