package extproc

import (
	"context"
	"errors"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
)

func TestProcessor_processRequestHeaders(t *testing.T) {
	p := &processor{}
	res, err := p.processRequestHeaders(context.Background(), &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}},
	})
	require.NoError(t, err)
	_, ok := res.Response.(*extprocv3.ProcessingResponse_RequestHeaders)
	require.True(t, ok)
	require.Equal(t, map[string]string{"foo": "bar"}, p.requestHeaders)
}

func TestProcessor_processResponseHeaders(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mt := &mockTranslator{t: t, expHeaders: make(map[string]string)}
		p := &processor{translator: mt}
		mt.retErr = errors.New("test error")
		_, err := p.processResponseHeaders(context.Background(), nil)
		require.ErrorContains(t, err, "test error")
	})
	t.Run("ok", func(t *testing.T) {
		inHeaders := &corev3.HeaderMap{
			Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}, {Key: "dog", RawValue: []byte("cat")}},
		}
		expHeaders := map[string]string{"foo": "bar", "dog": "cat"}
		mt := &mockTranslator{t: t, expHeaders: expHeaders}
		p := &processor{translator: mt}
		res, err := p.processResponseHeaders(context.Background(), inHeaders)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseHeaders).ResponseHeaders.Response
		require.Equal(t, mt.retHeaderMutation, commonRes.HeaderMutation)
	})
}

func TestProcessor_processResponseBody(t *testing.T) {
	t.Run("error translation", func(t *testing.T) {
		mt := &mockTranslator{t: t}
		p := &processor{translator: mt}
		mt.retErr = errors.New("test error")
		_, err := p.processResponseBody(context.Background(), nil)
		require.ErrorContains(t, err, "test error")
	})
	t.Run("ok", func(t *testing.T) {
		inBody := &extprocv3.HttpBody{}
		expBodyMut := &extprocv3.BodyMutation{}
		expHeadMut := &extprocv3.HeaderMutation{}
		mt := &mockTranslator{t: t, expResponseBody: inBody, retBodyMutation: expBodyMut, retHeaderMutation: expHeadMut}
		p := &processor{translator: mt}
		res, err := p.processResponseBody(context.Background(), inBody)
		require.NoError(t, err)
		commonRes := res.Response.(*extprocv3.ProcessingResponse_ResponseBody).ResponseBody.Response
		require.Equal(t, expBodyMut, commonRes.BodyMutation)
		require.Equal(t, expHeadMut, commonRes.HeaderMutation)
	})
}

func TestProcessor_processRequestBody(t *testing.T) {
	t.Run("body parser error", func(t *testing.T) {
		rbp := mockRequestBodyParser{t: t, retErr: errors.New("test error")}
		p := &processor{config: &processorConfig{bodyParser: rbp.impl}}
		_, err := p.processRequestBody(context.Background(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "failed to parse request body: test error")
	})
	t.Run("router error", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rbp := mockRequestBodyParser{t: t, retModelName: "some-model", expPath: "/foo"}
		rt := mockRouter{t: t, expHeaders: headers, retErr: errors.New("test error")}
		p := &processor{config: &processorConfig{bodyParser: rbp.impl, router: rt}, requestHeaders: headers}
		_, err := p.processRequestBody(context.Background(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "failed to calculate route: test error")
	})
	t.Run("translator not found", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rbp := mockRequestBodyParser{t: t, retModelName: "some-model", expPath: "/foo"}
		rt := mockRouter{
			t: t, expHeaders: headers, retBackendName: "some-backend",
			retVersionedAPISchema: extprocconfig.VersionedAPISchema{Schema: "some-schema", Version: "v10.0"},
		}
		p := &processor{config: &processorConfig{
			bodyParser: rbp.impl, router: rt,
			factories: make(map[extprocconfig.VersionedAPISchema]translator.Factory),
		}, requestHeaders: headers}
		_, err := p.processRequestBody(context.Background(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "failed to find factory for output schema {\"some-schema\" \"v10.0\"}")
	})
	t.Run("translator factory error", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rbp := mockRequestBodyParser{t: t, retModelName: "some-model", expPath: "/foo"}
		rt := mockRouter{
			t: t, expHeaders: headers, retBackendName: "some-backend",
			retVersionedAPISchema: extprocconfig.VersionedAPISchema{Schema: "some-schema", Version: "v10.0"},
		}
		factory := mockTranslatorFactory{t: t, retErr: errors.New("test error"), expPath: "/foo"}
		p := &processor{config: &processorConfig{
			bodyParser: rbp.impl, router: rt,
			factories: map[extprocconfig.VersionedAPISchema]translator.Factory{
				{Schema: "some-schema", Version: "v10.0"}: factory.impl,
			},
		}, requestHeaders: headers}
		_, err := p.processRequestBody(context.Background(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "failed to create translator: test error")
	})
	t.Run("translator error", func(t *testing.T) {
		headers := map[string]string{":path": "/foo"}
		rbp := mockRequestBodyParser{t: t, retModelName: "some-model", expPath: "/foo"}
		rt := mockRouter{
			t: t, expHeaders: headers, retBackendName: "some-backend",
			retVersionedAPISchema: extprocconfig.VersionedAPISchema{Schema: "some-schema", Version: "v10.0"},
		}
		factory := mockTranslatorFactory{t: t, retTranslator: mockTranslator{t: t, retErr: errors.New("test error")}, expPath: "/foo"}
		p := &processor{config: &processorConfig{
			bodyParser: rbp.impl, router: rt,
			factories: map[extprocconfig.VersionedAPISchema]translator.Factory{
				{Schema: "some-schema", Version: "v10.0"}: factory.impl,
			},
		}, requestHeaders: headers}
		_, err := p.processRequestBody(context.Background(), &extprocv3.HttpBody{})
		require.ErrorContains(t, err, "failed to transform request: test error")
	})
	t.Run("ok", func(t *testing.T) {
		someBody := router.RequestBody("foooooooooooooo")
		headers := map[string]string{":path": "/foo"}
		rbp := mockRequestBodyParser{t: t, retModelName: "some-model", expPath: "/foo", retRb: someBody}
		rt := mockRouter{
			t: t, expHeaders: headers, retBackendName: "some-backend",
			retVersionedAPISchema: extprocconfig.VersionedAPISchema{Schema: "some-schema", Version: "v10.0"},
		}
		headerMut := &extprocv3.HeaderMutation{}
		bodyMut := &extprocv3.BodyMutation{}
		mt := mockTranslator{t: t, expRequestBody: someBody, retHeaderMutation: headerMut, retBodyMutation: bodyMut}
		factory := mockTranslatorFactory{t: t, retTranslator: mt, expPath: "/foo"}
		p := &processor{config: &processorConfig{
			bodyParser: rbp.impl, router: rt,
			factories: map[extprocconfig.VersionedAPISchema]translator.Factory{
				{Schema: "some-schema", Version: "v10.0"}: factory.impl,
			},
			backendRoutingHeaderKey: "x-ai-gateway-backend-key",
			ModelNameHeaderKey:      "x-ai-gateway-model-key",
		}, requestHeaders: headers}
		resp, err := p.processRequestBody(context.Background(), &extprocv3.HttpBody{})
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
