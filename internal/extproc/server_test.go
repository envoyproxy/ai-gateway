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
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

func requireNewServerWithMockProcessor(t *testing.T) (*Server, *mockProcessor) {
	s, err := NewServer(slog.Default())
	require.NoError(t, err)
	require.NotNil(t, s)
	s.config = &processorConfig{}

	m := newMockProcessor(s.config, s.logger)
	s.Register("/", func(*processorConfig, map[string]string, *slog.Logger) (Processor, error) { return m, nil })

	return s, m.(*mockProcessor)
}

func TestServer_LoadConfig(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		config := &filterapi.Config{
			MetadataNamespace: "ns",
			LLMRequestCosts: []filterapi.LLMRequestCost{
				{MetadataKey: "key", Type: filterapi.LLMRequestCostTypeOutputToken},
				{MetadataKey: "cel_key", Type: filterapi.LLMRequestCostTypeCEL, CEL: "1 + 1"},
			},
			Schema:                   filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			SelectedBackendHeaderKey: "x-ai-eg-selected-backend",
			ModelNameHeaderKey:       "x-model-name",
			Rules: []filterapi.RouteRule{
				{
					Backends: []filterapi.Backend{
						{Name: "kserve", Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}},
						{Name: "awsbedrock", Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}},
					},
					Headers: []filterapi.HeaderMatch{
						{
							Name:  "x-model-name",
							Value: "llama3.3333",
						},
					},
				},
				{
					Backends: []filterapi.Backend{
						{Name: "openai", Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}},
					},
					Headers: []filterapi.HeaderMatch{
						{
							Name:  "x-model-name",
							Value: "gpt4.4444",
						},
					},
				},
			},
		}
		s, _ := requireNewServerWithMockProcessor(t)
		err := s.LoadConfig(t.Context(), config)
		require.NoError(t, err)

		require.NotNil(t, s.config)
		require.Equal(t, "ns", s.config.metadataNamespace)
		require.NotNil(t, s.config.router)
		require.Equal(t, s.config.schema, config.Schema)
		require.Equal(t, "x-ai-eg-selected-backend", s.config.selectedBackendHeaderKey)
		require.Equal(t, "x-model-name", s.config.modelNameHeaderKey)

		require.Len(t, s.config.requestCosts, 2)
		require.Equal(t, filterapi.LLMRequestCostTypeOutputToken, s.config.requestCosts[0].Type)
		require.Equal(t, "key", s.config.requestCosts[0].MetadataKey)
		require.Equal(t, filterapi.LLMRequestCostTypeCEL, s.config.requestCosts[1].Type)
		require.Equal(t, "1 + 1", s.config.requestCosts[1].CEL)
		prog := s.config.requestCosts[1].celProg
		require.NotNil(t, prog)
		val, err := llmcostcel.EvaluateProgram(prog, "", "", 1, 1, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(2), val)
	})
}

func TestServer_Check(t *testing.T) {
	s, _ := requireNewServerWithMockProcessor(t)

	res, err := s.Check(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, res.Status)
}

func TestServer_Watch(t *testing.T) {
	s, _ := requireNewServerWithMockProcessor(t)

	err := s.Watch(nil, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "Watch is not implemented")
}

func TestServer_processMsg(t *testing.T) {
	t.Run("unknown request type", func(t *testing.T) {
		s, p := requireNewServerWithMockProcessor(t)
		_, err := s.processMsg(t.Context(), p, &extprocv3.ProcessingRequest{})
		require.ErrorContains(t, err, "unknown request type")
	})
	t.Run("request headers", func(t *testing.T) {
		s, p := requireNewServerWithMockProcessor(t)

		hm := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}}}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{}}
		p.t = t
		p.expHeaderMap = hm
		p.retProcessingResponse = expResponse
		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{Headers: hm}},
		}
		resp, err := s.processMsg(t.Context(), p, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, expResponse, resp)
	})
	t.Run("request body", func(t *testing.T) {
		s, p := requireNewServerWithMockProcessor(t)

		reqBody := &extprocv3.HttpBody{}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestBody{}}
		p.t = t
		p.expBody = reqBody
		p.retProcessingResponse = expResponse
		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestBody{RequestBody: reqBody},
		}
		resp, err := s.processMsg(t.Context(), p, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, expResponse, resp)
	})
	t.Run("response headers", func(t *testing.T) {
		s, p := requireNewServerWithMockProcessor(t)

		hm := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}}}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{}}
		p.t = t
		p.expHeaderMap = hm
		p.retProcessingResponse = expResponse
		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_ResponseHeaders{ResponseHeaders: &extprocv3.HttpHeaders{Headers: hm}},
		}
		resp, err := s.processMsg(t.Context(), p, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, expResponse, resp)
	})
	t.Run("error response headers", func(t *testing.T) {
		s, p := requireNewServerWithMockProcessor(t)

		hm := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: ":status", Value: "504"}}}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{}}
		p.t = t
		p.expHeaderMap = hm
		p.retProcessingResponse = expResponse
		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_ResponseHeaders{ResponseHeaders: &extprocv3.HttpHeaders{Headers: hm}},
		}
		resp, err := s.processMsg(t.Context(), p, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, expResponse, resp)
	})
	t.Run("response body", func(t *testing.T) {
		s, p := requireNewServerWithMockProcessor(t)

		reqBody := &extprocv3.HttpBody{}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseBody{}}
		p.t = t
		p.expBody = reqBody
		p.retProcessingResponse = expResponse
		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_ResponseBody{ResponseBody: reqBody},
		}
		resp, err := s.processMsg(t.Context(), p, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, expResponse, resp)
	})
}

func TestServer_Process(t *testing.T) {
	t.Run("context done", func(t *testing.T) {
		s, _ := requireNewServerWithMockProcessor(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		ms := &mockExternalProcessingStream{t: t, ctx: ctx}
		err := s.Process(ms)
		require.ErrorContains(t, err, "context canceled")
	})
	t.Run("recv iof", func(t *testing.T) {
		s, _ := requireNewServerWithMockProcessor(t)
		ms := &mockExternalProcessingStream{t: t, retErr: io.EOF, ctx: t.Context()}
		err := s.Process(ms)
		require.NoError(t, err)
	})
	t.Run("recv canceled", func(t *testing.T) {
		s, _ := requireNewServerWithMockProcessor(t)
		ms := &mockExternalProcessingStream{t: t, retErr: status.Error(codes.Canceled, "someerror"), ctx: t.Context()}
		err := s.Process(ms)
		require.NoError(t, err)
	})
	t.Run("recv generic error", func(t *testing.T) {
		s, _ := requireNewServerWithMockProcessor(t)
		ms := &mockExternalProcessingStream{t: t, retErr: errors.New("some error"), ctx: t.Context()}
		err := s.Process(ms)
		require.ErrorContains(t, err, "some error")
	})

	t.Run("ok", func(t *testing.T) {
		s, p := requireNewServerWithMockProcessor(t)

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		hm := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: ":path", Value: "/"}, {Key: "foo", Value: "bar"}}}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{}}
		p.t = t
		p.expHeaderMap = hm
		p.retProcessingResponse = expResponse

		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{Headers: hm}},
		}
		ms := &mockExternalProcessingStream{t: t, ctx: ctx, retRecv: req, expResponseOnSend: expResponse}
		err := s.Process(ms)
		require.ErrorContains(t, err, "context deadline exceeded")
	})
	t.Run("without going through request headers phase", func(t *testing.T) {
		// This is a regression test as in #419.
		s, _ := requireNewServerWithMockProcessor(t)
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{}}
		req := &extprocv3.ProcessingRequest{Request: &extprocv3.ProcessingRequest_ResponseHeaders{
			ResponseHeaders: &extprocv3.HttpHeaders{Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: ":status", Value: "403"}}}},
		}}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		ms := &mockExternalProcessingStream{t: t, ctx: ctx, retRecv: req, expResponseOnSend: expResponse}
		err := s.Process(ms)
		require.ErrorContains(t, err, "context deadline exceeded")
	})
}

func TestServer_ProcessorSelection(t *testing.T) {
	s, err := NewServer(slog.Default())
	require.NoError(t, err)
	require.NotNil(t, s)

	s.config = &processorConfig{}
	s.Register("/one", func(*processorConfig, map[string]string, *slog.Logger) (Processor, error) {
		// Returning nil guarantees that the test will fail if this processor is selected
		return nil, nil
	})
	s.Register("/two", func(*processorConfig, map[string]string, *slog.Logger) (Processor, error) {
		return &mockProcessor{
			t:                     t,
			expHeaderMap:          &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: ":path", Value: "/two"}}},
			retProcessingResponse: &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{}},
		}, nil
	})

	t.Run("unknown path", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{
				RequestHeaders: &extprocv3.HttpHeaders{
					Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: ":path", Value: "/unknown"}}},
				},
			},
		}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{}}
		ms := &mockExternalProcessingStream{t: t, ctx: ctx, retRecv: req, expResponseOnSend: expResponse}

		err = s.Process(ms)
		require.Equal(t, codes.NotFound, status.Convert(err).Code())
		require.ErrorContains(t, err, "no processor defined for path: /unknown")
	})

	t.Run("known path", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{
				RequestHeaders: &extprocv3.HttpHeaders{
					Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: ":path", Value: "/two"}}},
				},
			},
		}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{}}
		ms := &mockExternalProcessingStream{t: t, ctx: ctx, retRecv: req, expResponseOnSend: expResponse}

		err = s.Process(ms)
		require.ErrorContains(t, err, "context deadline exceeded")
	})
}

func Test_filterSensitiveHeadersForLogging(t *testing.T) {
	hm := &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{
			{Key: "foo", Value: "bar"}, {Key: "dog", RawValue: []byte("cat")}, {Key: "authorization", Value: "sensitive"},
		},
	}
	filtered := filterSensitiveHeadersForLogging(hm, []string{"authorization"})
	require.Equal(t, []slog.Attr{
		slog.String("foo", "bar"),
		slog.String("dog", "cat"),
		slog.String("authorization", "[REDACTED]"),
	}, filtered)
	// Check original one should not be modified.
	require.Len(t, hm.Headers, 3)
	require.Contains(t, hm.Headers, &corev3.HeaderValue{Key: "foo", Value: "bar"})
	require.Contains(t, hm.Headers, &corev3.HeaderValue{Key: "dog", RawValue: []byte("cat")})
	require.Contains(t, hm.Headers, &corev3.HeaderValue{Key: "authorization", Value: "sensitive"})
}

func Test_filterSensitiveBodyForLogging(t *testing.T) {
	logger, buf := newTestLoggerWithBuffer()
	resp := &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: &extprocv3.HeaderMutation{
						SetHeaders: []*corev3.HeaderValueOption{
							{Header: &corev3.HeaderValue{
								Key:      ":path",
								RawValue: []byte("/model/some-random-model/converse"),
							}},
							{Header: &corev3.HeaderValue{
								Key:      "Authorization",
								RawValue: []byte("sensitive"),
							}},
						},
						RemoveHeaders: []string{"x-envoy-original-path"},
					},
					BodyMutation: &extprocv3.BodyMutation{},
				},
			},
		},
	}
	filtered := filterSensitiveBodyForLogging(resp, logger, []string{"authorization"})
	require.NotNil(t, filtered)
	filteredMutation := filtered.Response.(*extprocv3.ProcessingResponse_RequestBody).RequestBody.Response.GetHeaderMutation()
	require.Equal(t, []string{"x-envoy-original-path"}, filteredMutation.GetRemoveHeaders())
	require.Equal(t, []*corev3.HeaderValueOption{
		{Header: &corev3.HeaderValue{Key: ":path", RawValue: []byte("/model/some-random-model/converse")}},
		{Header: &corev3.HeaderValue{Key: "Authorization", RawValue: []byte("[REDACTED]")}},
	}, filteredMutation.GetSetHeaders())
	// Original one should not be modified, otherwise it will be an unexpected behavior.
	originalMutation := resp.Response.(*extprocv3.ProcessingResponse_RequestBody).RequestBody.Response.GetHeaderMutation()
	require.Equal(t, []string{"x-envoy-original-path"}, originalMutation.GetRemoveHeaders())
	require.Equal(t, []*corev3.HeaderValueOption{
		{Header: &corev3.HeaderValue{Key: ":path", RawValue: []byte("/model/some-random-model/converse")}},
		{Header: &corev3.HeaderValue{Key: "Authorization", RawValue: []byte("sensitive")}},
	}, originalMutation.GetSetHeaders())
	require.Contains(t, buf.String(), "filtering sensitive header")
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
