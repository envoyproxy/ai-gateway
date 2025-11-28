// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

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

type mockAudioSpeechMetrics struct {
	requestStart        time.Time
	originalModel       string
	requestModel        string
	responseModel       string
	backend             string
	requestSuccessCount int
	requestErrorCount   int
	tokenUsageCount     int
}

func (m *mockAudioSpeechMetrics) StartRequest(_ map[string]string) { m.requestStart = time.Now() }
func (m *mockAudioSpeechMetrics) SetOriginalModel(originalModel string) {
	m.originalModel = originalModel
}
func (m *mockAudioSpeechMetrics) SetRequestModel(requestModel string) { m.requestModel = requestModel }
func (m *mockAudioSpeechMetrics) SetResponseModel(responseModel string) {
	m.responseModel = responseModel
}
func (m *mockAudioSpeechMetrics) SetBackend(backend *filterapi.Backend) { m.backend = backend.Name }
func (m *mockAudioSpeechMetrics) RecordTokenUsage(_ context.Context, inputTokens uint32, _ map[string]string) {
	m.tokenUsageCount += int(inputTokens)
}

func (m *mockAudioSpeechMetrics) RecordRequestCompletion(_ context.Context, success bool, _ map[string]string) {
	if success {
		m.requestSuccessCount++
	} else {
		m.requestErrorCount++
	}
}

var _ metrics.AudioSpeechMetrics = &mockAudioSpeechMetrics{}

type mockAudioSpeechTranslator struct {
	t                   *testing.T
	expHeaders          map[string]string
	expRequestBody      *openai.AudioSpeechRequest
	expResponseBody     []byte
	retNewHeaders       []internalapi.Header
	retMutatedBody      []byte
	retUsedToken        translator.LLMTokenUsage
	retResponseModel    string
	retErr              error
	responseErrorCalled bool
}

func (m *mockAudioSpeechTranslator) RequestBody(_ []byte, body *openai.AudioSpeechRequest, _ bool) ([]internalapi.Header, []byte, error) {
	if m.expRequestBody != nil {
		require.Equal(m.t, m.expRequestBody, body)
	}
	return m.retNewHeaders, m.retMutatedBody, m.retErr
}

func (m *mockAudioSpeechTranslator) ResponseHeaders(headers map[string]string) ([]internalapi.Header, error) {
	if m.expHeaders != nil {
		require.Equal(m.t, m.expHeaders, headers)
	}
	return m.retNewHeaders, m.retErr
}

func (m *mockAudioSpeechTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ any) ([]internalapi.Header, []byte, translator.LLMTokenUsage, internalapi.ResponseModel, error) {
	if m.expResponseBody != nil {
		buf, err := io.ReadAll(body)
		require.NoError(m.t, err)
		require.Equal(m.t, m.expResponseBody, buf)
	}
	return m.retNewHeaders, m.retMutatedBody, m.retUsedToken, m.retResponseModel, m.retErr
}

func (m *mockAudioSpeechTranslator) ResponseError(_ map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	m.responseErrorCalled = true
	if m.expResponseBody != nil {
		buf, err := io.ReadAll(body)
		require.NoError(m.t, err)
		require.Equal(m.t, m.expResponseBody, buf)
	}
	return m.retNewHeaders, m.retMutatedBody, m.retErr
}

var _ translator.AudioSpeechTranslator = &mockAudioSpeechTranslator{}

func TestAudioSpeechProcessorFactory(t *testing.T) {
	t.Run("router filter", func(t *testing.T) {
		factory := AudioSpeechProcessorFactory(func() metrics.AudioSpeechMetrics {
			return &mockAudioSpeechMetrics{}
		})
		processor, err := factory(&filterapi.RuntimeConfig{}, map[string]string{}, slog.Default(), tracing.NoopTracing{}, false)
		require.NoError(t, err)
		require.NotNil(t, processor)
		require.IsType(t, &audioSpeechProcessorRouterFilter{}, processor)
	})

	t.Run("upstream filter", func(t *testing.T) {
		factory := AudioSpeechProcessorFactory(func() metrics.AudioSpeechMetrics {
			return &mockAudioSpeechMetrics{}
		})
		processor, err := factory(&filterapi.RuntimeConfig{}, map[string]string{}, slog.Default(), tracing.NoopTracing{}, true)
		require.NoError(t, err)
		require.NotNil(t, processor)
		require.IsType(t, &audioSpeechProcessorUpstreamFilter{}, processor)
	})
}

func TestAudioSpeechProcessorRouterFilter_ProcessRequestBody(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		p := &audioSpeechProcessorRouterFilter{
			requestHeaders: map[string]string{":path": "/v1/audio/speech"},
		}
		_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: []byte("invalid")})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse request body")
	})

	t.Run("success", func(t *testing.T) {
		req := openai.AudioSpeechRequest{
			Model: "tts-1",
			Input: "test input",
			Voice: "alloy",
		}
		reqBody, _ := json.Marshal(req)

		p := &audioSpeechProcessorRouterFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: map[string]string{":path": "/v1/audio/speech"},
			logger:         slog.Default(),
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
		require.Equal(t, "tts-1", string(headers[0].Header.RawValue))
	})
}

func TestAudioSpeechProcessorRouterFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("with upstream filter", func(t *testing.T) {
		headerMap := &corev3.HeaderMap{}
		mockUpstream := &mockProcessor{
			t:                     t,
			expHeaderMap:          headerMap,
			retProcessingResponse: &extprocv3.ProcessingResponse{},
		}
		p := &audioSpeechProcessorRouterFilter{
			upstreamFilter: mockUpstream,
		}

		resp, err := p.ProcessResponseHeaders(context.Background(), headerMap)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("without upstream filter", func(t *testing.T) {
		p := &audioSpeechProcessorRouterFilter{}
		resp, err := p.ProcessResponseHeaders(context.Background(), &corev3.HeaderMap{})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestAudioSpeechProcessorRouterFilter_ProcessResponseBody(t *testing.T) {
	t.Run("with upstream filter", func(t *testing.T) {
		httpBody := &extprocv3.HttpBody{}
		mockUpstream := &mockProcessor{
			t:                     t,
			expBody:               httpBody,
			retProcessingResponse: &extprocv3.ProcessingResponse{},
		}
		p := &audioSpeechProcessorRouterFilter{
			upstreamFilter: mockUpstream,
		}

		resp, err := p.ProcessResponseBody(context.Background(), httpBody)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("without upstream filter", func(t *testing.T) {
		p := &audioSpeechProcessorRouterFilter{}
		resp, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestAudioSpeechProcessorUpstreamFilter_SelectTranslator(t *testing.T) {
	t.Run("openai", func(t *testing.T) {
		p := &audioSpeechProcessorUpstreamFilter{}
		err := p.selectTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v1"})
		require.NoError(t, err)
		require.NotNil(t, p.translator)
	})

	t.Run("gcp vertex ai", func(t *testing.T) {
		p := &audioSpeechProcessorUpstreamFilter{}
		err := p.selectTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaGCPVertexAI})
		require.NoError(t, err)
		require.NotNil(t, p.translator)
	})

	t.Run("unsupported", func(t *testing.T) {
		p := &audioSpeechProcessorUpstreamFilter{}
		err := p.selectTranslator(filterapi.VersionedAPISchema{Name: "unsupported"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported API schema")
	})
}

func TestAudioSpeechProcessorUpstreamFilter_ProcessRequestHeaders(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockTranslator := &mockAudioSpeechTranslator{
			t:              t,
			retNewHeaders:  []internalapi.Header{},
			retMutatedBody: []byte("test"),
		}

		req := openai.AudioSpeechRequest{
			Model: "tts-1",
			Input: "test",
			Voice: "alloy",
		}
		reqBody, _ := json.Marshal(req)

		p := &audioSpeechProcessorUpstreamFilter{
			logger:                 slog.Default(),
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         map[string]string{},
			originalRequestBody:    &req,
			originalRequestBodyRaw: reqBody,
			translator:             mockTranslator,
			metrics:                &mockAudioSpeechMetrics{},
		}

		resp, err := p.ProcessRequestHeaders(context.Background(), &corev3.HeaderMap{})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("translator error", func(t *testing.T) {
		mockTranslator := &mockAudioSpeechTranslator{
			t:      t,
			retErr: errors.New("translator error"),
		}

		req := openai.AudioSpeechRequest{Model: "tts-1"}
		reqBody, _ := json.Marshal(req)

		p := &audioSpeechProcessorUpstreamFilter{
			logger:                 slog.Default(),
			config:                 &filterapi.RuntimeConfig{},
			requestHeaders:         map[string]string{},
			originalRequestBody:    &req,
			originalRequestBodyRaw: reqBody,
			translator:             mockTranslator,
			metrics:                &mockAudioSpeechMetrics{},
		}

		_, err := p.ProcessRequestHeaders(context.Background(), &corev3.HeaderMap{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to transform request")
	})
}

func TestAudioSpeechProcessorUpstreamFilter_ProcessRequestBody(t *testing.T) {
	p := &audioSpeechProcessorUpstreamFilter{}
	require.Panics(t, func() {
		_, _ = p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{})
	})
}

func TestAudioSpeechProcessorUpstreamFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockTranslator := &mockAudioSpeechTranslator{
			t:             t,
			retNewHeaders: []internalapi.Header{},
		}

		p := &audioSpeechProcessorUpstreamFilter{
			translator: mockTranslator,
			metrics:    &mockAudioSpeechMetrics{},
		}

		headers := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{
				{Key: "content-type", RawValue: []byte("audio/mpeg")},
			},
		}

		resp, err := p.ProcessResponseHeaders(context.Background(), headers)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("with content-encoding", func(t *testing.T) {
		mockTranslator := &mockAudioSpeechTranslator{
			t:             t,
			retNewHeaders: []internalapi.Header{},
		}

		p := &audioSpeechProcessorUpstreamFilter{
			translator: mockTranslator,
			metrics:    &mockAudioSpeechMetrics{},
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
		mockTranslator := &mockAudioSpeechTranslator{
			t:      t,
			retErr: errors.New("translator error"),
		}

		p := &audioSpeechProcessorUpstreamFilter{
			translator: mockTranslator,
			metrics:    &mockAudioSpeechMetrics{},
		}

		_, err := p.ProcessResponseHeaders(context.Background(), &corev3.HeaderMap{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to transform response headers")
	})
}

func TestAudioSpeechProcessorUpstreamFilter_ProcessResponseBody(t *testing.T) {
	t.Run("success end of stream", func(t *testing.T) {
		mockTranslator := &mockAudioSpeechTranslator{
			t:              t,
			retNewHeaders:  []internalapi.Header{},
			retMutatedBody: []byte{},
			retUsedToken: translator.LLMTokenUsage{
				InputTokens: 100,
				TotalTokens: 100,
			},
			retResponseModel: "tts-1",
		}

		p := &audioSpeechProcessorUpstreamFilter{
			translator:      mockTranslator,
			requestHeaders:  map[string]string{},
			responseHeaders: map[string]string{":status": "200"},
			config:          &filterapi.RuntimeConfig{},
			metrics:         &mockAudioSpeechMetrics{},
		}

		resp, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{
			Body:        []byte("audio data"),
			EndOfStream: true,
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("error response", func(t *testing.T) {
		mockTranslator := &mockAudioSpeechTranslator{
			t:              t,
			retNewHeaders:  []internalapi.Header{},
			retMutatedBody: []byte{},
		}

		p := &audioSpeechProcessorUpstreamFilter{
			translator:      mockTranslator,
			requestHeaders:  map[string]string{},
			responseHeaders: map[string]string{":status": "400"},
			config:          &filterapi.RuntimeConfig{},
			metrics:         &mockAudioSpeechMetrics{},
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
		mockTranslator := &mockAudioSpeechTranslator{
			t:      t,
			retErr: errors.New("translator error"),
		}

		p := &audioSpeechProcessorUpstreamFilter{
			translator:      mockTranslator,
			requestHeaders:  map[string]string{},
			responseHeaders: map[string]string{":status": "200"},
			config:          &filterapi.RuntimeConfig{},
			metrics:         &mockAudioSpeechMetrics{},
		}

		_, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{
			Body:        []byte("audio data"),
			EndOfStream: true,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to transform response")
	})
}

func TestAudioSpeechProcessorUpstreamFilter_SetBackend(t *testing.T) {
	t.Run("openai backend", func(t *testing.T) {
		routeProcessor := &audioSpeechProcessorRouterFilter{
			originalRequestBody:    &openai.AudioSpeechRequest{Model: "tts-1"},
			originalRequestBodyRaw: []byte("test"),
		}

		backend := &filterapi.Backend{
			Name:   "test-backend",
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v1"},
		}

		p := &audioSpeechProcessorUpstreamFilter{
			config:  &filterapi.RuntimeConfig{},
			metrics: &mockAudioSpeechMetrics{},
		}

		err := p.SetBackend(context.Background(), backend, nil, routeProcessor)
		require.NoError(t, err)
		require.NotNil(t, p.translator)
		require.Equal(t, "test-backend", p.backendName)
	})

	t.Run("gcp vertex ai backend", func(t *testing.T) {
		routeProcessor := &audioSpeechProcessorRouterFilter{
			originalRequestBody:    &openai.AudioSpeechRequest{Model: "tts-1"},
			originalRequestBodyRaw: []byte("test"),
		}

		backend := &filterapi.Backend{
			Name:   "gcp-backend",
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaGCPVertexAI},
		}

		p := &audioSpeechProcessorUpstreamFilter{
			config:  &filterapi.RuntimeConfig{},
			metrics: &mockAudioSpeechMetrics{},
		}

		err := p.SetBackend(context.Background(), backend, nil, routeProcessor)
		require.NoError(t, err)
		require.NotNil(t, p.translator)
	})

	t.Run("model name override", func(t *testing.T) {
		routeProcessor := &audioSpeechProcessorRouterFilter{
			originalRequestBody:    &openai.AudioSpeechRequest{Model: "tts-1"},
			originalRequestBodyRaw: []byte("test"),
			requestHeaders:         map[string]string{},
		}

		backend := &filterapi.Backend{
			Name:              "test-backend",
			Schema:            filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Version: "v1"},
			ModelNameOverride: "override-model",
		}

		p := &audioSpeechProcessorUpstreamFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: map[string]string{},
			metrics:        &mockAudioSpeechMetrics{},
		}

		err := p.SetBackend(context.Background(), backend, nil, routeProcessor)
		require.NoError(t, err)
		require.Equal(t, "override-model", p.modelNameOverride)
	})

	t.Run("unsupported schema", func(t *testing.T) {
		routeProcessor := &audioSpeechProcessorRouterFilter{
			originalRequestBody:    &openai.AudioSpeechRequest{Model: "tts-1"},
			originalRequestBodyRaw: []byte("test"),
		}

		backend := &filterapi.Backend{
			Name:   "unsupported-backend",
			Schema: filterapi.VersionedAPISchema{Name: "unsupported"},
		}

		p := &audioSpeechProcessorUpstreamFilter{
			config:  &filterapi.RuntimeConfig{},
			metrics: &mockAudioSpeechMetrics{},
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

		p := &audioSpeechProcessorUpstreamFilter{
			config:  &filterapi.RuntimeConfig{},
			metrics: &mockAudioSpeechMetrics{},
		}

		require.Panics(t, func() {
			_ = p.SetBackend(context.Background(), backend, nil, &mockProcessor{})
		})
	})
}

func TestParseAudioSpeechBody(t *testing.T) {
	t.Run("valid body", func(t *testing.T) {
		req := openai.AudioSpeechRequest{
			Model: "tts-1",
			Input: "test input",
			Voice: "alloy",
		}
		body, _ := json.Marshal(req)

		modelName, parsedReq, err := parseAudioSpeechBody(&extprocv3.HttpBody{Body: body})
		require.NoError(t, err)
		require.Equal(t, "tts-1", modelName)
		require.Equal(t, "test input", parsedReq.Input)
		require.Equal(t, "alloy", parsedReq.Voice)
	})

	t.Run("invalid json", func(t *testing.T) {
		_, _, err := parseAudioSpeechBody(&extprocv3.HttpBody{Body: []byte("invalid")})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal body")
	})
}
