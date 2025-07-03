// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/metadata"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/extproc/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
)

var (
	_ Processor                                 = &mockProcessor{}
	_ translator.OpenAIChatCompletionTranslator = &mockTranslator{}
	_ translator.OpenAIEmbeddingTranslator      = &mockEmbeddingTranslator{}
	_ x.Router                                  = &mockRouter{}
)

func newMockProcessor(_ *processorConfig, _ *slog.Logger) Processor {
	return &mockProcessor{}
}

// mockProcessor implements [Processor] for testing.
type mockProcessor struct {
	t                     *testing.T
	expHeaderMap          *corev3.HeaderMap
	expBody               *extprocv3.HttpBody
	retProcessingResponse *extprocv3.ProcessingResponse
	retErr                error
}

// SetBackend implements [Processor.SetBackend].
func (m mockProcessor) SetBackend(context.Context, *filterapi.Backend, backendauth.Handler, Processor) error {
	return nil
}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
func (m mockProcessor) ProcessRequestHeaders(_ context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	require.Equal(m.t, m.expHeaderMap, headerMap)
	return m.retProcessingResponse, m.retErr
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (m mockProcessor) ProcessRequestBody(_ context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	require.Equal(m.t, m.expBody, body)
	return m.retProcessingResponse, m.retErr
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (m mockProcessor) ProcessResponseHeaders(_ context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	require.Equal(m.t, m.expHeaderMap, headerMap)
	return m.retProcessingResponse, m.retErr
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (m mockProcessor) ProcessResponseBody(_ context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	require.Equal(m.t, m.expBody, body)
	return m.retProcessingResponse, m.retErr
}

// mockTranslator implements [translator.Translator] for testing.
type mockTranslator struct {
	t                 *testing.T
	expHeaders        map[string]string
	expRequestBody    *openai.ChatCompletionRequest
	expResponseBody   *extprocv3.HttpBody
	retHeaderMutation *extprocv3.HeaderMutation
	retBodyMutation   *extprocv3.BodyMutation
	retUsedToken      translator.LLMTokenUsage
	retErr            error
}

// RequestBody implements [translator.OpenAIChatCompletionTranslator].
func (m mockTranslator) RequestBody(_ []byte, body *openai.ChatCompletionRequest, _ bool) (headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error) {
	require.Equal(m.t, m.expRequestBody, body)
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

// ResponseHeaders implements [translator.OpenAIChatCompletionTranslator].
func (m mockTranslator) ResponseHeaders(headers map[string]string) (headerMutation *extprocv3.HeaderMutation, err error) {
	require.Equal(m.t, m.expHeaders, headers)
	return m.retHeaderMutation, m.retErr
}

// ResponseError implements [translator.OpenAIChatCompletionTranslator].
func (m mockTranslator) ResponseError(_ map[string]string, body io.Reader) (headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error) {
	if m.expResponseBody != nil {
		buf, err := io.ReadAll(body)
		require.NoError(m.t, err)
		require.Equal(m.t, m.expResponseBody.Body, buf)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

// ResponseBody implements [translator.OpenAIChatCompletionTranslator].
func (m mockTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool) (headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage translator.LLMTokenUsage, err error) {
	if m.expResponseBody != nil {
		buf, err := io.ReadAll(body)
		require.NoError(m.t, err)
		require.Equal(m.t, m.expResponseBody.Body, buf)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retUsedToken, m.retErr
}

// mockRouter implements [router.Router] for testing.
type mockRouter struct {
	t            *testing.T
	expHeaders   map[string]string
	retRouteName string
	retErr       error
}

// Calculate implements [router.Router.Calculate].
func (m mockRouter) Calculate(headers map[string]string) (filterapi.RouteRuleName, error) {
	require.Equal(m.t, m.expHeaders, headers)
	return filterapi.RouteRuleName(m.retRouteName), m.retErr
}

// mockExternalProcessingStream implements [extprocv3.ExternalProcessor_ProcessServer] for testing.
type mockExternalProcessingStream struct {
	t                 *testing.T
	ctx               context.Context
	expResponseOnSend *extprocv3.ProcessingResponse
	retRecv           *extprocv3.ProcessingRequest
	retErr            error
}

// Context implements [extprocv3.ExternalProcessor_ProcessServer].
func (m mockExternalProcessingStream) Context() context.Context {
	return m.ctx
}

// Send implements [extprocv3.ExternalProcessor_ProcessServer].
func (m mockExternalProcessingStream) Send(response *extprocv3.ProcessingResponse) error {
	require.Equal(m.t, m.expResponseOnSend, response)
	return m.retErr
}

// Recv implements [extprocv3.ExternalProcessor_ProcessServer].
func (m mockExternalProcessingStream) Recv() (*extprocv3.ProcessingRequest, error) {
	return m.retRecv, m.retErr
}

// SetHeader implements [extprocv3.ExternalProcessor_ProcessServer].
func (m mockExternalProcessingStream) SetHeader(_ metadata.MD) error { panic("TODO") }

// SendHeader implements [extprocv3.ExternalProcessor_ProcessServer].
func (m mockExternalProcessingStream) SendHeader(metadata.MD) error { panic("TODO") }

// SetTrailer implements [extprocv3.ExternalProcessor_ProcessServer].
func (m mockExternalProcessingStream) SetTrailer(metadata.MD) { panic("TODO") }

// SendMsg implements [extprocv3.ExternalProcessor_ProcessServer].
func (m mockExternalProcessingStream) SendMsg(any) error { panic("TODO") }

// RecvMsg implements [extprocv3.ExternalProcessor_ProcessServer].
func (m mockExternalProcessingStream) RecvMsg(any) error { panic("TODO") }

var _ extprocv3.ExternalProcessor_ProcessServer = &mockExternalProcessingStream{}

// mockChatCompletionMetrics implements [metrics.ChatCompletion] for testing.
type mockChatCompletionMetrics struct {
	requestStart        time.Time
	model               string
	backend             string
	requestSuccessCount int
	requestErrorCount   int
	tokenUsageCount     int
	tokenLatencyCount   int
	timeToFirstToken    float64
	interTokenLatency   float64
}

// StartRequest implements [metrics.ChatCompletion].
func (m *mockChatCompletionMetrics) StartRequest(_ map[string]string) { m.requestStart = time.Now() }

// SetModel implements [metrics.ChatCompletion].
func (m *mockChatCompletionMetrics) SetModel(model string) { m.model = model }

// SetBackend implements [metrics.ChatCompletion].
func (m *mockChatCompletionMetrics) SetBackend(backend *filterapi.Backend) { m.backend = backend.Name }

// RecordTokenUsage implements [metrics.ChatCompletion].
func (m *mockChatCompletionMetrics) RecordTokenUsage(_ context.Context, _, _, _ uint32, _ ...attribute.KeyValue) {
	m.tokenUsageCount++
}

// RecordTokenLatency implements [metrics.ChatCompletion].
func (m *mockChatCompletionMetrics) RecordTokenLatency(_ context.Context, _ uint32, _ ...attribute.KeyValue) {
	m.tokenLatencyCount++
}

// GetTimeToFirstTokenMs implements [metrics.ChatCompletion].
func (m *mockChatCompletionMetrics) GetTimeToFirstTokenMs() float64 {
	m.timeToFirstToken = 1.0
	return m.timeToFirstToken * 1000
}

// GetInterTokenLatencyMs implements [metrics.ChatCompletion].
func (m *mockChatCompletionMetrics) GetInterTokenLatencyMs() float64 {
	m.interTokenLatency = 0.5
	return m.interTokenLatency * 1000
}

// RecordRequestCompletion implements [metrics.ChatCompletion].
func (m *mockChatCompletionMetrics) RecordRequestCompletion(_ context.Context, success bool, _ ...attribute.KeyValue) {
	if success {
		m.requestSuccessCount++
	} else {
		m.requestErrorCount++
	}
}

// RequireSelectedModel asserts the model and backend set on the metrics.
func (m *mockChatCompletionMetrics) RequireSelectedModel(t *testing.T, model string) {
	require.Equal(t, model, m.model)
}

// RequireModelAndBackendSet asserts the model and backend set on the metrics.
func (m *mockChatCompletionMetrics) RequireSelectedBackend(t *testing.T, backend string) {
	require.Equal(t, backend, m.backend)
}

// RequireRequestFailure asserts the request was marked as a failure.
func (m *mockChatCompletionMetrics) RequireRequestFailure(t *testing.T) {
	require.Equal(t, 0, m.requestSuccessCount)
	require.Equal(t, 1, m.requestErrorCount)
}

// RequireRequestNotCompleted asserts the request was not completed.
func (m *mockChatCompletionMetrics) RequireRequestNotCompleted(t *testing.T) {
	require.Equal(t, 0, m.requestSuccessCount)
	require.Equal(t, 0, m.requestErrorCount)
}

// RequireRequestSuccess asserts the request was marked as a success.
func (m *mockChatCompletionMetrics) RequireRequestSuccess(t *testing.T) {
	require.Equal(t, 1, m.requestSuccessCount)
	require.Equal(t, 0, m.requestErrorCount)
}

// RequireTokensRecorded asserts the number of tokens recorded.
func (m *mockChatCompletionMetrics) RequireTokensRecorded(t *testing.T, count int) {
	require.Equal(t, count, m.tokenUsageCount)
	require.Equal(t, count, m.tokenLatencyCount)
}

var _ x.ChatCompletionMetrics = &mockChatCompletionMetrics{}

// mockEmbeddingTranslator implements [translator.OpenAIEmbeddingTranslator] for testing.
type mockEmbeddingTranslator struct {
	t                 *testing.T
	expHeaders        map[string]string
	expRequestBody    *openai.EmbeddingRequest
	expResponseBody   *extprocv3.HttpBody
	retHeaderMutation *extprocv3.HeaderMutation
	retBodyMutation   *extprocv3.BodyMutation
	retUsedToken      translator.LLMTokenUsage
	retErr            error
}

// RequestBody implements [translator.OpenAIEmbeddingTranslator].
func (m mockEmbeddingTranslator) RequestBody(_ []byte, body *openai.EmbeddingRequest, _ bool) (headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error) {
	require.Equal(m.t, m.expRequestBody, body)
	return m.retHeaderMutation, m.retBodyMutation, m.retErr
}

// ResponseHeaders implements [translator.OpenAIEmbeddingTranslator].
func (m mockEmbeddingTranslator) ResponseHeaders(headers map[string]string) (headerMutation *extprocv3.HeaderMutation, err error) {
	require.Equal(m.t, m.expHeaders, headers)
	return m.retHeaderMutation, m.retErr
}

// ResponseBody implements [translator.OpenAIEmbeddingTranslator].
func (m mockEmbeddingTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool) (headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage translator.LLMTokenUsage, err error) {
	if m.expResponseBody != nil {
		buf, err := io.ReadAll(body)
		require.NoError(m.t, err)
		require.Equal(m.t, m.expResponseBody.Body, buf)
	}
	return m.retHeaderMutation, m.retBodyMutation, m.retUsedToken, m.retErr
}

// mockEmbeddingsMetrics implements [x.EmbeddingsMetrics] for testing.
type mockEmbeddingsMetrics struct {
	requestStart        time.Time
	model               string
	backend             string
	requestSuccessCount int
	requestErrorCount   int
	tokenUsageCount     int
}

// StartRequest implements [x.EmbeddingsMetrics].
func (m *mockEmbeddingsMetrics) StartRequest(_ map[string]string) { m.requestStart = time.Now() }

// SetModel implements [x.EmbeddingsMetrics].
func (m *mockEmbeddingsMetrics) SetModel(model string) { m.model = model }

// SetBackend implements [x.EmbeddingsMetrics].
func (m *mockEmbeddingsMetrics) SetBackend(backend *filterapi.Backend) { m.backend = backend.Name }

// RecordTokenUsage implements [x.EmbeddingsMetrics].
func (m *mockEmbeddingsMetrics) RecordTokenUsage(_ context.Context, _, _ uint32, _ ...attribute.KeyValue) {
	m.tokenUsageCount++
}

// RecordRequestCompletion implements [x.EmbeddingsMetrics].
func (m *mockEmbeddingsMetrics) RecordRequestCompletion(_ context.Context, success bool, _ ...attribute.KeyValue) {
	if success {
		m.requestSuccessCount++
	} else {
		m.requestErrorCount++
	}
}

// RequireSelectedModel asserts the model set on the metrics.
func (m *mockEmbeddingsMetrics) RequireSelectedModel(t *testing.T, model string) {
	require.Equal(t, model, m.model)
}

// RequireSelectedBackend asserts the backend set on the metrics.
func (m *mockEmbeddingsMetrics) RequireSelectedBackend(t *testing.T, backend string) {
	require.Equal(t, backend, m.backend)
}

// RequireRequestFailure asserts the request was marked as a failure.
func (m *mockEmbeddingsMetrics) RequireRequestFailure(t *testing.T) {
	require.Equal(t, 0, m.requestSuccessCount)
	require.Equal(t, 1, m.requestErrorCount)
}

// RequireRequestNotCompleted asserts the request was not completed.
func (m *mockEmbeddingsMetrics) RequireRequestNotCompleted(t *testing.T) {
	require.Equal(t, 0, m.requestSuccessCount)
	require.Equal(t, 0, m.requestErrorCount)
}

// RequireRequestSuccess asserts the request was marked as a success.
func (m *mockEmbeddingsMetrics) RequireRequestSuccess(t *testing.T) {
	require.Equal(t, 1, m.requestSuccessCount)
	require.Equal(t, 0, m.requestErrorCount)
}

// RequireTokensRecorded asserts the number of tokens recorded.
func (m *mockEmbeddingsMetrics) RequireTokensRecorded(t *testing.T, count int) {
	require.Equal(t, count, m.tokenUsageCount)
}

var _ x.EmbeddingsMetrics = &mockEmbeddingsMetrics{}
