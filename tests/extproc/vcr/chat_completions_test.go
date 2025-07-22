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

	"github.com/envoyproxy/ai-gateway/tests/internal/fakeopenai"
)

func TestChatCompletions(t *testing.T) {
	env := setupTestEnvironment(t)
	defer env.Close()

	listenerPort := env.listenerPort

	// Define test cases for different request types.
	// These expectations match the actual responses from the fake OpenAI server cassettes.
	tests := []struct {
		name               fakeopenai.Cassette
		expectResponseBody string
		expectStatusCode   int
	}{
		{
			name: fakeopenai.CassetteChatBasic,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "logprobs": null,
      "message": {
        "annotations": [],
        "content": "Hello! How can I assist you today?",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753162006,
  "id": "chatcmpl-BvzoUDLRFA18TkSzKSxeGRL5XpbRr",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 9,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 9,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 18
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatStreaming,
			expectResponseBody: `data: {"id":"chatcmpl-BvzoWhZJbwLOqU99BTljeM8ve29kc","object":"chat.completion.chunk","created":1753162008,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BvzoWhZJbwLOqU99BTljeM8ve29kc","object":"chat.completion.chunk","created":1753162008,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"Hello"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BvzoWhZJbwLOqU99BTljeM8ve29kc","object":"chat.completion.chunk","created":1753162008,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"!"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BvzoWhZJbwLOqU99BTljeM8ve29kc","object":"chat.completion.chunk","created":1753162008,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" How"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BvzoWhZJbwLOqU99BTljeM8ve29kc","object":"chat.completion.chunk","created":1753162008,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" can"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BvzoWhZJbwLOqU99BTljeM8ve29kc","object":"chat.completion.chunk","created":1753162008,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" I"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BvzoWhZJbwLOqU99BTljeM8ve29kc","object":"chat.completion.chunk","created":1753162008,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" assist"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BvzoWhZJbwLOqU99BTljeM8ve29kc","object":"chat.completion.chunk","created":1753162008,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" you"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BvzoWhZJbwLOqU99BTljeM8ve29kc","object":"chat.completion.chunk","created":1753162008,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" today"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BvzoWhZJbwLOqU99BTljeM8ve29kc","object":"chat.completion.chunk","created":1753162008,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"?"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-BvzoWhZJbwLOqU99BTljeM8ve29kc","object":"chat.completion.chunk","created":1753162008,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}],"usage":null}

data: {"id":"chatcmpl-BvzoWhZJbwLOqU99BTljeM8ve29kc","object":"chat.completion.chunk","created":1753162008,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[],"usage":{"prompt_tokens":19,"completion_tokens":9,"total_tokens":28,"prompt_tokens_details":{"cached_tokens":0,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":0,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}}

data: [DONE]

`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatTools,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "tool_calls",
      "index": 0,
      "logprobs": null,
      "message": {
        "annotations": [],
        "content": null,
        "refusal": null,
        "role": "assistant",
        "tool_calls": [
          {
            "function": {
              "arguments": "{\"location\": \"Boston\"}",
              "name": "get_current_weather"
            },
            "id": "call_26LsX7EjeYJZFKQVtq7cZKmC",
            "type": "function"
          },
          {
            "function": {
              "arguments": "{\"location\": \"Boston\"}",
              "name": "get_current_weather"
            },
            "id": "call_pQt0Pxu6mOA30pUBGfiTTgn1",
            "type": "function"
          }
        ]
      }
    }
  ],
  "created": 1753162009,
  "id": "chatcmpl-BvzoXS47ObBgHCv5ypPuJjnUQcSk9",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 46,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 81,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 127
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatMultimodal,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "logprobs": null,
      "message": {
        "annotations": [],
        "content": "This image shows a wooden pathway or boardwalk cutting through a lush, green grassy field under a partly cloudy sky. The scene appears peaceful and natural, with trees and bushes in the distance.",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753162012,
  "id": "chatcmpl-Bvzoa1bfMP6Un9WZKOGEAG0gdnYum",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 38,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 3675,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 3713
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatMultiturn,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "logprobs": null,
      "message": {
        "annotations": [],
        "content": "I don't have real-time access to current weather data. However, I can help you find the weather forecast if you tell me your location. Alternatively, you can check a weather website or app for the latest updates. How can I assist you further?",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753162021,
  "id": "chatcmpl-BvzojqxiCJvlfUn7EkHrdDbGaNqwg",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 50,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 41,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 91
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatJSONMode,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "logprobs": null,
      "message": {
        "annotations": [],
        "content": "{\n  \"name\": \"John Doe\",\n  \"age\": 30,\n  \"city\": \"New York\"\n}",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753162057,
  "id": "chatcmpl-BvzpJO8f3GTzaZ025BmH3BB7H6oKv",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 25,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 22,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 47
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatNoMessages,
			expectResponseBody: `{
  "error": {
    "code": "empty_array",
    "message": "Invalid 'messages': empty array. Expected an array with minimum length 1, but got an empty array instead.",
    "param": "messages",
    "type": "invalid_request_error"
  }
}`,
			expectStatusCode: http.StatusBadRequest,
		},
		{
			name: fakeopenai.CassetteChatParallelTools,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "tool_calls",
      "index": 0,
      "logprobs": null,
      "message": {
        "annotations": [],
        "content": null,
        "refusal": null,
        "role": "assistant",
        "tool_calls": [
          {
            "function": {
              "arguments": "{\"location\": \"San Francisco, CA\"}",
              "name": "get_current_weather"
            },
            "id": "call_D6u1WUoe5SU9K153FitUAhfw",
            "type": "function"
          },
          {
            "function": {
              "arguments": "{\"location\": \"San Francisco, CA\"}",
              "name": "get_current_weather"
            },
            "id": "call_aQlRzPlzkHJ29YluNaXlVfuT",
            "type": "function"
          }
        ]
      }
    }
  ],
  "created": 1753162059,
  "id": "chatcmpl-BvzpL1xt8QNyiT9xFDZp0olg9pSEx",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 52,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 81,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 133
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: fakeopenai.CassetteChatBadRequest,
			expectResponseBody: `{
  "error": {
    "code": "integer_below_min_value",
    "message": "Invalid 'max_tokens': integer below minimum value. Expected a value >= 1, but got 0 instead.",
    "param": "max_tokens",
    "type": "invalid_request_error"
  }
}`,
			expectStatusCode: http.StatusBadRequest,
		},
		{
			name: fakeopenai.CassetteChatBase64Image,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "logprobs": null,
      "message": {
        "annotations": [],
        "content": "It seems the image didn't load properly. Could you please try uploading it again?",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753162062,
  "id": "chatcmpl-BvzpObAOzjs99gvHvWxmnRoOx9tP3",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 16,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 16,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 32
  }
}`,
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

	wasBadGateway := false
	for _, tc := range tests {
		if wasBadGateway {
			t.Logf("=== ExtProc Output (stdout + stderr) ===\n%s", env.extprocOut.String())
			t.Logf("=== Envoy Output (stdout + stderr) ===\n%s", env.envoyOut.String())
			return // rather than also failing subsequent tests, which confuses root cause.
		}
		t.Run(tc.name.String(), func(t *testing.T) {
			req, err := fakeopenai.NewRequest(fmt.Sprintf("http://localhost:%d/v1", listenerPort), tc.name)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			if resp.StatusCode == http.StatusBadGateway {
				wasBadGateway = true
			}
			// Safe to use assert as no nil risk and response body explains status.
			assert.Equal(t, tc.expectStatusCode, resp.StatusCode)
			assert.Equal(t, tc.expectResponseBody, string(body))
		})
	}
}
