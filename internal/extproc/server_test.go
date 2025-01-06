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

	"github.com/envoyproxy/ai-gateway/extprocconfig"
)

func requireNewServerWithMockProcessor(t *testing.T) *Server[*mockProcessor] {
	s, err := NewServer[*mockProcessor](slog.Default(), newMockProcessor)
	require.NoError(t, err)
	require.NotNil(t, s)
	return s
}

func TestServer_LoadConfig(t *testing.T) {
	t.Run("invalid input schema", func(t *testing.T) {
		s := requireNewServerWithMockProcessor(t)
		err := s.LoadConfig(&extprocconfig.Config{
			InputSchema: extprocconfig.VersionedAPISchema{Schema: "some-invalid-schema"},
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "cannot create request body parser")
	})
	t.Run("ok", func(t *testing.T) {
		config := &extprocconfig.Config{
			TokenUsageMetadata:      &extprocconfig.TokenUsageMetadata{Namespace: "ns", Key: "key"},
			InputSchema:             extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaOpenAI},
			BackendRoutingHeaderKey: "x-backend-name",
			ModelNameHeaderKey:      "x-model-name",
			Rules: []extprocconfig.RouteRule{
				{
					Backends: []extprocconfig.Backend{
						{Name: "kserve", OutputSchema: extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaOpenAI}},
						{Name: "awsbedrock", OutputSchema: extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaAWSBedrock}},
					},
					Headers: []extprocconfig.HeaderMatch{
						{
							Name:  "x-model-name",
							Value: "llama3.3333",
						},
					},
				},
				{
					Backends: []extprocconfig.Backend{
						{Name: "openai", OutputSchema: extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaOpenAI}},
					},
					Headers: []extprocconfig.HeaderMatch{
						{
							Name:  "x-model-name",
							Value: "gpt4.4444",
						},
					},
				},
			},
		}
		s := requireNewServerWithMockProcessor(t)
		err := s.LoadConfig(config)
		require.NoError(t, err)

		require.NotNil(t, s.config)
		require.NotNil(t, s.config.tokenUsageMetadata)
		require.Equal(t, "ns", s.config.tokenUsageMetadata.Namespace)
		require.Equal(t, "key", s.config.tokenUsageMetadata.Key)
		require.NotNil(t, s.config.router)
		require.NotNil(t, s.config.bodyParser)
		require.Equal(t, "x-backend-name", s.config.backendRoutingHeaderKey)
		require.Equal(t, "x-model-name", s.config.ModelNameHeaderKey)
		require.Len(t, s.config.factories, 2)
		require.NotNil(t, s.config.factories[extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaOpenAI}])
		require.NotNil(t, s.config.factories[extprocconfig.VersionedAPISchema{Schema: extprocconfig.APISchemaAWSBedrock}])
	})
}

func TestServer_Check(t *testing.T) {
	s := requireNewServerWithMockProcessor(t)

	res, err := s.Check(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, res.Status)
}

func TestServer_Watch(t *testing.T) {
	s := requireNewServerWithMockProcessor(t)

	err := s.Watch(nil, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "Watch is not implemented")
}

func TestServer_processMsg(t *testing.T) {
	t.Run("unknown request type", func(t *testing.T) {
		s := requireNewServerWithMockProcessor(t)
		p := s.newProcessor(nil)
		_, err := s.processMsg(context.Background(), p, &extprocv3.ProcessingRequest{})
		require.ErrorContains(t, err, "unknown request type")
	})
	t.Run("request headers", func(t *testing.T) {
		s := requireNewServerWithMockProcessor(t)
		p := s.newProcessor(nil)

		hm := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}}}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{}}
		p.t = t
		p.expHeaderMap = hm
		p.retProcessingResponse = expResponse
		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{Headers: hm}},
		}
		resp, err := s.processMsg(context.Background(), p, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, expResponse, resp)
	})
	t.Run("request body", func(t *testing.T) {
		s := requireNewServerWithMockProcessor(t)
		p := s.newProcessor(nil)

		reqBody := &extprocv3.HttpBody{}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestBody{}}
		p.t = t
		p.expBody = reqBody
		p.retProcessingResponse = expResponse
		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestBody{RequestBody: reqBody},
		}
		resp, err := s.processMsg(context.Background(), p, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, expResponse, resp)
	})
	t.Run("response headers", func(t *testing.T) {
		s := requireNewServerWithMockProcessor(t)
		p := s.newProcessor(nil)

		hm := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}}}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{}}
		p.t = t
		p.expHeaderMap = hm
		p.retProcessingResponse = expResponse
		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_ResponseHeaders{ResponseHeaders: &extprocv3.HttpHeaders{Headers: hm}},
		}
		resp, err := s.processMsg(context.Background(), p, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, expResponse, resp)
	})
	t.Run("response body", func(t *testing.T) {
		s := requireNewServerWithMockProcessor(t)
		p := s.newProcessor(nil)

		reqBody := &extprocv3.HttpBody{}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseBody{}}
		p.t = t
		p.expBody = reqBody
		p.retProcessingResponse = expResponse
		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_ResponseBody{ResponseBody: reqBody},
		}
		resp, err := s.processMsg(context.Background(), p, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, expResponse, resp)
	})
}

func TestServer_Process(t *testing.T) {
	t.Run("context done", func(t *testing.T) {
		s := requireNewServerWithMockProcessor(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		ms := &mockExternalProcessingStream{t: t, ctx: ctx}
		err := s.Process(ms)
		require.ErrorContains(t, err, "context canceled")
	})
	t.Run("recv iof", func(t *testing.T) {
		s := requireNewServerWithMockProcessor(t)
		ms := &mockExternalProcessingStream{t: t, retErr: io.EOF, ctx: context.Background()}
		err := s.Process(ms)
		require.NoError(t, err)
	})
	t.Run("recv canceled", func(t *testing.T) {
		s := requireNewServerWithMockProcessor(t)
		ms := &mockExternalProcessingStream{t: t, retErr: status.Error(codes.Canceled, "someerror"), ctx: context.Background()}
		err := s.Process(ms)
		require.NoError(t, err)
	})
	t.Run("recv generic error", func(t *testing.T) {
		s := requireNewServerWithMockProcessor(t)
		ms := &mockExternalProcessingStream{t: t, retErr: errors.New("some error"), ctx: context.Background()}
		err := s.Process(ms)
		require.ErrorContains(t, err, "some error")
	})

	t.Run("ok", func(t *testing.T) {
		s := requireNewServerWithMockProcessor(t)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		p := s.newProcessor(nil)
		hm := &corev3.HeaderMap{Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}}}
		expResponse := &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_RequestHeaders{}}
		p.t = t
		p.expHeaderMap = hm
		p.retProcessingResponse = expResponse

		req := &extprocv3.ProcessingRequest{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{Headers: hm}},
		}
		ms := &mockExternalProcessingStream{t: t, ctx: ctx, retRecv: req, expResponseOnSend: expResponse}
		err := s.process(p, ms)
		require.Error(t, err, "context canceled")
	})
}
