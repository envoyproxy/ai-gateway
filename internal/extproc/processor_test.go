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

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

func TestProcessor_ProcessRequestHeaders(t *testing.T) {
	p := &Processor{}
	res, err := p.ProcessRequestHeaders(context.Background(), &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}},
	})
	require.NoError(t, err)
	_, ok := res.Response.(*extprocv3.ProcessingResponse_RequestHeaders)
	require.True(t, ok)
	require.Equal(t, map[string]string{"foo": "bar"}, p.requestHeaders)
}

func TestProcessor_ProcessResponseHeaders(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mt := &mockTranslator{t: t, expHeaders: make(map[string]string)}
		p := &Processor{translator: mt}
		mt.retErr = errors.New("test error")
		_, err := p.ProcessResponseHeaders(context.Background(), nil)
		require.ErrorContains(t, err, "test error")
	})
	t.Run("ok", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}, {Key: "dog", RawValue: []byte("cat")}},
		}
		expHeaders := map[string]string{"foo": "bar", "dog": "cat"}
		mt := &mockTranslator{t: t, expHeaders: expHeaders}
		p := &Processor{translator: mt}
		res, err := p.ProcessResponseHeaders(context.Background(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Equal(t, mt.retHeaderMutation, commonRes.HeaderMutation)
	})
}

func TestProcessor_ProcessResponseBody(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mt := &mockTranslator{t: t}
		p := &Processor{translator: mt}
		mt.retErr = errors.New("test error")
		_, err := p.ProcessResponseBody(context.Background(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "test error")
	})
	t.Run("ok", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{Body: []byte("some-body"), EndOfStream: true}
		expBodyMut := &extprocv3.BodyMutation{}
		expHeadMut := &extprocv3.HeaderMutation{}
		mt := &mockTranslator{
			t: t, expResponseBody: inBody,
			retBodyMutation: expBodyMut, retHeaderMutation: expHeadMut,
			retUsedToken: translator.LLMTokenUsage{OutputTokens: 123, InputTokens: 1},
		}

		celProgInt, err := llmcostcel.NewProgram("54321")
		require.NoError(t, err)
		celProgUint, err := llmcostcel.NewProgram("uint(9999)")
		require.NoError(t, err)
		p := &Processor{
			translator: mt,
			logger:     slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})),
			config: &processorConfig{
				metadataNamespace: "ai_gateway_llm_ns",
				requestCosts: []processorConfigRequestCost{
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeOutputToken, MetadataKey: "output_token_usage"}},
					{LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeInputToken, MetadataKey: "input_token_usage"}},
					{
						celProg:        celProgInt,
						LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeCELExpression, MetadataKey: "cel_int"},
					},
					{
						celProg:        celProgUint,
						LLMRequestCost: &filterapi.LLMRequestCost{Type: filterapi.LLMRequestCostTypeCELExpression, MetadataKey: "cel_uint"},
					},
				},
			},
			metrics: metrics.New(),
		}
		res, err := p.ProcessResponseBody(context.Background(), inBody)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
		require.Equal(t, expBodyMut, commonRes.BodyMutation)
		require.Equal(t, expHeadMut, commonRes.HeaderMutation)

		md := res.DynamicMetadata
		require.NotNil(t, md)
		require.Equal(t, float64(123), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["output_token_usage"].GetNumberValue())
		require.Equal(t, float64(1), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["input_token_usage"].GetNumberValue())
		require.Equal(t, float64(54321), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["cel_int"].GetNumberValue())
		require.Equal(t, float64(9999), md.Fields["ai_gateway_llm_ns"].
			GetStructValue().Fields["cel_uint"].GetNumberValue())
	})
}

func TestProcessor_ProcessRequestBody(t *testing.T) {
	t.Run("body parser error", func(t *testing.T) {
		rbp := mockRequestBodyParser{t: t, retErr: errors.New("test error")}
		p := &Processor{
			config:  &processorConfig{bodyParser: rbp.impl},
			metrics: metrics.New(),
		}
		_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "failed to parse request body: test error")
	})
	t.Run("router error", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rbp := mockRequestBodyParser{t: t, retModelName: "some-model", expPath: "/foo"}
		rt := mockRouter{t: t, expHeaders: headers, retErr: errors.New("test error")}
		p := &Processor{
			config: &processorConfig{
				bodyParser: rbp.impl,
				router:     rt,
			},
			requestHeaders: headers,
			logger:         slog.Default(),
			metrics:        metrics.New(),
		}
		_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "failed to calculate route: test error")
	})
	t.Run("translator not found", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rbp := mockRequestBodyParser{t: t, retModelName: "some-model", expPath: "/foo"}
		rt := mockRouter{
			t: t, expHeaders: headers,
			retBackendName: "some-backend",
			retVersionedAPISchema: filterapi.VersionedAPISchema{
				Name:    "some-schema",
				Version: "v10.0",
			},
		}
		p := &Processor{
			config: &processorConfig{
				bodyParser: rbp.impl,
				router:     rt,
				factories:  make(map[filterapi.VersionedAPISchema]translator.Factory),
			},
			requestHeaders: headers,
			logger:         slog.Default(),
			metrics:        metrics.New(),
		}
		_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "failed to find factory for output schema {\"some-schema\" \"v10.0\"}")
	})
	t.Run("translator factory error", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rbp := mockRequestBodyParser{t: t, retModelName: "some-model", expPath: "/foo"}
		rt := mockRouter{
			t: t, expHeaders: headers,
			retBackendName: "some-backend",
			retVersionedAPISchema: filterapi.VersionedAPISchema{
				Name:    "some-schema",
				Version: "v10.0",
			},
		}
		factory := mockTranslatorFactory{t: t, retErr: errors.New("test error"), expPath: "/foo"}
		p := &Processor{
			config: &processorConfig{
				bodyParser: rbp.impl,
				router:     rt,
				factories:  map[filterapi.VersionedAPISchema]translator.Factory{{Name: "some-schema", Version: "v10.0"}: factory.impl},
			},
			requestHeaders: headers,
			logger:         slog.Default(),
			metrics:        metrics.New(),
		}
		_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "failed to create translator: test error")
	})
	t.Run("translator error", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rbp := mockRequestBodyParser{t: t, retModelName: "some-model", expPath: "/foo"}
		rt := mockRouter{
			t: t, expHeaders: headers,
			retBackendName: "some-backend",
			retVersionedAPISchema: filterapi.VersionedAPISchema{
				Name:    "some-schema",
				Version: "v10.0",
			},
		}
		factory := mockTranslatorFactory{t: t, retTranslator: mockTranslator{t: t, retErr: errors.New("test error")}, expPath: "/foo"}
		p := &Processor{
			config: &processorConfig{
				bodyParser: rbp.impl,
				router:     rt,
				factories:  map[filterapi.VersionedAPISchema]translator.Factory{{Name: "some-schema", Version: "v10.0"}: factory.impl},
			},
			requestHeaders: headers,
			logger:         slog.Default(),
			metrics:        metrics.New(),
		}
		_, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "failed to transform request: test error")
	})
	t.Run("ok", func(t *testing.T) {
		someBody := router.RequestBody("foooooooooooooo")
		headers := map[string]string{":path": "/foo"}
		rbp := mockRequestBodyParser{t: t, retModelName: "some-model", expPath: "/foo", retRb: someBody}
		rt := mockRouter{
			t: t, expHeaders: headers,
			retBackendName: "some-backend",
			retVersionedAPISchema: filterapi.VersionedAPISchema{
				Name:    "some-schema",
				Version: "v10.0",
			},
		}
		headerMut := &extprocv3.HeaderMutation{}
		bodyMut := &extprocv3.BodyMutation{}
		mt := mockTranslator{t: t, expRequestBody: someBody, retHeaderMutation: headerMut, retBodyMutation: bodyMut}
		factory := mockTranslatorFactory{t: t, retTranslator: mt, expPath: "/foo"}
		p := &Processor{
			config: &processorConfig{
				bodyParser:               rbp.impl,
				router:                   rt,
				factories:                map[filterapi.VersionedAPISchema]translator.Factory{{Name: "some-schema", Version: "v10.0"}: factory.impl},
				selectedBackendHeaderKey: "x-ai-gateway-backend-key",
				modelNameHeaderKey:       "x-ai-gateway-model-key",
			},
			requestHeaders: headers,
			logger:         slog.Default(),
			metrics:        metrics.New(),
		}
		resp, err := p.ProcessRequestBody(context.Background(), &extprocv3.HttpBody{})
		require.NoError(t, err)
		require.Equal(t, mt, p.translator)
		require.NotNil(t, resp)
		commonRes := resp.Response.(*extprocv3.ProcessingResponse_RequestBody).RequestBody.Response
		require.Equal(t, headerMut, commonRes.HeaderMutation)
		require.Equal(t, bodyMut, commonRes.BodyMutation)

		// Check the model and backend headers are set in headerMut.
		hdrs := headerMut.SetHeaders
		require.Len(t, hdrs, 2)
		require.Equal(t, "x-ai-gateway-model-key", hdrs[0].Header.Key)
		require.Equal(t, "some-model", string(hdrs[0].Header.RawValue))
		require.Equal(t, "x-ai-gateway-backend-key", hdrs[1].Header.Key)
		require.Equal(t, "some-backend", string(hdrs[1].Header.RawValue))
	})
}

func Test_headersToMap(t *testing.T) {
	hm := &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{
			{Key: "foo", Value: "bar"},
			{Key: "dog", RawValue: []byte("cat")},
		},
	}
	m := headersToMap(hm)
	require.Equal(t, map[string]string{"foo": "bar", "dog": "cat"}, m)
}
