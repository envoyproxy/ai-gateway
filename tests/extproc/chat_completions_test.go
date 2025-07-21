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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/fakeopenai"
)

func TestChatCompletions(t *testing.T) {
	env := setupTestEnvironment(t)
	defer env.Close()

	listenerPort := env.listenerPort

	// Define test cases for different request types.
	// These expectations match the actual responses from the fake OpenAI server cassettes.
	tests := []struct {
		name               fakeopenai.CassetteName
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
  "created": 1753158974,
  "id": "chatcmpl-Bvz1a5W2Zpze5C8RA8KBox4K6vFsi",
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
			expectResponseBody: `data: {"id":"chatcmpl-Bvz1cLxlCNSNS3qJL9JpAdpbrnggX","object":"chat.completion.chunk","created":1753158976,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bvz1cLxlCNSNS3qJL9JpAdpbrnggX","object":"chat.completion.chunk","created":1753158976,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"Hello"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bvz1cLxlCNSNS3qJL9JpAdpbrnggX","object":"chat.completion.chunk","created":1753158976,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"!"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bvz1cLxlCNSNS3qJL9JpAdpbrnggX","object":"chat.completion.chunk","created":1753158976,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" How"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bvz1cLxlCNSNS3qJL9JpAdpbrnggX","object":"chat.completion.chunk","created":1753158976,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" can"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bvz1cLxlCNSNS3qJL9JpAdpbrnggX","object":"chat.completion.chunk","created":1753158976,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" I"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bvz1cLxlCNSNS3qJL9JpAdpbrnggX","object":"chat.completion.chunk","created":1753158976,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" assist"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bvz1cLxlCNSNS3qJL9JpAdpbrnggX","object":"chat.completion.chunk","created":1753158976,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" you"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bvz1cLxlCNSNS3qJL9JpAdpbrnggX","object":"chat.completion.chunk","created":1753158976,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" today"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bvz1cLxlCNSNS3qJL9JpAdpbrnggX","object":"chat.completion.chunk","created":1753158976,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"?"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bvz1cLxlCNSNS3qJL9JpAdpbrnggX","object":"chat.completion.chunk","created":1753158976,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}],"usage":null}

data: {"id":"chatcmpl-Bvz1cLxlCNSNS3qJL9JpAdpbrnggX","object":"chat.completion.chunk","created":1753158976,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":null,"choices":[],"usage":{"prompt_tokens":19,"completion_tokens":9,"total_tokens":28,"prompt_tokens_details":{"cached_tokens":0,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":0,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}}

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
              "arguments": "{\"location\": \"Boston, MA\"}",
              "name": "get_current_weather"
            },
            "id": "call_D3F3FgeNpo7YYla1fW7I0cu4",
            "type": "function"
          },
          {
            "function": {
              "arguments": "{\"location\": \"Boston, MA\"}",
              "name": "get_current_weather"
            },
            "id": "call_bEF70X0NXwdKE3UPd0DKMBsO",
            "type": "function"
          }
        ]
      }
    }
  ],
  "created": 1753158977,
  "id": "chatcmpl-Bvz1dGgYteqrsbxUHK04G7k238oG1",
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
    "prompt_tokens": 81,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 131
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
        "content": "The image shows a wooden pathway or boardwalk running through a lush green field or meadow, with trees in the distance. The sky is partly cloudy with blue and white clouds, suggesting a bright and peaceful day.",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753158982,
  "id": "chatcmpl-Bvz1iNWdUzQzIXlWE8hWMH2rtLjDR",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 42,
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
    "total_tokens": 3717
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
        "content": "I don't have access to real-time weather data. However, you can check the current weather for your location using a weather app on your device, or by searching \"weather\" followed by your location on a search engine. If you tell me your location, I can help you find the best way to get the latest weather information!",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753158986,
  "id": "chatcmpl-Bvz1mWg3RzUcZqHGSKiwxeRgIuzcw",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 66,
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
    "total_tokens": 107
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
  "created": 1753158988,
  "id": "chatcmpl-Bvz1o4yvSlHIo4hE7gaGVCzwnXlCb",
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
    "code": "missing_required_parameter",
    "message": "Missing required parameter: 'messages'.",
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
              "arguments": "{\"location\":\"San Francisco\"}",
              "name": "get_current_weather"
            },
            "id": "call_DHUvMd9gdjnyNtquborNDiMr",
            "type": "function"
          }
        ]
      }
    }
  ],
  "created": 1753158991,
  "id": "chatcmpl-Bvz1rrnlzLEvhGDcP7UHZH7U3xJ50",
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
    "prompt_tokens": 81,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 97
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
        "content": "It looks like the image is empty or there was an issue with uploading. Could you please try sending it again?",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753158993,
  "id": "chatcmpl-Bvz1teiJfRwx7v2j3vmwmu82u8bHy",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 23,
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
    "total_tokens": 39
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
			return // rather than also failing subsequent tests which confuses root cause.
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
