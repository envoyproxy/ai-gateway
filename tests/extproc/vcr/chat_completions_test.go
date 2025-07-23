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

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

func TestOpenAIChatCompletions(t *testing.T) {
	env := setupTestEnvironment(t)
	defer env.Close()

	listenerPort := env.listenerPort

	// Define test cases for different request types.
	// These expectations match the actual responses from the test OpenAI server cassettes.
	tests := []struct {
		name               testopenai.Cassette
		expectResponseBody string
		expectStatusCode   int
	}{
		{
			name: testopenai.CassetteChatBasic,
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
  "created": 1753423143,
  "id": "chatcmpl-Bx5kNovDsMvLVkXYomgZvfV95lhEd",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": "fp_38343a2f8f",
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
			name: testopenai.CassetteChatStreaming,
			expectResponseBody: `data: {"id":"chatcmpl-Bx5kRNsKBfYXdH2IeSETLB6DQk1kC","object":"chat.completion.chunk","created":1753423147,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_479cfdfab2","choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5kRNsKBfYXdH2IeSETLB6DQk1kC","object":"chat.completion.chunk","created":1753423147,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_479cfdfab2","choices":[{"index":0,"delta":{"content":"Hello"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5kRNsKBfYXdH2IeSETLB6DQk1kC","object":"chat.completion.chunk","created":1753423147,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_479cfdfab2","choices":[{"index":0,"delta":{"content":"!"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5kRNsKBfYXdH2IeSETLB6DQk1kC","object":"chat.completion.chunk","created":1753423147,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_479cfdfab2","choices":[{"index":0,"delta":{"content":" How"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5kRNsKBfYXdH2IeSETLB6DQk1kC","object":"chat.completion.chunk","created":1753423147,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_479cfdfab2","choices":[{"index":0,"delta":{"content":" can"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5kRNsKBfYXdH2IeSETLB6DQk1kC","object":"chat.completion.chunk","created":1753423147,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_479cfdfab2","choices":[{"index":0,"delta":{"content":" I"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5kRNsKBfYXdH2IeSETLB6DQk1kC","object":"chat.completion.chunk","created":1753423147,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_479cfdfab2","choices":[{"index":0,"delta":{"content":" assist"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5kRNsKBfYXdH2IeSETLB6DQk1kC","object":"chat.completion.chunk","created":1753423147,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_479cfdfab2","choices":[{"index":0,"delta":{"content":" you"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5kRNsKBfYXdH2IeSETLB6DQk1kC","object":"chat.completion.chunk","created":1753423147,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_479cfdfab2","choices":[{"index":0,"delta":{"content":" today"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5kRNsKBfYXdH2IeSETLB6DQk1kC","object":"chat.completion.chunk","created":1753423147,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_479cfdfab2","choices":[{"index":0,"delta":{"content":"?"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5kRNsKBfYXdH2IeSETLB6DQk1kC","object":"chat.completion.chunk","created":1753423147,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_479cfdfab2","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}],"usage":null}

data: {"id":"chatcmpl-Bx5kRNsKBfYXdH2IeSETLB6DQk1kC","object":"chat.completion.chunk","created":1753423147,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_479cfdfab2","choices":[],"usage":{"prompt_tokens":19,"completion_tokens":9,"total_tokens":28,"prompt_tokens_details":{"cached_tokens":0,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":0,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}}

data: [DONE]

`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: testopenai.CassetteChatTools,
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
            "id": "call_1o2wGNYyriaQFlwkmvBmkJs7",
            "type": "function"
          },
          {
            "function": {
              "arguments": "{\"location\": \"Boston\"}",
              "name": "get_current_weather"
            },
            "id": "call_vLHpymLmyGCbjCxUJZf0aP45",
            "type": "function"
          }
        ]
      }
    }
  ],
  "created": 1753423148,
  "id": "chatcmpl-Bx5kSOQua7eWnIemycaUxlSzwmHyi",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": "fp_38343a2f8f",
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
			name: testopenai.CassetteChatMultimodal,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "logprobs": null,
      "message": {
        "annotations": [],
        "content": "This image shows a wooden pathway running through a lush, green grassy field under a partly cloudy blue sky. The scene appears peaceful and rural, with trees in the distance and vibrant natural surroundings.",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753423151,
  "id": "chatcmpl-Bx5kVc7U7izUQncE24gOSCVWJld1e",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": "fp_38343a2f8f",
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
			name: testopenai.CassetteChatMultiturn,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "logprobs": null,
      "message": {
        "annotations": [],
        "content": "I need location details.",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753423155,
  "id": "chatcmpl-Bx5kZF0xoXkDFMJujGg5fenim2yh2",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": "fp_38343a2f8f",
  "usage": {
    "completion_tokens": 5,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 49,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 54
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: testopenai.CassetteChatJSONMode,
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
  "created": 1753423157,
  "id": "chatcmpl-Bx5kbEUUMAowmenXgOVnlnUEiEZws",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": "fp_38343a2f8f",
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
			name: testopenai.CassetteChatNoMessages,
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
			name: testopenai.CassetteChatParallelTools,
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
            "id": "call_tJS1YOMPgrRQ0uOhzX6u3xRW",
            "type": "function"
          }
        ]
      }
    }
  ],
  "created": 1753423163,
  "id": "chatcmpl-Bx5kh1kb0qKYD3a7CmCaJwAbMohnb",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": "fp_38343a2f8f",
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
			name: testopenai.CassetteChatBadRequest,
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
			name: testopenai.CassetteChatUnknownModel,
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
		{
			name: testopenai.CassetteChatReasoning,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "message": {
        "annotations": [],
        "content": "Let's call the cost of the ball x dollars. Since the bat costs $1 more than the ball, the bat costs x + 1 dollars. According to the problem, together they cost $1.10:\n\nx + (x + 1) = 1.10\n\nCombine like terms:\n\n2x + 1 = 1.10\n\nSubtract 1 from both sides:\n\n2x = 0.10\n\nDivide both sides by 2:\n\nx = 0.05\n\nSo, the ball costs 5 cents.",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753426499,
  "id": "chatcmpl-Bx6cVJgZ2XKLXyxEEE1bCseXcilXV",
  "model": "o3-mini-2025-01-31",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 187,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 64,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 27,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 214
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: testopenai.CassetteChatImageToText,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "logprobs": null,
      "message": {
        "annotations": [],
        "content": "A colorful Nintendo Switch game",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753423165,
  "id": "chatcmpl-Bx5kjGx7JwhMgT2Ov820b7pO9CYrv",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": "fp_38343a2f8f",
  "usage": {
    "completion_tokens": 5,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 24,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 29
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: testopenai.CassetteChatTextToImageTool,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "stop",
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
              "arguments": "{\"prompt\":\"A simple, minimalist sketch-style drawing of a cute cat playing with a ball of yarn.\"}",
              "name": "generate_image"
            },
            "id": "call_nhKKLlnwC3sew7xv4rnYpqVH",
            "type": "function"
          }
        ]
      }
    }
  ],
  "created": 1753423214,
  "id": "chatcmpl-Bx5lWqI4bV0HErS7PX8Q3CUHXoFni",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": "fp_38343a2f8f",
  "usage": {
    "completion_tokens": 22,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 131,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 153
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: testopenai.CassetteChatAudioToText,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "message": {
        "annotations": [],
        "content": "Certainly! Please share the audio, and I'll tell you what I hear.",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753423217,
  "id": "chatcmpl-Bx5lZtphlT8syGmka1aflVu8IMFSr",
  "model": "gpt-4o-audio-preview-2025-06-03",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": "fp_b5d60d6081",
  "usage": {
    "completion_tokens": 15,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0,
      "text_tokens": 15
    },
    "prompt_tokens": 28,
    "prompt_tokens_details": {
      "audio_tokens": 1,
      "cached_tokens": 0,
      "image_tokens": 0,
      "text_tokens": 27
    },
    "total_tokens": 43
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: testopenai.CassetteChatTextToAudio,
			// TODO: This test returns a very large audio response that we cannot fully match.
			// For now, we'll skip the body check and just verify status code.
			expectResponseBody: `SKIP_BODY_CHECK`,
			expectStatusCode:   http.StatusOK,
		},
		{
			name: testopenai.CassetteChatDetailedUsage,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "logprobs": null,
      "message": {
        "annotations": [],
        "content": "Signals flow like streams,  \nTracing whispers through the code,  \nInsights softly dawn.",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1753423222,
  "id": "chatcmpl-Bx5leAxYQ3dJoFhNRPRmeW3MtYcfY",
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": "fp_38343a2f8f",
  "usage": {
    "completion_tokens": 17,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 33,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 50
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: testopenai.CassetteChatStreamingDetailedUsage,
			expectResponseBody: `data: {"id":"chatcmpl-Bx5lfsiWaJAorkTpnbrjlnAnl0892","object":"chat.completion.chunk","created":1753423223,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_38343a2f8f","choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5lfsiWaJAorkTpnbrjlnAnl0892","object":"chat.completion.chunk","created":1753423223,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_38343a2f8f","choices":[{"index":0,"delta":{"content":"Hello"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5lfsiWaJAorkTpnbrjlnAnl0892","object":"chat.completion.chunk","created":1753423223,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_38343a2f8f","choices":[{"index":0,"delta":{"content":"!"},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-Bx5lfsiWaJAorkTpnbrjlnAnl0892","object":"chat.completion.chunk","created":1753423223,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_38343a2f8f","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}],"usage":null}

data: {"id":"chatcmpl-Bx5lfsiWaJAorkTpnbrjlnAnl0892","object":"chat.completion.chunk","created":1753423223,"model":"gpt-4.1-nano-2025-04-14","service_tier":"default","system_fingerprint":"fp_38343a2f8f","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":2,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":0,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":0,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}}

data: [DONE]

`,
			expectStatusCode: http.StatusOK,
		},
	}

	wasBadGateway := false
	for _, tc := range tests {
		if wasBadGateway {
			env.logEnvoyAndExtProc(t)
			return // rather than also failing subsequent tests, which confuses root cause.
		}
		t.Run(tc.name.String(), func(t *testing.T) {
			req, err := testopenai.NewRequest(fmt.Sprintf("http://localhost:%d/v1", listenerPort), tc.name)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			if resp.StatusCode == http.StatusBadGateway {
				wasBadGateway = true // assertions will fail later and log the body.
			}
			// Safe to use assert as no nil risk and response body explains status.
			assert.Equal(t, tc.expectStatusCode, resp.StatusCode)
			if tc.expectResponseBody != "SKIP_BODY_CHECK" {
				assert.Equal(t, tc.expectResponseBody, string(body))
			}
		})
	}
}
