// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/testing/fakeopenai"
)

func TestChatCompletions(t *testing.T) {
	env := SetupTestEnvironment(t)
	defer env.Close()

	listenerPort := env.ListenerPort

	// Define test cases for different request types.
	tests := []struct {
		name               string // same as the cassette name.
		expectResponseBody string
		expectStatusCode   int
	}{
		{
			name: fakeopenai.CassetteChatBasic,
			expectResponseBody: `{
  "id": "chatcmpl-BpYvY6uXAMC7BF9cEriK2br4THEou",
  "object": "chat.completion",
  "created": 1751628688,
  "model": "gpt-4.1-nano-2025-04-14",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Southern Ocean",
        "refusal": null,
        "annotations": []
      },
      "logprobs": null,
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 22,
    "completion_tokens": 2,
    "total_tokens": 24,
    "prompt_tokens_details": {
      "cached_tokens": 0,
      "audio_tokens": 0
    },
    "completion_tokens_details": {
      "reasoning_tokens": 0,
      "audio_tokens": 0,
      "accepted_prediction_tokens": 0,
      "rejected_prediction_tokens": 0
    }
  },
  "service_tier": "default",
  "system_fingerprint": "fp_38343a2f8f"
}
`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatStreaming,
			expectResponseBody: `data: {"id":"chatcmpl-BupIE1Yst8aJrvBW2GrvLDTlUtyCB","object":"chat.completion.chunk","created":1752883238,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BupIE1Yst8aJrvBW2GrvLDTlUtyCB","object":"chat.completion.chunk","created":1752883238,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"South"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BupIE1Yst8aJrvBW2GrvLDTlUtyCB","object":"chat.completion.chunk","created":1752883238,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" Atlantic"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BupIE1Yst8aJrvBW2GrvLDTlUtyCB","object":"chat.completion.chunk","created":1752883238,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" Ocean"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BupIE1Yst8aJrvBW2GrvLDTlUtyCB","object":"chat.completion.chunk","created":1752883238,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"."},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BupIE1Yst8aJrvBW2GrvLDTlUtyCB","object":"chat.completion.chunk","created":1752883238,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}],"usage":null}

data: {"id":"chatcmpl-BupIE1Yst8aJrvBW2GrvLDTlUtyCB","object":"chat.completion.chunk","created":1752883238,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":null,"choices":[],"usage":{"prompt_tokens":22,"completion_tokens":4,"total_tokens":26,"prompt_tokens_details":{"cached_tokens":0,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":0,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}}

data: [DONE]

`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatTools,
			expectResponseBody: `{
  "id": "chatcmpl-BupIGRXl5WBRXY7ERXY5wRCCUZCFx",
  "object": "chat.completion",
  "created": 1752883240,
  "model": "gpt-4o-mini-2024-07-18",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "South Atlantic Ocean.",
        "refusal": null,
        "annotations": []
      },
      "logprobs": null,
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 75,
    "completion_tokens": 5,
    "total_tokens": 80,
    "prompt_tokens_details": {
      "cached_tokens": 0,
      "audio_tokens": 0
    },
    "completion_tokens_details": {
      "reasoning_tokens": 0,
      "audio_tokens": 0,
      "accepted_prediction_tokens": 0,
      "rejected_prediction_tokens": 0
    }
  },
  "service_tier": "default",
  "system_fingerprint": null
}
`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatMultimodal,
			expectResponseBody: `{
  "id": "chatcmpl-BupII9igyIpXdvYIN8W7Cqeetcw4p",
  "object": "chat.completion",
  "created": 1752883242,
  "model": "gpt-4o-mini-2024-07-18",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "South Atlantic Ocean.",
        "refusal": null,
        "annotations": []
      },
      "logprobs": null,
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 8522,
    "completion_tokens": 4,
    "total_tokens": 8526,
    "prompt_tokens_details": {
      "cached_tokens": 0,
      "audio_tokens": 0
    },
    "completion_tokens_details": {
      "reasoning_tokens": 0,
      "audio_tokens": 0,
      "accepted_prediction_tokens": 0,
      "rejected_prediction_tokens": 0
    }
  },
  "service_tier": "default",
  "system_fingerprint": null
}
`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatMultiturn,
			expectResponseBody: `{
  "id": "chatcmpl-BupILtXaOEg1A5XNUHbkFOdLPGA3N",
  "object": "chat.completion",
  "created": 1752883245,
  "model": "gpt-4o-mini-2024-07-18",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "South Atlantic.",
        "refusal": null,
        "annotations": []
      },
      "logprobs": null,
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 58,
    "completion_tokens": 3,
    "total_tokens": 61,
    "prompt_tokens_details": {
      "cached_tokens": 0,
      "audio_tokens": 0
    },
    "completion_tokens_details": {
      "reasoning_tokens": 0,
      "audio_tokens": 0,
      "accepted_prediction_tokens": 0,
      "rejected_prediction_tokens": 0
    }
  },
  "service_tier": "default",
  "system_fingerprint": null
}
`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatJSONMode,
			expectResponseBody: `{
  "error": {
    "message": "'messages' must contain the word 'json' in some form, to use 'response_format' of type 'json_object'.",
    "type": "invalid_request_error",
    "param": "messages",
    "code": null
  }
}`,
			expectStatusCode: http.StatusBadRequest,
		},
		{
			name: fakeopenai.CassetteChatNoMessages,
			expectResponseBody: `{
  "error": {
    "message": "Missing required parameter: 'messages'.",
    "type": "invalid_request_error",
    "param": "messages",
    "code": "missing_required_parameter"
  }
}`,
			expectStatusCode: http.StatusBadRequest,
		},
		{
			name: fakeopenai.CassetteChatBadModel,
			expectResponseBody: `{
    "error": {
        "message": "The model ` + "`invalid-model-xyz`" + ` does not exist or you do not have access to it.",
        "type": "invalid_request_error",
        "param": null,
        "code": "model_not_found"
    }
}
`,
			expectStatusCode: http.StatusNotFound,
		},
		{
			name: fakeopenai.CassetteChatParallelTools,
			expectResponseBody: `{
  "id": "chatcmpl-BupIR7hwxVBdoH5JuYQz3oS1o8KdT",
  "object": "chat.completion",
  "created": 1752883251,
  "model": "gpt-4o-mini-2024-07-18",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "South Atlantic Ocean.",
        "refusal": null,
        "annotations": []
      },
      "logprobs": null,
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 58,
    "completion_tokens": 5,
    "total_tokens": 63,
    "prompt_tokens_details": {
      "cached_tokens": 0,
      "audio_tokens": 0
    },
    "completion_tokens_details": {
      "reasoning_tokens": 0,
      "audio_tokens": 0,
      "accepted_prediction_tokens": 0,
      "rejected_prediction_tokens": 0
    }
  },
  "service_tier": "default",
  "system_fingerprint": null
}
`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteEdgeBadRequest,
			expectResponseBody: `{
  "error": {
    "message": "Invalid 'max_tokens': integer below minimum value. Expected a value >= 1, but got 0 instead.",
    "type": "invalid_request_error",
    "param": "max_tokens",
    "code": "integer_below_min_value"
  }
}`,
			expectStatusCode: http.StatusBadRequest,
		},
		{
			name: fakeopenai.CassetteEdgeBase64Image,
			expectResponseBody: `{
  "id": "chatcmpl-BupI5HOPx9XYbtKOofKJiQdBRTvnT",
  "object": "chat.completion",
  "created": 1752883229,
  "model": "gpt-4o-mini-2024-07-18",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "South Atlantic Ocean.",
        "refusal": null,
        "annotations": []
      },
      "logprobs": null,
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 8522,
    "completion_tokens": 4,
    "total_tokens": 8526,
    "prompt_tokens_details": {
      "cached_tokens": 0,
      "audio_tokens": 0
    },
    "completion_tokens_details": {
      "reasoning_tokens": 0,
      "audio_tokens": 0,
      "accepted_prediction_tokens": 0,
      "rejected_prediction_tokens": 0
    }
  },
  "service_tier": "default",
  "system_fingerprint": null
}
`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatUnknownModel,
			expectResponseBody: `{
    "error": {
        "message": "The model ` + "`gpt-4.1-nano-wrong`" + ` does not exist or you do not have access to it.",
        "type": "invalid_request_error",
        "param": null,
        "code": "model_not_found"
    }
}
`,
			expectStatusCode: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := fakeopenai.NewRequest(fmt.Sprintf("http://localhost:%d/v1", listenerPort), tc.name)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			require.Equal(t, tc.expectStatusCode, resp.StatusCode)
			require.Equal(t, tc.expectResponseBody, string(body))
		})
	}
}
