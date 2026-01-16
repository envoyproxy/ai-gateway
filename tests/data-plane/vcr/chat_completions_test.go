// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package vcr

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/testsinternal/testopenai"
)

func TestOpenAIChatCompletions(t *testing.T) {
	listenerPort := startTestEnvironment(t, nil).EnvoyListenerPort()

	// Define test cases for different request types.
	// These expectations match the actual responses from the test OpenAI server cassettes.
	tests := []struct {
		name               testopenai.Cassette
		expectStatusCode   int
		expectResponseBody string // only set this when not the same as what's proxied.
	}{
		{
			name:             testopenai.CassetteChatBasic,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatStreaming,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatTools,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatMultimodal,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatMultiturn,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatJSONMode,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatNoMessages,
			expectStatusCode: http.StatusBadRequest,
		},
		{
			name:             testopenai.CassetteChatParallelTools,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatBadRequest,
			expectStatusCode: http.StatusBadRequest,
		},
		{
			name:               testopenai.CassetteChatUnknownModel,
			expectResponseBody: "{\n    \"error\": {\n        \"message\": \"The model `gpt-4.1-nano-wrong` does not exist or you do not have access to it.\",\n        \"type\": \"invalid_request_error\",\n        \"param\": null,\n        \"code\": \"model_not_found\"\n    }\n}\n",
			expectStatusCode:   http.StatusNotFound,
		},
		{
			name:             testopenai.CassetteChatReasoning,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatImageToText,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatTextToImageTool,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatAudioToText,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatTextToAudio,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatDetailedUsage,
			expectStatusCode: http.StatusOK,
		},
		{
			name:             testopenai.CassetteChatStreamingDetailedUsage,
			expectStatusCode: http.StatusOK,
		},
	}

	was5xx := false
	for _, tc := range tests {
		if was5xx {
			return // rather than also failing subsequent tests, which confuses root cause.
		}
		t.Run(tc.name.String(), func(t *testing.T) {
			req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d", listenerPort), tc.name)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			if resp.StatusCode == http.StatusBadGateway {
				was5xx = true // assertions will fail later and log the body.
			}
			// Safe to use assert as no nil risk and response body explains status.
			expectedBody := tc.expectResponseBody
			if expectedBody == "" {
				expectedBody = testopenai.ResponseBody(tc.name)
			}
			assert.Equal(t, expectedBody, string(body))
		})
	}
}
