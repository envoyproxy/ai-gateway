// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func requireNewServerWithMockProcessor(t *testing.T) (*Server, *mockProcessor) {
	s, err := NewServer(slog.Default())
	require.NoError(t, err)
	require.NotNil(t, s)
	s.config = &filterapi.RuntimeConfig{}

	m := newMockProcessor(s.config, s.logger)
	s.Register("/", func(*filterapi.RuntimeConfig, map[string]string, *slog.Logger, bool) (Processor, error) {
		return m, nil
	})

	return s, m.(*mockProcessor)
}

func TestServer_LoadConfig(t *testing.T) {
	config := &filterapi.Config{}
	s := &Server{}
	err := s.LoadConfig(t.Context(), config)
	require.NoError(t, err)
	require.NotNil(t, s.config)
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

func TestServer_List(t *testing.T) {
	s, _ := requireNewServerWithMockProcessor(t)

	res, err := s.List(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, res.Statuses["extproc"].Status)
}

func TestServer_processMsg(t *testing.T) {
	t.Run("unknown request type", func(t *testing.T) {
		s, p := requireNewServerWithMockProcessor(t)
		_, err := s.processMsg(t.Context(), slog.Default(), p, &extprocv3.ProcessingRequest{}, "test-req-id", false)
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
		resp, err := s.processMsg(t.Context(), slog.Default(), p, req, "test-req-id", false)
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
		resp, err := s.processMsg(t.Context(), slog.Default(), p, req, "test-req-id", false)
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
		resp, err := s.processMsg(t.Context(), slog.Default(), p, req, "test-req-id", false)
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
		resp, err := s.processMsg(t.Context(), slog.Default(), p, req, "test-req-id", false)
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
		resp, err := s.processMsg(t.Context(), slog.Default(), p, req, "test-req-id", false)
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
	t.Run("upstream filter", func(t *testing.T) {
		s, p := requireNewServerWithMockProcessor(t)

		hm := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: originalPathHeader, Value: "/"}, {Key: internalReqIDHeader, Value: "test-req-id-123"}, {Key: "foo", Value: "bar"}}}
		p.t = t
		p.expHeaderMap = hm
		req := &extprocv3.ProcessingRequest{
			Attributes: map[string]*structpb.Struct{
				"envoy.filters.http.ext_proc": {Fields: map[string]*structpb.Value{"something": {}}},
			},
			Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{Headers: hm}},
		}
		ms := &mockExternalProcessingStream{t: t, ctx: t.Context(), retRecv: req}
		err := s.Process(ms)
		require.ErrorContains(t, err, `missing backend name in attributes at path: xds.upstream_host_metadata.filter_metadata['aigateway.envoy.io']['per_route_rule_backend_name']`)
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

func TestServer_setBackend(t *testing.T) {
	s, _ := requireNewServerWithMockProcessor(t)
	s.config.Backends = map[string]*filterapi.RuntimeBackend{"openai": {Backend: &filterapi.Backend{Name: "openai"}}}
	mockProc := &mockProcessor{}

	for _, isEndpointPicker := range []bool{true, false} {
		t.Run(fmt.Sprintf("isEndpointPicker=%t", isEndpointPicker), func(t *testing.T) {
			attributeKey := internalapi.XDSUpstreamHostMetadataBackendNamePath
			if isEndpointPicker {
				attributeKey = internalapi.XDSClusterMetadataBackendNamePath
			}

			err := s.setBackend(t.Context(), mockProc, "aaaaaaaaaaaa", isEndpointPicker, &extprocv3.ProcessingRequest{
				Attributes: map[string]*structpb.Struct{
					"envoy.filters.http.ext_proc": {Fields: map[string]*structpb.Value{
						attributeKey: {Kind: &structpb.Value_StringValue{
							StringValue: "openai",
						}},
					}},
				},
				Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}},
			})
			// Should be an error as there is no router processor registered for openai backend.
			// The purpose of this test is to verify that the backend name is correctly extracted.
			require.ErrorContains(t, err, `no router processor found, request_id=aaaaaaaaaaaa, backend=openai`)
		})
	}
}

func TestServer_ProcessorSelection(t *testing.T) {
	s, err := NewServer(slog.Default())
	require.NoError(t, err)
	require.NotNil(t, s)

	s.config = &filterapi.RuntimeConfig{}
	s.Register("/one", func(*filterapi.RuntimeConfig, map[string]string, *slog.Logger, bool) (Processor, error) {
		// Returning nil guarantees that the test will fail if this processor is selected.
		return nil, nil
	})
	s.Register("/two", func(*filterapi.RuntimeConfig, map[string]string, *slog.Logger, bool) (Processor, error) {
		return &mockProcessor{
			t:                     t,
			expHeaderMap:          &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: ":path", Value: "/two"}, {Key: "x-request-id", Value: "original-req-id"}}},
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
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ImmediateResponse{
			ImmediateResponse: &extprocv3.ImmediateResponse{
				Status:     &typev3.HttpStatus{Code: typev3.StatusCode_NotFound},
				Body:       []byte("unsupported path: /unknown"),
				GrpcStatus: &extprocv3.GrpcStatus{Status: uint32(codes.NotFound)},
			},
		}}
		ms := &mockExternalProcessingStream{t: t, ctx: ctx, retRecv: req, expResponseOnSend: expResponse}

		err = s.Process(ms)
		require.Equal(t, codes.NotFound, status.Convert(err).Code())
		require.ErrorContains(t, err, "unsupported path: /unknown")
	})

	t.Run("known path", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{
				RequestHeaders: &extprocv3.HttpHeaders{
					Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
						{Key: ":path", Value: "/two"},
						{Key: "x-request-id", Value: "original-req-id"},
					}},
				},
			},
		}
		expResponse := &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestHeaders{
				RequestHeaders: &extprocv3.HeadersResponse{
					Response: &extprocv3.CommonResponse{
						HeaderMutation: &extprocv3.HeaderMutation{
							SetHeaders: []*corev3.HeaderValueOption{
								{
									Header: &corev3.HeaderValue{
										Key:      internalReqIDHeader,
										RawValue: []byte("original-req-id-test-internal-req-id"),
									},
								},
							},
						},
					},
				},
			},
		}

		s.uuidFn = func() string {
			return "test-internal-req-id"
		}

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
	buf := internaltesting.CaptureOutput("test")[0]
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
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
	filtered := filterSensitiveRequestBodyForLogging(resp, logger, []string{"authorization"})
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

	t.Run("do nothing for immediate response", func(t *testing.T) {
		resp := &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ImmediateResponse{
				ImmediateResponse: &extprocv3.ImmediateResponse{},
			},
		}
		filtered := filterSensitiveRequestBodyForLogging(resp, logger, []string{"authorization"})
		require.NotNil(t, filtered)
		require.Equal(t, resp, filtered)
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

func TestServer_ProcessorForPath_QueryParameterStripping(t *testing.T) {
	s, err := NewServer(slog.Default())
	require.NoError(t, err)
	require.NotNil(t, s)

	s.config = &filterapi.RuntimeConfig{}

	// Register processors for different base paths.
	mockProc := &mockProcessor{}
	s.Register("/v1/messages", func(*filterapi.RuntimeConfig, map[string]string, *slog.Logger, bool) (Processor, error) {
		return mockProc, nil
	})
	s.Register("/anthropic/v1/messages", func(*filterapi.RuntimeConfig, map[string]string, *slog.Logger, bool) (Processor, error) {
		return mockProc, nil
	})

	tests := []struct {
		name           string
		requestHeaders map[string]string
		isUpstream     bool
		expectSuccess  bool
		expectedError  string
	}{
		{
			name: "path_without_query_params",
			requestHeaders: map[string]string{
				":path": "/v1/messages",
			},
			isUpstream:    false,
			expectSuccess: true,
		},
		{
			name: "path_with_beta_query_param",
			requestHeaders: map[string]string{
				":path": "/v1/messages?beta=true",
			},
			isUpstream:    false,
			expectSuccess: true,
		},
		{
			name: "path_with_multiple_query_params",
			requestHeaders: map[string]string{
				":path": "/v1/messages?beta=true&stream=false&version=2",
			},
			isUpstream:    false,
			expectSuccess: true,
		},
		{
			name: "anthropic_path_with_beta_param",
			requestHeaders: map[string]string{
				":path": "/anthropic/v1/messages?beta=true",
			},
			isUpstream:    false,
			expectSuccess: true,
		},
		{
			name: "unknown_path_without_query_params",
			requestHeaders: map[string]string{
				":path": "/unknown/path",
			},
			isUpstream:    false,
			expectSuccess: false,
			expectedError: "no processor registered for the given path: /unknown/path",
		},
		{
			name: "unknown_path_with_query_params",
			requestHeaders: map[string]string{
				":path": "/unknown/path?param=value",
			},
			isUpstream:    false,
			expectSuccess: false,
			expectedError: "no processor registered for the given path: /unknown/path",
		},
		{
			name: "upstream_filter_with_original_path_header",
			requestHeaders: map[string]string{
				originalPathHeader: "/v1/messages?beta=true&other=param",
			},
			isUpstream:    true,
			expectSuccess: true,
		},
		{
			name: "empty_path",
			requestHeaders: map[string]string{
				":path": "",
			},
			isUpstream:    false,
			expectSuccess: false,
			expectedError: "no processor registered for the given path: ",
		},
		{
			name: "path_with_only_query_params",
			requestHeaders: map[string]string{
				":path": "?beta=true",
			},
			isUpstream:    false,
			expectSuccess: false,
			expectedError: "no processor registered for the given path: ",
		},
		{
			name: "path_with_fragment_and_query",
			requestHeaders: map[string]string{
				":path": "/v1/messages?beta=true#fragment",
			},
			isUpstream:    false,
			expectSuccess: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor, err := s.processorForPath(tt.requestHeaders, tt.isUpstream)

			if tt.expectSuccess {
				require.NoError(t, err)
				require.NotNil(t, processor)
				require.Equal(t, mockProc, processor)
			} else {
				require.Error(t, err)
				require.Nil(t, processor)
				if tt.expectedError != "" {
					require.Contains(t, err.Error(), tt.expectedError)
				}
			}
		})
	}
}
