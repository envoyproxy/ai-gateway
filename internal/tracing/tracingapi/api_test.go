// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracingapi // nolint: revive

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func TestNoopTracing(t *testing.T) {
	tracing := NoopTracing{}
	require.IsType(t, NoopTracer[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]{}, tracing.ChatCompletionTracer())
	require.Equal(t, NoopMCPTracer{}, tracing.MCPTracer())

	// Calling shutdown twice should not cause an error.
	require.NoError(t, tracing.Shutdown(t.Context()))
	require.NoError(t, tracing.Shutdown(t.Context()))
}

func TestNoopTracerChatCompletion(t *testing.T) {
	tracer := NoopTracer[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]{}

	readHeaders := map[string]string{}
	writeHeaders := propagation.MapCarrier{}
	req := &openai.ChatCompletionRequest{}
	reqBody := []byte{'{', '}'}

	span := tracer.StartSpanAndInjectHeaders(
		t.Context(),
		readHeaders,
		writeHeaders,
		req,
		reqBody,
	)

	// Currently we return nil from this, but that should be re-evaluated as it
	// can cause subtle bugs and limit our ability to control scoping in the
	// future.
	require.Nil(t, span)

	// no side effects
	require.Equal(t, map[string]string{}, readHeaders)
	require.Equal(t, propagation.MapCarrier{}, writeHeaders)
	require.Equal(t, &openai.ChatCompletionRequest{}, req)
	require.Equal(t, []byte{'{', '}'}, reqBody)
}
