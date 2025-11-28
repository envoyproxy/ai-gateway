// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

type mockAudioTranscriptionTranslator struct {
	t                   *testing.T
	expHeaders          map[string]string
	expRequestBody      *openai.AudioTranscriptionRequest
	expResponseBody     []byte
	retHeaderMutation   *extprocv3.HeaderMutation
	retBodyMutation     *extprocv3.BodyMutation
	retUsedToken        metrics.TokenUsage
	retResponseModel    internalapi.ResponseModel
	retErr              error
	responseErrorCalled bool
	useGeminiDirectPath bool
	contentType         string
}

func (m *mockAudioTranscriptionTranslator) RequestBody(_ []byte, body *openai.AudioTranscriptionRequest, _ bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	if m.expRequestBody != nil {
		require.Equal(m.t, m.expRequestBody, body)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

func (m *mockAudioTranscriptionTranslator) ResponseHeaders(headers map[string]string) (*extprocv3.HeaderMutation, error) {
	if m.expHeaders != nil {
		require.Equal(m.t, m.expHeaders, headers)
	}
	return m.retHeaderMutation, m.retErr
}

func (m *mockAudioTranscriptionTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, metrics.TokenUsage, internalapi.ResponseModel, error) {
	if m.expResponseBody != nil {
		buf, err := io.ReadAll(body)
		require.NoError(m.t, err)
		require.Equal(m.t, m.expResponseBody, buf)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retUsedToken, m.retResponseModel, m.retErr
}

func (m *mockAudioTranscriptionTranslator) ResponseError(_ map[string]string, body io.Reader) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	m.responseErrorCalled = true
	if m.expResponseBody != nil {
		buf, err := io.ReadAll(body)
		require.NoError(m.t, err)
		require.Equal(m.t, m.expResponseBody, buf)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

func (m *mockAudioTranscriptionTranslator) SetUseGeminiDirectPath(use bool) {
	m.useGeminiDirectPath = use
}

func (m *mockAudioTranscriptionTranslator) SetContentType(contentType string) {
	m.contentType = contentType
}

var _ translator.AudioTranscriptionTranslator = &mockAudioTranscriptionTranslator{}

func TestAudioTranscriptionProcessorFactory(t *testing.T) {
	t.Run("router filter", func(t *testing.T) {
		factory := AudioTranscriptionProcessorFactory(&mockMetricsFactory{})
		processor, err := factory(&filterapi.RuntimeConfig{}, map[string]string{}, slog.Default(), tracing.NoopTracing{}, false)
		require.NoError(t, err)
		require.NotNil(t, processor)
		require.IsType(t, &audioTranscriptionProcessorRouterFilter{}, processor)
	})

	t.Run("upstream filter", func(t *testing.T) {
		factory := AudioTranscriptionProcessorFactory(&mockMetricsFactory{})
		processor, err := factory(&filterapi.RuntimeConfig{}, map[string]string{}, slog.Default(), tracing.NoopTracing{}, true)
		require.NoError(t, err)
		require.NotNil(t, processor)
		require.IsType(t, &audioTranscriptionProcessorUpstreamFilter{}, processor)
	})
}

func TestAudioTranscriptionProcessorRouterFilter_ProcessRequestBody(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		p := &audioTranscriptionProcessorRouterFilter{
			requestHeaders: map[string]string{
				":path":        "/v1/audio/transcriptions",
				"content-type": "application/json",
			},
		}
		_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: []byte("invalid")})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse request body")
	})

	t.Run("success json", func(t *testing.T) {
		req := openai.AudioTranscriptionRequest{
			Model: "whisper-1",
		}
		reqBody, _ := json.Marshal(req)

		p := &audioTranscriptionProcessorRouterFilter{
			config: &filterapi.RuntimeConfig{},
			requestHeaders: map[string]string{
				":path":        "/v1/audio/transcriptions",
				"content-type": "application/json",
			},
			logger: slog.Default(),
		}

		resp, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: reqBody})
		require.NoError(t, err)
		require.NotNil(t, resp)

		rb := resp.GetRequestBody()
		require.NotNil(t, rb)
		require.True(t, rb.Response.ClearRouteCache)

		headers := rb.Response.HeaderMutation.SetHeaders
		require.Len(t, headers, 2)
		require.Equal(t, internalapi.ModelNameHeaderKeyDefault, headers[0].Header.Key)
		require.Equal(t, "whisper-1", string(headers[0].Header.RawValue))
	})

	t.Run("success multipart", func(t *testing.T) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		_ = writer.WriteField("model", "whisper-1")
		_ = writer.WriteField("language", "en")

		part, _ := writer.CreateFormFile("file", "test.mp3")
		_, _ = part.Write([]byte("audio data"))

		writer.Close()

		p := &audioTranscriptionProcessorRouterFilter{
			config: &filterapi.RuntimeConfig{},
			requestHeaders: map[string]string{
				":path":        "/v1/audio/transcriptions",
				"content-type": writer.FormDataContentType(),
			},
			logger: slog.Default(),
		}

		resp, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: buf.Bytes()})
		require.NoError(t, err)
		require.NotNil(t, resp)

		rb := resp.GetRequestBody()
		require.NotNil(t, rb)
		require.True(t, rb.Response.ClearRouteCache)

		headers := rb.Response.HeaderMutation.SetHeaders
		require.Len(t, headers, 2)
		require.Equal(t, internalapi.ModelNameHeaderKeyDefault, headers[0].Header.Key)
		require.Equal(t, "whisper-1", string(headers[0].Header.RawValue))
	})
}

func TestAudioTranscriptionProcessorRouterFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("with upstream filter", func(t *testing.T) {
		headerMap := &corev3.HeaderMap{}
		mockUpstream := &mockProcessor{
			t:                     t,
			expHeaderMap:          headerMap,
			retProcessingResponse: &extprocv3.ProcessingResponse{},
		}
		p := &audioTranscriptionProcessorRouterFilter{
			upstreamFilter: mockUpstream,
		}

		resp, err := p.ProcessResponseHeaders(context.Background(), headerMap)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("without upstream filter", func(t *testing.T) {
		p := &audioTranscriptionProcessorRouterFilter{}
		resp, err := p.ProcessResponseHeaders(context.Background(), &corev3.HeaderMap{})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestAudioTranscriptionProcessorRouterFilter_ProcessResponseBody(t *testing.T) {
	t.Run("with upstream filter", func(t *testing.T) {
		httpBody := &extprocv3.HttpBody{}
		mockUpstream := &mockProcessor{
			t:                     t,
			expBody:               httpBody,
			retProcessingResponse: &extprocv3.ProcessingResponse{},
		}
		p := &audioTranscriptionProcessorRouterFilter{
			upstreamFilter: mockUpstream,
		}

		resp, err := p.ProcessResponseBody(context.Background(), httpBody)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("without upstream filter", func(t *testing.T) {
		p := &audioTranscriptionProcessorRouterFilter{}
		resp, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestAudioTranscriptionProcessorUpstreamFilter_SelectTranslator(t *testing.T) {
	t.Run("openai", func(t *testing.T) {
		p := &audioTranscriptionProcessorUpstreamFilter{}
		err := p.selectTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v1"})
		require.NoError(t, err)
		require.NotNil(t, p.translator)
	})

	t.Run("gcp vertex ai", func(t *testing.T) {
		p := &audioTranscriptionProcessorUpstreamFilter{}
		err := p.selectTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaGCPVertexAI})
		require.NoError(t, err)
		require.NotNil(t, p.translator)
	})

	t.Run("unsupported", func(t *testing.T) {
		p := &audioTranscriptionProcessorUpstreamFilter{}
		err := p.selectTranslator(filterapi.VersionedAPISchema{Name: "unsupported"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported API schema")
	})
}

func TestAudioTranscriptionProcessorUpstreamFilter_ProcessRequestHeaders(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockTranslator := &mockAudioTranscriptionTranslator{
			t:                 t,
			retHeaderMutation: &extprocv3.HeaderMutation{},
			retBodyMutation:   &extprocv3.BodyMutation{Mutation: &extprocv3.BodyMutation_Body{Body: []byte("test")}},
		}

		req := openai.AudioTranscriptionRequest{
			Model: "whisper-1",
		}
		reqBody, _ := json.Marshal(req)

		p := &audioTranscriptionProcessorUpstreamFilter{
			logger:                 slog.Default(),
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         map[string]string{},
			originalRequestBody:    &req,
			originalRequestBodyRaw: reqBody,
			translator:             mockTranslator,
			metrics:                &mockMetrics{},
		}

		resp, err := p.ProcessRequestHeaders(context.Background(), &corev3.HeaderMap{})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("translator error", func(t *testing.T) {
		mockTranslator := &mockAudioTranscriptionTranslator{
			t:      t,
			retErr: errors.New("translator error"),
		}

		req := openai.AudioTranscriptionRequest{Model: "whisper-1"}
		reqBody, _ := json.Marshal(req)

		p := &audioTranscriptionProcessorUpstreamFilter{
			logger:                 slog.Default(),
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         map[string]string{},
			originalRequestBody:    &req,
			originalRequestBodyRaw: reqBody,
			translator:             mockTranslator,
			metrics:                &mockMetrics{},
		}

		_, err := p.ProcessRequestHeaders(context.Background(), &corev3.HeaderMap{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to transform request")
	})

	t.Run("with auth handler", func(t *testing.T) {
		mockTranslator := &mockAudioTranscriptionTranslator{
			t:                 t,
			retHeaderMutation: &extprocv3.HeaderMutation{},
			retBodyMutation:   &extprocv3.BodyMutation{Mutation: &extprocv3.BodyMutation_Body{Body: []byte("test")}},
		}

		req := openai.AudioTranscriptionRequest{Model: "whisper-1"}
		reqBody, _ := json.Marshal(req)

		p := &audioTranscriptionProcessorUpstreamFilter{
			logger:                 slog.Default(),
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         map[string]string{},
			originalRequestBody:    &req,
			originalRequestBodyRaw: reqBody,
			translator:             mockTranslator,
			handler:                &mockBackendAuthHandler{},
			metrics:                &mockMetrics{},
		}

		resp, err := p.ProcessRequestHeaders(context.Background(), &corev3.HeaderMap{})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("auth handler error", func(t *testing.T) {
		mockTranslator := &mockAudioTranscriptionTranslator{
			t:                 t,
			retHeaderMutation: &extprocv3.HeaderMutation{},
			retBodyMutation:   &extprocv3.BodyMutation{Mutation: &extprocv3.BodyMutation_Body{Body: []byte("test")}},
		}

		req := openai.AudioTranscriptionRequest{Model: "whisper-1"}
		reqBody, _ := json.Marshal(req)

		p := &audioTranscriptionProcessorUpstreamFilter{
			logger:                 slog.Default(),
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         map[string]string{},
			originalRequestBody:    &req,
			originalRequestBodyRaw: reqBody,
			translator:             mockTranslator,
			handler:                &mockBackendAuthHandlerError{},
			metrics:                &mockMetrics{},
		}

		_, err := p.ProcessRequestHeaders(context.Background(), &corev3.HeaderMap{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to do auth request")
	})

	t.Run("with content type setter", func(t *testing.T) {
		mockTranslator := &mockAudioTranscriptionTranslator{
			t:                 t,
			retHeaderMutation: &extprocv3.HeaderMutation{},
			retBodyMutation:   &extprocv3.BodyMutation{Mutation: &extprocv3.BodyMutation_Body{Body: []byte("test")}},
		}

		req := openai.AudioTranscriptionRequest{Model: "whisper-1"}
		reqBody, _ := json.Marshal(req)

		p := &audioTranscriptionProcessorUpstreamFilter{
			logger:                 slog.Default(),
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         map[string]string{"content-type": "multipart/form-data"},
			originalRequestBody:    &req,
			originalRequestBodyRaw: reqBody,
			translator:             mockTranslator,
			metrics:                &mockMetrics{},
		}

		resp, err := p.ProcessRequestHeaders(context.Background(), &corev3.HeaderMap{})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, "multipart/form-data", mockTranslator.contentType)
	})
}

func TestAudioTranscriptionProcessorUpstreamFilter_ProcessRequestBody(t *testing.T) {
	p := &audioTranscriptionProcessorUpstreamFilter{}
	require.Panics(t, func() {
		_, _ = p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{})
	})
}

func TestAudioTranscriptionProcessorUpstreamFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockTranslator := &mockAudioTranscriptionTranslator{
			t:                 t,
			retHeaderMutation: &extprocv3.HeaderMutation{},
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			translator: mockTranslator,
			metrics:    &mockMetrics{},
		}

		headers := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{
				{Key: "content-type", RawValue: []byte("application/json")},
			},
		}

		resp, err := p.ProcessResponseHeaders(context.Background(), headers)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("with content-encoding", func(t *testing.T) {
		mockTranslator := &mockAudioTranscriptionTranslator{
			t:                 t,
			retHeaderMutation: &extprocv3.HeaderMutation{},
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			translator: mockTranslator,
			metrics:    &mockMetrics{},
		}

		headers := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{
				{Key: "content-encoding", RawValue: []byte("gzip")},
			},
		}

		resp, err := p.ProcessResponseHeaders(context.Background(), headers)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, "gzip", p.responseEncoding)
	})

	t.Run("translator error", func(t *testing.T) {
		mockTranslator := &mockAudioTranscriptionTranslator{
			t:      t,
			retErr: errors.New("translator error"),
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			translator: mockTranslator,
			metrics:    &mockMetrics{},
		}

		_, err := p.ProcessResponseHeaders(context.Background(), &corev3.HeaderMap{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to transform response headers")
	})
}

func TestAudioTranscriptionProcessorUpstreamFilter_ProcessResponseBody(t *testing.T) {
	t.Run("success end of stream", func(t *testing.T) {
		var tokenUsage metrics.TokenUsage
		tokenUsage.SetInputTokens(100)
		tokenUsage.SetTotalTokens(100)
		mockTranslator := &mockAudioTranscriptionTranslator{
			t:                 t,
			retHeaderMutation: &extprocv3.HeaderMutation{},
			retBodyMutation:   &extprocv3.BodyMutation{},
			retUsedToken:      tokenUsage,
			retResponseModel:  "whisper-1",
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			translator:      mockTranslator,
			requestHeaders:  map[string]string{},
			responseHeaders: map[string]string{":status": "200"},
			config:          &filterapi.RuntimeConfig{},
			metrics:         &mockMetrics{},
		}

		resp, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{
			Body:        []byte(`{"text": "transcribed text"}`),
			EndOfStream: true,
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("not end of stream", func(t *testing.T) {
		var tokenUsage metrics.TokenUsage
		tokenUsage.SetInputTokens(50)
		tokenUsage.SetTotalTokens(50)
		mockTranslator := &mockAudioTranscriptionTranslator{
			t:                 t,
			retHeaderMutation: &extprocv3.HeaderMutation{},
			retBodyMutation:   &extprocv3.BodyMutation{},
			retUsedToken:      tokenUsage,
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			translator:      mockTranslator,
			requestHeaders:  map[string]string{},
			responseHeaders: map[string]string{":status": "200"},
			config:          &filterapi.RuntimeConfig{},
			metrics:         &mockMetrics{},
		}

		resp, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{
			Body:        []byte(`{"text": "partial"}`),
			EndOfStream: false,
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("error response", func(t *testing.T) {
		mockTranslator := &mockAudioTranscriptionTranslator{
			t:                 t,
			retHeaderMutation: &extprocv3.HeaderMutation{},
			retBodyMutation:   &extprocv3.BodyMutation{},
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			translator:      mockTranslator,
			requestHeaders:  map[string]string{},
			responseHeaders: map[string]string{":status": "400"},
			config:          &filterapi.RuntimeConfig{},
			metrics:         &mockMetrics{},
		}

		resp, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{
			Body:        []byte("error"),
			EndOfStream: true,
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, mockTranslator.responseErrorCalled)
	})

	t.Run("translator error", func(t *testing.T) {
		mockTranslator := &mockAudioTranscriptionTranslator{
			t:      t,
			retErr: errors.New("translator error"),
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			translator:      mockTranslator,
			requestHeaders:  map[string]string{},
			responseHeaders: map[string]string{":status": "200"},
			config:          &filterapi.RuntimeConfig{},
			metrics:         &mockMetrics{},
		}

		_, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{
			Body:        []byte(`{"text": "test"}`),
			EndOfStream: true,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to transform response")
	})

	t.Run("token override", func(t *testing.T) {
		// First chunk returns empty token usage (not end of stream)
		var emptyTokenUsage metrics.TokenUsage

		// Final chunk returns the cumulative token usage
		var finalTokenUsage metrics.TokenUsage
		finalTokenUsage.SetInputTokens(100)
		finalTokenUsage.SetTotalTokens(100)

		mockTranslator := &mockAudioTranscriptionTranslator{
			t:                 t,
			retHeaderMutation: &extprocv3.HeaderMutation{},
			retBodyMutation:   &extprocv3.BodyMutation{},
			retUsedToken:      emptyTokenUsage,
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			translator:      mockTranslator,
			requestHeaders:  map[string]string{},
			responseHeaders: map[string]string{":status": "200"},
			config:          &filterapi.RuntimeConfig{},
			metrics:         &mockMetrics{},
		}

		// First call (not end of stream) - translator returns empty token usage
		_, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{
			Body:        []byte(`{"text": "test1"}`),
			EndOfStream: false,
		})
		require.NoError(t, err)
		// Costs should be empty since translator returned empty token usage
		_, inputSet := p.costs.InputTokens()
		_, totalSet := p.costs.TotalTokens()
		require.False(t, inputSet)
		require.False(t, totalSet)

		// Update mock to return final token usage for end of stream
		mockTranslator.retUsedToken = finalTokenUsage

		// Second call (end of stream) - translator returns cumulative token usage
		_, err = p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{
			Body:        []byte(`{"text": "test2"}`),
			EndOfStream: true,
		})
		require.NoError(t, err)
		inputTokens, _ := p.costs.InputTokens()
		totalTokens, _ := p.costs.TotalTokens()
		require.Equal(t, uint32(100), inputTokens)
		require.Equal(t, uint32(100), totalTokens)
	})
}

func TestAudioTranscriptionProcessorUpstreamFilter_SetBackend(t *testing.T) {
	t.Run("openai backend", func(t *testing.T) {
		routeProcessor := &audioTranscriptionProcessorRouterFilter{
			originalRequestBody:    &openai.AudioTranscriptionRequest{Model: "whisper-1"},
			originalRequestBodyRaw: []byte("test"),
		}

		backend := &filterapi.Backend{
			Name:   "test-backend",
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v1"},
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: map[string]string{},
			metrics:        &mockMetrics{},
		}

		err := p.SetBackend(context.Background(), backend, nil, routeProcessor)
		require.NoError(t, err)
		require.NotNil(t, p.translator)
		require.Equal(t, "test-backend", p.backendName)
	})

	t.Run("gcp vertex ai backend", func(t *testing.T) {
		routeProcessor := &audioTranscriptionProcessorRouterFilter{
			originalRequestBody:    &openai.AudioTranscriptionRequest{Model: "whisper-1"},
			originalRequestBodyRaw: []byte("test"),
		}

		backend := &filterapi.Backend{
			Name:   "gcp-backend",
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaGCPVertexAI},
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: map[string]string{},
			metrics:        &mockMetrics{},
		}

		err := p.SetBackend(context.Background(), backend, nil, routeProcessor)
		require.NoError(t, err)
		require.NotNil(t, p.translator)
	})

	t.Run("model name override", func(t *testing.T) {
		routeProcessor := &audioTranscriptionProcessorRouterFilter{
			originalRequestBody:    &openai.AudioTranscriptionRequest{Model: "whisper-1"},
			originalRequestBodyRaw: []byte("test"),
			requestHeaders:         map[string]string{},
		}

		backend := &filterapi.Backend{
			Name:              "test-backend",
			Schema:            filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v1"},
			ModelNameOverride: "override-model",
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: map[string]string{},
			metrics:        &mockMetrics{},
		}

		err := p.SetBackend(context.Background(), backend, nil, routeProcessor)
		require.NoError(t, err)
		require.Equal(t, internalapi.ModelNameOverride("override-model"), p.modelNameOverride)
	})

	t.Run("unsupported schema", func(t *testing.T) {
		routeProcessor := &audioTranscriptionProcessorRouterFilter{
			originalRequestBody:    &openai.AudioTranscriptionRequest{Model: "whisper-1"},
			originalRequestBodyRaw: []byte("test"),
		}

		backend := &filterapi.Backend{
			Name:   "unsupported-backend",
			Schema: filterapi.VersionedAPISchema{Name: "unsupported"},
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			config:  &filterapi.RuntimeConfig{},
			metrics: &mockMetrics{},
		}

		err := p.SetBackend(context.Background(), backend, nil, routeProcessor)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to select translator")
	})

	t.Run("panic on wrong processor type", func(t *testing.T) {
		backend := &filterapi.Backend{
			Name:   "test-backend",
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			config:  &filterapi.RuntimeConfig{},
			metrics: &mockMetrics{},
		}

		require.Panics(t, func() {
			_ = p.SetBackend(context.Background(), backend, nil, &mockProcessor{})
		})
	})

	t.Run("retry scenario", func(t *testing.T) {
		routeProcessor := &audioTranscriptionProcessorRouterFilter{
			originalRequestBody:    &openai.AudioTranscriptionRequest{Model: "whisper-1"},
			originalRequestBodyRaw: []byte("test"),
			upstreamFilterCount:    1,
		}

		backend := &filterapi.Backend{
			Name:   "test-backend",
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v1"},
		}

		p := &audioTranscriptionProcessorUpstreamFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: map[string]string{},
			metrics:        &mockMetrics{},
		}

		err := p.SetBackend(context.Background(), backend, nil, routeProcessor)
		require.NoError(t, err)
		require.True(t, p.onRetry)
		require.Equal(t, 2, routeProcessor.upstreamFilterCount)
	})
}

func TestParseAudioTranscriptionBody(t *testing.T) {
	t.Run("valid json body", func(t *testing.T) {
		req := openai.AudioTranscriptionRequest{
			Model:    "whisper-1",
			Language: "en",
			Prompt:   "test prompt",
		}
		body, _ := json.Marshal(req)

		modelName, parsedReq, err := parseAudioTranscriptionBody(&extprocv3.HttpBody{Body: body}, "application/json")
		require.NoError(t, err)
		require.Equal(t, "whisper-1", modelName)
		require.Equal(t, "en", parsedReq.Language)
		require.Equal(t, "test prompt", parsedReq.Prompt)
	})

	t.Run("invalid json", func(t *testing.T) {
		_, _, err := parseAudioTranscriptionBody(&extprocv3.HttpBody{Body: []byte("invalid")}, "application/json")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal body")
	})

	t.Run("multipart with all fields", func(t *testing.T) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		_ = writer.WriteField("model", "whisper-1")
		_ = writer.WriteField("language", "en")
		_ = writer.WriteField("prompt", "test prompt")
		_ = writer.WriteField("response_format", "json")
		_ = writer.WriteField("temperature", "0.5")

		part, _ := writer.CreateFormFile("file", "test.mp3")
		_, _ = part.Write([]byte("audio data"))

		writer.Close()

		modelName, parsedReq, err := parseAudioTranscriptionBody(&extprocv3.HttpBody{Body: buf.Bytes()}, writer.FormDataContentType())
		require.NoError(t, err)
		require.Equal(t, "whisper-1", modelName)
		require.Equal(t, "en", parsedReq.Language)
		require.Equal(t, "test prompt", parsedReq.Prompt)
		require.Equal(t, "json", parsedReq.ResponseFormat)
		require.NotNil(t, parsedReq.Temperature)
		require.Equal(t, 0.5, *parsedReq.Temperature)
	})

	t.Run("multipart without model defaults to whisper-1", func(t *testing.T) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		part, _ := writer.CreateFormFile("file", "test.mp3")
		_, _ = part.Write([]byte("audio data"))

		writer.Close()

		modelName, parsedReq, err := parseAudioTranscriptionBody(&extprocv3.HttpBody{Body: buf.Bytes()}, writer.FormDataContentType())
		require.NoError(t, err)
		require.Equal(t, "whisper-1", modelName)
		require.Equal(t, "whisper-1", parsedReq.Model)
	})

	t.Run("multipart missing boundary", func(t *testing.T) {
		_, _, err := parseAudioTranscriptionBody(&extprocv3.HttpBody{Body: []byte("test")}, "multipart/form-data")
		require.Error(t, err)
		require.Contains(t, err.Error(), "multipart content-type missing boundary")
	})

	t.Run("invalid content type", func(t *testing.T) {
		_, _, err := parseAudioTranscriptionBody(&extprocv3.HttpBody{Body: []byte("test")}, "invalid;;content-type")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse content-type")
	})

	t.Run("multipart with temperature as invalid json", func(t *testing.T) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		_ = writer.WriteField("model", "whisper-1")
		_ = writer.WriteField("temperature", "not-a-number")

		part, _ := writer.CreateFormFile("file", "test.mp3")
		_, _ = part.Write([]byte("audio data"))

		writer.Close()

		modelName, parsedReq, err := parseAudioTranscriptionBody(&extprocv3.HttpBody{Body: buf.Bytes()}, writer.FormDataContentType())
		require.NoError(t, err)
		require.Equal(t, "whisper-1", modelName)
		require.Nil(t, parsedReq.Temperature)
	})
}
