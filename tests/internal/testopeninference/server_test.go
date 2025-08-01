// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopeninference

import (
	"context"
	"io"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

func TestGetSpan(t *testing.T) {
	validSpanJSON := `{
        "name": "ChatCompletion",
        "kind": "SPAN_KIND_INTERNAL",
        "attributes": [
            {
                "key": "llm.system",
                "value": {"stringValue": "openai"}
            }
        ]
    }`

	tests := []struct {
		name        string
		files       map[string]*fstest.MapFile
		recordSpans string
		expectSpan  *tracev1.Span
		expectError string
	}{
		{
			name:  "cached",
			files: map[string]*fstest.MapFile{"spans/chat-basic.json": {Data: []byte(validSpanJSON)}},
			expectSpan: &tracev1.Span{
				Name: "ChatCompletion",
				Kind: tracev1.Span_SPAN_KIND_INTERNAL,
			},
		},
		{
			name:        "missing no record",
			expectError: "span not found for cassette chat-basic and RECORD_SPANS is not set",
		},
		{
			name:        "record enabled",
			recordSpans: "true",
			expectSpan: &tracev1.Span{
				Name: "ChatCompletion",
				Kind: tracev1.Span_SPAN_KIND_INTERNAL,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.recordSpans != "" {
				t.Setenv("RECORD_SPANS", tt.recordSpans)
			}
			s, err := newServer(io.Discard, fstest.MapFS(tt.files), t.TempDir())
			require.NoError(t, err)
			if tt.expectSpan != nil && tt.recordSpans != "" {
				s.recorder.startProxy = mockProxy
			}
			span, err := s.getSpan(context.Background(), testopenai.CassetteChatBasic)
			if tt.expectError != "" {
				require.EqualError(t, err, tt.expectError)
				return
			}
			require.NoError(t, err)
			if tt.expectSpan != nil {
				require.NotNil(t, span)
				require.Equal(t, tt.expectSpan.Name, span.Name)
				require.Equal(t, tt.expectSpan.Kind, span.Kind)
			} else {
				require.Nil(t, span)
			}
		})
	}
}
