// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

type mockRealtimeClientSecretsTranslator struct {
	t                 *testing.T
	expRequestBody    *openai.RealtimeClientSecretRequest
	expResponseBody   []byte
	retHeaderMutation *extprocv3.HeaderMutation
	retBodyMutation   *extprocv3.BodyMutation
	retErr            error
}

func (m *mockRealtimeClientSecretsTranslator) RequestBody(body *openai.RealtimeClientSecretRequest) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	if m.expRequestBody != nil {
		require.Equal(m.t, m.expRequestBody, body)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

func (m *mockRealtimeClientSecretsTranslator) ResponseBody(body []byte) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	if m.expResponseBody != nil {
		require.Equal(m.t, m.expResponseBody, body)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

var _ translator.RealtimeClientSecretsTranslator = &mockRealtimeClientSecretsTranslator{}

func TestRealtimeClientSecretsProcessorFactory(t *testing.T) {
	t.Run("router filter", func(t *testing.T) {
		factory := RealtimeClientSecretsProcessorFactory()
		processor, err := factory(&filterapi.RuntimeConfig{}, map[string]string{}, slog.Default(), tracing.NoopTracing{}, false)
		require.NoError(t, err)
		require.NotNil(t, processor)
		require.IsType(t, &realtimeClientSecretsProcessorRouterFilter{}, processor)
	})

	t.Run("upstream filter", func(t *testing.T) {
		factory := RealtimeClientSecretsProcessorFactory()
		processor, err := factory(&filterapi.RuntimeConfig{}, map[string]string{}, slog.Default(), tracing.NoopTracing{}, true)
		require.NoError(t, err)
		require.NotNil(t, processor)
		require.IsType(t, &realtimeClientSecretsProcessorUpstreamFilter{}, processor)
	})
}

func TestRealtimeClientSecretsProcessorRouterFilter_ProcessRequestHeaders(t *testing.T) {
	p := &realtimeClientSecretsProcessorRouterFilter{}
	resp, err := p.ProcessRequestHeaders(context.Background(), &corev3.HeaderMap{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.GetRequestHeaders())
}

func TestRealtimeClientSecretsProcessorRouterFilter_ProcessRequestBody(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		p := &realtimeClientSecretsProcessorRouterFilter{
			requestHeaders: map[string]string{":path": "/v1/realtime/client_secrets"},
		}
		_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: []byte("invalid")})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse request body")
	})

	t.Run("success with model", func(t *testing.T) {
		req := openai.RealtimeClientSecretRequest{
			Session: &openai.RealtimeClientSecretSession{
				Model: "gpt-4o-realtime-preview",
			},
		}
		reqBody, _ := json.Marshal(req)

		p := &realtimeClientSecretsProcessorRouterFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: map[string]string{":path": "/v1/realtime/client_secrets"},
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
		require.Equal(t, "gpt-4o-realtime-preview", string(headers[0].Header.RawValue))
		require.Equal(t, originalPathHeader, headers[1].Header.Key)
	})

	t.Run("success without model", func(t *testing.T) {
		req := openai.RealtimeClientSecretRequest{}
		reqBody, _ := json.Marshal(req)

		p := &realtimeClientSecretsProcessorRouterFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: map[string]string{":path": "/v1/realtime/client_secrets"},
			logger:         slog.Default(),
		}

		resp, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: reqBody})
		require.NoError(t, err)
		require.NotNil(t, resp)

		rb := resp.GetRequestBody()
		headers := rb.Response.HeaderMutation.SetHeaders
		require.Equal(t, "gpt-realtime", string(headers[0].Header.RawValue))
	})

	t.Run("success with nil session", func(t *testing.T) {
		req := openai.RealtimeClientSecretRequest{
			Session: nil,
		}
		reqBody, _ := json.Marshal(req)

		p := &realtimeClientSecretsProcessorRouterFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: map[string]string{":path": "/v1/realtime/client_secrets"},
			logger:         slog.Default(),
		}

		resp, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: reqBody})
		require.NoError(t, err)
		require.NotNil(t, resp)

		rb := resp.GetRequestBody()
		headers := rb.Response.HeaderMutation.SetHeaders
		require.Equal(t, "gpt-realtime", string(headers[0].Header.RawValue))
	})
}

func TestRealtimeClientSecretsProcessorRouterFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("with upstream filter", func(t *testing.T) {
		headerMap := &corev3.HeaderMap{}
		mockUpstream := &mockProcessor{
			t:                     t,
			expHeaderMap:          headerMap,
			retProcessingResponse: &extprocv3.ProcessingResponse{},
		}
		p := &realtimeClientSecretsProcessorRouterFilter{
			upstreamFilter: mockUpstream,
		}

		resp, err := p.ProcessResponseHeaders(context.Background(), headerMap)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("without upstream filter", func(t *testing.T) {
		p := &realtimeClientSecretsProcessorRouterFilter{}
		resp, err := p.ProcessResponseHeaders(context.Background(), &corev3.HeaderMap{})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestRealtimeClientSecretsProcessorRouterFilter_ProcessResponseBody(t *testing.T) {
	t.Run("with upstream filter", func(t *testing.T) {
		httpBody := &extprocv3.HttpBody{}
		mockUpstream := &mockProcessor{
			t:                     t,
			expBody:               httpBody,
			retProcessingResponse: &extprocv3.ProcessingResponse{},
		}
		p := &realtimeClientSecretsProcessorRouterFilter{
			upstreamFilter: mockUpstream,
		}

		resp, err := p.ProcessResponseBody(context.Background(), httpBody)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("without upstream filter", func(t *testing.T) {
		p := &realtimeClientSecretsProcessorRouterFilter{}
		resp, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestRealtimeClientSecretsProcessorRouterFilter_SetBackend(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		backend := &filterapi.Backend{
			Name:   "test-backend",
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		}

		p := &realtimeClientSecretsProcessorRouterFilter{
			config:         &filterapi.RuntimeConfig{},
			requestHeaders: map[string]string{},
			logger:         slog.Default(),
		}

		err := p.SetBackend(context.Background(), backend, nil, p)
		require.NoError(t, err)
		require.NotNil(t, p.upstreamFilter)
	})
}

func TestRealtimeClientSecretsProcessorUpstreamFilter_SetBackend(t *testing.T) {
	t.Run("openai backend", func(t *testing.T) {
		backend := &filterapi.Backend{
			Name:   "openai-backend",
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		}

		p := &realtimeClientSecretsProcessorUpstreamFilter{
			logger: slog.Default(),
		}

		err := p.SetBackend(context.Background(), backend, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, p.translator)
	})

	t.Run("azure openai backend", func(t *testing.T) {
		backend := &filterapi.Backend{
			Name:   "azure-backend",
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI},
		}

		p := &realtimeClientSecretsProcessorUpstreamFilter{
			logger: slog.Default(),
		}

		err := p.SetBackend(context.Background(), backend, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, p.translator)
	})

	t.Run("gcp vertex ai backend", func(t *testing.T) {
		backend := &filterapi.Backend{
			Name:   "gcp-backend",
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaGCPVertexAI},
		}

		p := &realtimeClientSecretsProcessorUpstreamFilter{
			logger: slog.Default(),
		}

		err := p.SetBackend(context.Background(), backend, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, p.translator)
	})

	t.Run("unsupported schema", func(t *testing.T) {
		backend := &filterapi.Backend{
			Name:   "unsupported-backend",
			Schema: filterapi.VersionedAPISchema{Name: "unsupported"},
		}

		p := &realtimeClientSecretsProcessorUpstreamFilter{
			logger: slog.Default(),
		}

		err := p.SetBackend(context.Background(), backend, nil, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported schema for realtime client_secrets")
	})

	t.Run("with auth handler", func(t *testing.T) {
		backend := &filterapi.Backend{
			Name:   "openai-backend",
			Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		}

		p := &realtimeClientSecretsProcessorUpstreamFilter{
			logger: slog.Default(),
		}

		err := p.SetBackend(context.Background(), backend, &mockBackendAuthHandler{}, nil)
		require.NoError(t, err)
		require.NotNil(t, p.handler)
	})
}

func TestRealtimeClientSecretsProcessorUpstreamFilter_ProcessRequestHeaders(t *testing.T) {
	p := &realtimeClientSecretsProcessorUpstreamFilter{}
	resp, err := p.ProcessRequestHeaders(context.Background(), &corev3.HeaderMap{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.ModeOverride)
}

func TestRealtimeClientSecretsProcessorUpstreamFilter_ProcessRequestBody(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		p := &realtimeClientSecretsProcessorUpstreamFilter{
			translator: &mockRealtimeClientSecretsTranslator{},
		}
		_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: []byte("invalid")})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse request body")
	})

	t.Run("success", func(t *testing.T) {
		req := openai.RealtimeClientSecretRequest{
			Session: &openai.RealtimeClientSecretSession{
				Model: "gpt-4o-realtime-preview",
			},
		}
		reqBody, _ := json.Marshal(req)

		mockTranslator := &mockRealtimeClientSecretsTranslator{
			t: t,
			retHeaderMutation: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{Header: &corev3.HeaderValue{Key: ":path", RawValue: []byte("/v1/realtime")}},
				},
			},
			retBodyMutation: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{Body: []byte("translated")},
			},
		}

		p := &realtimeClientSecretsProcessorUpstreamFilter{
			translator:     mockTranslator,
			requestHeaders: map[string]string{},
		}

		resp, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: reqBody})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("translator error", func(t *testing.T) {
		req := openai.RealtimeClientSecretRequest{}
		reqBody, _ := json.Marshal(req)

		mockTranslator := &mockRealtimeClientSecretsTranslator{
			t:      t,
			retErr: errors.New("translator error"),
		}

		p := &realtimeClientSecretsProcessorUpstreamFilter{
			translator: mockTranslator,
		}

		_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: reqBody})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to translate request")
	})

	t.Run("with auth handler", func(t *testing.T) {
		req := openai.RealtimeClientSecretRequest{}
		reqBody, _ := json.Marshal(req)

		mockTranslator := &mockRealtimeClientSecretsTranslator{
			t: t,
			retHeaderMutation: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{Header: &corev3.HeaderValue{Key: ":path", RawValue: []byte("/v1/realtime")}},
				},
			},
			retBodyMutation: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{Body: []byte("translated")},
			},
		}

		p := &realtimeClientSecretsProcessorUpstreamFilter{
			translator:     mockTranslator,
			requestHeaders: map[string]string{},
			handler:        &mockBackendAuthHandler{},
		}

		resp, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: reqBody})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("auth handler error", func(t *testing.T) {
		req := openai.RealtimeClientSecretRequest{}
		reqBody, _ := json.Marshal(req)

		mockTranslator := &mockRealtimeClientSecretsTranslator{
			t: t,
			retHeaderMutation: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{Header: &corev3.HeaderValue{Key: ":path", RawValue: []byte("/v1/realtime")}},
				},
			},
			retBodyMutation: &extprocv3.BodyMutation{},
		}

		p := &realtimeClientSecretsProcessorUpstreamFilter{
			translator:     mockTranslator,
			requestHeaders: map[string]string{},
			handler:        &mockBackendAuthHandlerError{},
		}

		_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{Body: reqBody})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to apply authentication")
	})
}

func TestRealtimeClientSecretsProcessorUpstreamFilter_ProcessResponseHeaders(t *testing.T) {
	p := &realtimeClientSecretsProcessorUpstreamFilter{}
	headers := &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{
			{Key: "content-type", RawValue: []byte("application/json")},
		},
	}
	resp, err := p.ProcessResponseHeaders(context.Background(), headers)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.ModeOverride)
}

func TestRealtimeClientSecretsProcessorUpstreamFilter_ProcessResponseBody(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockTranslator := &mockRealtimeClientSecretsTranslator{
			t:                 t,
			retHeaderMutation: &extprocv3.HeaderMutation{},
			retBodyMutation: &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{Body: []byte("translated response")},
			},
		}

		p := &realtimeClientSecretsProcessorUpstreamFilter{
			translator: mockTranslator,
		}

		resp, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{
			Body: []byte("response"),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("translator error", func(t *testing.T) {
		mockTranslator := &mockRealtimeClientSecretsTranslator{
			t:      t,
			retErr: errors.New("translator error"),
		}

		p := &realtimeClientSecretsProcessorUpstreamFilter{
			translator: mockTranslator,
		}

		_, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{
			Body: []byte("response"),
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to translate response")
	})
}
