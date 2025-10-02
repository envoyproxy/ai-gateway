// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestCompletions_Schema(t *testing.T) {
	tests := []struct {
		name         string
		onUpstream   bool
		expectedType interface{}
	}{
		{
			name:         "supported openai / on route",
			onUpstream:   false,
			expectedType: &completionsProcessorRouterFilter{},
		},
		{
			name:         "supported openai / on upstream",
			onUpstream:   true,
			expectedType: &completionsProcessorUpstreamFilter{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &processorConfig{}
			filter, err := CompletionsProcessorFactory(nil)(cfg, nil, slog.Default(), nil, tt.onUpstream)
			require.NoError(t, err)
			require.NotNil(t, filter)
			require.IsType(t, tt.expectedType, filter)
		})
	}
}

func Test_completionsProcessorUpstreamFilter_SelectTranslator(t *testing.T) {
	tests := []struct {
		name          string
		schema        filterapi.VersionedAPISchema
		expectError   bool
		errorContains string
	}{
		{
			name:          "unsupported",
			schema:        filterapi.VersionedAPISchema{Name: "Bar", Version: "v123"},
			expectError:   true,
			errorContains: "unsupported API schema: backend={Bar v123}",
		},
		{
			name:        "supported openai",
			schema:      filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &completionsProcessorUpstreamFilter{}
			err := c.selectTranslator(tt.schema)
			if tt.expectError {
				require.ErrorContains(t, err, tt.errorContains)
			} else {
				require.NoError(t, err)
				require.NotNil(t, c.translator)
			}
		})
	}
}

func Test_completionsProcessorRouterFilter_ProcessRequestBody(t *testing.T) {
	t.Run("body parser error", func(t *testing.T) {
		p := &completionsProcessorRouterFilter{}
		_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: []byte("nonjson")})
		require.ErrorContains(t, err, "invalid character 'o' in literal null")
	})

	t.Run("ok", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		const modelKey = "x-ai-gateway-model-key"
		p := &completionsProcessorRouterFilter{
			config:         &processorConfig{modelNameHeaderKey: modelKey},
			requestHeaders: headers,
			logger:         slog.Default(),
		}
		resp, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{Body: completionBodyFromModel(t, "some-model")})
		require.NoError(t, err)
		require.NotNil(t, resp)
		re, ok := resp.Response.(*extprocv3.ProcessingResponse_RequestBody)
		require.True(t, ok)
		require.NotNil(t, re)
		require.NotNil(t, re.RequestBody)
		setHeaders := re.RequestBody.GetResponse().GetHeaderMutation().SetHeaders
		require.Len(t, setHeaders, 2)
		require.Equal(t, modelKey, setHeaders[0].Header.Key)
		require.Equal(t, "some-model", string(setHeaders[0].Header.RawValue))
		require.Equal(t, "x-ai-eg-original-path", setHeaders[1].Header.Key)
		require.Equal(t, "/foo", string(setHeaders[1].Header.RawValue))
	})
}

func Test_completionsProcessorUpstreamFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mt := &mockCompletionTranslator{t: t, expHeaders: make(map[string]string)}
		p := &completionsProcessorUpstreamFilter{
			translator: mt,
		}
		const headerName = ":test-header:"
		const headerValue = ":test-header-value:"
		mt.expHeaders[headerName] = headerValue
		mt.resHeaderMutation = &extprocv3.HeaderMutation{
			SetHeaders: []*corev3.HeaderValueOption{
				{Header: &corev3.HeaderValue{Key: headerName, RawValue: []byte(headerValue)}},
			},
		}

		resp, err := p.ProcessResponseHeaders(t.Context(), &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{
				{Key: headerName, RawValue: []byte(headerValue)},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		re, ok := resp.Response.(*extprocv3.ProcessingResponse_ResponseHeaders)
		require.True(t, ok)
		require.NotNil(t, re)
		require.NotNil(t, re.ResponseHeaders)
		setHeaders := re.ResponseHeaders.GetResponse().GetHeaderMutation().SetHeaders
		require.Len(t, setHeaders, 1)
		require.Equal(t, headerName, setHeaders[0].Header.Key)
		require.Equal(t, headerValue, string(setHeaders[0].Header.RawValue))
	})
}

func Test_completionsProcessorUpstreamFilter_ProcessResponseBody(t *testing.T) {
	t.Run("error response", func(t *testing.T) {
		mt := &mockCompletionTranslator{t: t}
		p := &completionsProcessorUpstreamFilter{
			translator:      mt,
			responseHeaders: map[string]string{":status": "400"},
		}

		mt.resErrorHeaderMutation = &extprocv3.HeaderMutation{
			SetHeaders: []*corev3.HeaderValueOption{
				{Header: &corev3.HeaderValue{Key: "test", RawValue: []byte("error")}},
			},
		}
		mt.resErrorBodyMutation = &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: []byte("error body")},
		}

		resp, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: []byte("test error")})
		require.NoError(t, err)
		require.NotNil(t, resp)

		re, ok := resp.Response.(*extprocv3.ProcessingResponse_ResponseBody)
		require.True(t, ok)
		require.NotNil(t, re)
		require.NotNil(t, re.ResponseBody)
		require.Equal(t, mt.resErrorHeaderMutation, re.ResponseBody.GetResponse().GetHeaderMutation())
		require.Equal(t, mt.resErrorBodyMutation, re.ResponseBody.GetResponse().GetBodyMutation())
	})

	t.Run("successful response with token usage", func(t *testing.T) {
		mt := &mockCompletionTranslator{t: t}
		p := &completionsProcessorUpstreamFilter{
			translator:      mt,
			responseHeaders: map[string]string{":status": "200"},
			config:          &processorConfig{},
			logger:          slog.Default(),
		}

		mt.resHeaderMutation = &extprocv3.HeaderMutation{
			SetHeaders: []*corev3.HeaderValueOption{
				{Header: &corev3.HeaderValue{Key: "test", RawValue: []byte("success")}},
			},
		}
		mt.resBodyMutation = &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: []byte("response body")},
		}
		mt.resTokenUsage = translator.LLMTokenUsage{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
		}
		mt.resModel = "gpt-4"

		resp, err := p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{Body: []byte("test"), EndOfStream: true})
		require.NoError(t, err)
		require.NotNil(t, resp)

		re, ok := resp.Response.(*extprocv3.ProcessingResponse_ResponseBody)
		require.True(t, ok)
		require.NotNil(t, re)
		require.NotNil(t, re.ResponseBody)
		require.Equal(t, mt.resHeaderMutation, re.ResponseBody.GetResponse().GetHeaderMutation())
		require.Equal(t, mt.resBodyMutation, re.ResponseBody.GetResponse().GetBodyMutation())

		// Check that costs were accumulated
		require.Equal(t, uint32(10), p.costs.InputTokens)
		require.Equal(t, uint32(20), p.costs.OutputTokens)
		require.Equal(t, uint32(30), p.costs.TotalTokens)
	})
}

func Test_completionsProcessorUpstreamFilter_SetBackend(t *testing.T) {
	tests := []struct {
		name                        string
		routeFilterCount            int
		backend                     *filterapi.Backend
		expectedModelOverride       string
		expectedBackendName         string
		expectedOnRetry             bool
		expectedUpstreamFilterCount int
	}{
		{
			name:             "set backend with model override",
			routeFilterCount: 0,
			backend: &filterapi.Backend{
				Name:              "test-backend",
				ModelNameOverride: "override-model",
				Schema:            filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
				HeaderMutation: &filterapi.HTTPHeaderMutation{
					Set: []filterapi.HTTPHeader{
						{Name: "backend-header", Value: "value"},
					},
				},
			},
			expectedModelOverride:       "override-model",
			expectedBackendName:         "test-backend",
			expectedOnRetry:             false,
			expectedUpstreamFilterCount: 1,
		},
		{
			name:             "retry request",
			routeFilterCount: 1,
			backend: &filterapi.Backend{
				Name:   "retry-backend",
				Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			},
			expectedModelOverride:       "",
			expectedBackendName:         "retry-backend",
			expectedOnRetry:             true,
			expectedUpstreamFilterCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routeFilter := &completionsProcessorRouterFilter{
				config:                 &processorConfig{modelNameHeaderKey: "x-model"},
				requestHeaders:         make(map[string]string),
				originalRequestBody:    &openai.CompletionRequest{Model: "test-model"},
				originalRequestBodyRaw: []byte(`{"model":"test-model"}`),
				upstreamFilterCount:    tt.routeFilterCount,
			}
			upstreamFilter := &completionsProcessorUpstreamFilter{
				config:         routeFilter.config,
				requestHeaders: routeFilter.requestHeaders,
			}

			err := upstreamFilter.SetBackend(t.Context(), tt.backend, nil, routeFilter)
			require.NoError(t, err)
			require.Equal(t, tt.expectedModelOverride, upstreamFilter.modelNameOverride)
			require.Equal(t, tt.expectedBackendName, upstreamFilter.backendName)
			require.Equal(t, routeFilter.originalRequestBody, upstreamFilter.originalRequestBody)
			require.Equal(t, routeFilter.originalRequestBodyRaw, upstreamFilter.originalRequestBodyRaw)
			require.Equal(t, tt.expectedOnRetry, upstreamFilter.onRetry)
			require.NotNil(t, upstreamFilter.translator)
			if tt.backend.HeaderMutation != nil {
				require.NotNil(t, upstreamFilter.headerMutator)
			}
			if tt.expectedModelOverride != "" {
				require.Equal(t, tt.expectedModelOverride, upstreamFilter.requestHeaders["x-model"])
			}
			require.Equal(t, upstreamFilter, routeFilter.upstreamFilter)
			require.Equal(t, tt.expectedUpstreamFilterCount, routeFilter.upstreamFilterCount)
		})
	}
}

func Test_completionsProcessorRouterFilter_ProcessResponseHeaders(t *testing.T) {
	t.Run("with upstream filter", func(t *testing.T) {
		mt := &mockCompletionTranslator{t: t, expHeaders: make(map[string]string)}
		upstreamFilter := &completionsProcessorUpstreamFilter{
			translator: mt,
		}
		routeFilter := &completionsProcessorRouterFilter{
			upstreamFilter: upstreamFilter,
		}

		headers := &corev3.HeaderMap{}
		resp, err := routeFilter.ProcessResponseHeaders(t.Context(), headers)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("without upstream filter", func(t *testing.T) {
		routeFilter := &completionsProcessorRouterFilter{}
		headers := &corev3.HeaderMap{}
		resp, err := routeFilter.ProcessResponseHeaders(t.Context(), headers)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func Test_completionsProcessorRouterFilter_ProcessResponseBody(t *testing.T) {
	t.Run("with upstream filter", func(t *testing.T) {
		mt := &mockCompletionTranslator{t: t}
		upstreamFilter := &completionsProcessorUpstreamFilter{
			translator:      mt,
			responseHeaders: map[string]string{":status": "200"},
		}
		routeFilter := &completionsProcessorRouterFilter{
			upstreamFilter: upstreamFilter,
		}

		body := &extprocv3.HttpBody{Body: []byte("test")}
		resp, err := routeFilter.ProcessResponseBody(t.Context(), body)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("without upstream filter", func(t *testing.T) {
		routeFilter := &completionsProcessorRouterFilter{}
		body := &extprocv3.HttpBody{Body: []byte("test")}
		resp, err := routeFilter.ProcessResponseBody(t.Context(), body)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func Test_completionsProcessorUpstreamFilter_ProcessRequestHeaders(t *testing.T) {
	mt := &mockCompletionTranslator{t: t}
	upstreamFilter := &completionsProcessorUpstreamFilter{
		config:                 &processorConfig{},
		requestHeaders:         make(map[string]string),
		originalRequestBody:    &openai.CompletionRequest{Model: "test"},
		originalRequestBodyRaw: []byte(`{"model":"test"}`),
		translator:             mt,
	}

	headers := &corev3.HeaderMap{}
	resp, err := upstreamFilter.ProcessRequestHeaders(t.Context(), headers)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify CONTINUE_AND_REPLACE status
	rh := resp.GetRequestHeaders()
	require.NotNil(t, rh)
	require.Equal(t, extprocv3.CommonResponse_CONTINUE_AND_REPLACE, rh.GetResponse().GetStatus())
}

func TestCompletionsProcessorUpstreamFilter_UnimplementedMethods(t *testing.T) {
	p := &completionsProcessorUpstreamFilter{}
	_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{})
	require.ErrorIs(t, err, errUnexpectedCall)
}

// Helper function to create completion request body
func completionBodyFromModel(t *testing.T, model string) []byte {
	t.Helper()
	req := openai.CompletionRequest{
		Model:  model,
		Prompt: openai.PromptUnion{Value: "test prompt"},
	}
	b, err := json.Marshal(req)
	require.NoError(t, err)
	return b
}

// Mock translator for testing
type mockCompletionTranslator struct {
	t                      *testing.T
	expHeaders             map[string]string
	resHeaderMutation      *extprocv3.HeaderMutation
	resBodyMutation        *extprocv3.BodyMutation
	resErrorHeaderMutation *extprocv3.HeaderMutation
	resErrorBodyMutation   *extprocv3.BodyMutation
	resTokenUsage          translator.LLMTokenUsage
	resModel               internalapi.ResponseModel
	err                    error
}

func (m *mockCompletionTranslator) RequestBody([]byte, *openai.CompletionRequest, bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	return nil, nil, m.err
}

func (m *mockCompletionTranslator) ResponseHeaders(headers map[string]string) (*extprocv3.HeaderMutation, error) {
	for k, v := range m.expHeaders {
		require.Equal(m.t, v, headers[k])
	}
	return m.resHeaderMutation, m.err
}

func (m *mockCompletionTranslator) ResponseBody(map[string]string, io.Reader, bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, translator.LLMTokenUsage, internalapi.ResponseModel, error) {
	return m.resHeaderMutation, m.resBodyMutation, m.resTokenUsage, m.resModel, m.err
}

func (m *mockCompletionTranslator) ResponseError(map[string]string, io.Reader) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	return m.resErrorHeaderMutation, m.resErrorBodyMutation, m.err
}
