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
	env := startTestEnvironment(t, extprocBin, extprocConfig, nil, envoyConfig)

	listenerPort := env.EnvoyListenerPort()

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
  "choices" : [ {
    "finish_reason" : "stop",
    "index" : 0,
    "message" : {
      "annotations" : [ ],
      "content" : "Hi there! How can I help you today? I can explain concepts, answer questions, help with writing or editing, brainstorm ideas, assist with coding or math, plan tasks, and more. Tell me what you’d like to do.",
      "refusal" : null,
      "role" : "assistant"
    }
  } ],
  "created" : 1755133833,
  "id" : "chatcmpl-C4Gm9xikLXbgE8He0BHWeoM03aa72",
  "model" : "gpt-5-nano-2025-08-07",
  "object" : "chat.completion",
  "service_tier" : "default",
  "system_fingerprint" : null,
  "usage" : {
    "completion_tokens" : 377,
    "completion_tokens_details" : {
      "accepted_prediction_tokens" : 0,
      "audio_tokens" : 0,
      "reasoning_tokens" : 320,
      "rejected_prediction_tokens" : 0
    },
    "prompt_tokens" : 8,
    "prompt_tokens_details" : {
      "audio_tokens" : 0,
      "cached_tokens" : 0
    },
    "total_tokens" : 385
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name:               testopenai.CassetteChatStreaming,
			expectResponseBody: `SKIP_BODY_CHECK`,
			expectStatusCode:   http.StatusOK,
		},
		{
			name: testopenai.CassetteChatTools,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "tool_calls",
      "index": 0,
      "message": {
        "annotations": [],
        "content": null,
        "refusal": null,
        "role": "assistant",
        "tool_calls": [
          {
            "function": {
              "arguments": "{\"location\":\"Boston, MA\",\"unit\":\"fahrenheit\"}",
              "name": "get_current_weather"
            },
            "id": "call_8fxy20OEu9ulvvaa5b5CzVA4",
            "type": "function"
          }
        ]
      }
    }
  ],
  "created": 1755133856,
  "id": "chatcmpl-C4GmWI2Sl7HnrQsKorSAZkJy94dpT",
  "model": "gpt-5-nano-2025-08-07",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 287,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 256,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 162,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 449
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: testopenai.CassetteChatMultimodal,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "length",
      "index": 0,
      "message": {
        "annotations": [],
        "content": "",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1755134160,
  "id": "chatcmpl-C4GrQKcrez1dN2o93xZTQ9ohbQFBY",
  "model": "gpt-5-nano-2025-08-07",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 100,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 100,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 2245,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 2345
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
      "message": {
        "annotations": [],
        "content": "What city or region?",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1755134078,
  "id": "chatcmpl-C4Gq6FIIOJIrXvd2A8RPuRIuAlHZr",
  "model": "gpt-5-nano-2025-08-07",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 526,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 512,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 50,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 576
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
      "message": {
        "annotations": [],
        "content": "{\"name\":\"Alex Smith\",\"age\":29,\"city\":\"San Francisco\"}",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1755133839,
  "id": "chatcmpl-C4GmF9uTIEqemNux4KSwWJMUTrrer",
  "model": "gpt-5-nano-2025-08-07",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 280,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 256,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 21,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 301
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
      "message": {
        "annotations": [],
        "content": null,
        "refusal": null,
        "role": "assistant",
        "tool_calls": [
          {
            "function": {
              "arguments": "{\"location\":\"San Francisco, CA\"}",
              "name": "get_current_weather"
            },
            "id": "call_Xc4Aiih37EUJvqjoZlu4Vdoi",
            "type": "function"
          }
        ]
      }
    }
  ],
  "created": 1755133847,
  "id": "chatcmpl-C4GmNWjg23NFd2DxEgJ1aMbICfhdA",
  "model": "gpt-5-nano-2025-08-07",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 219,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 192,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 162,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 381
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: testopenai.CassetteChatBadRequest,
			expectResponseBody: `{
  "error": {
    "code": "unsupported_parameter",
    "message": "Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.",
    "param": "max_tokens",
    "type": "invalid_request_error"
  }
}`,
			expectStatusCode: http.StatusBadRequest,
		},
		{
			name:               testopenai.CassetteChatUnknownModel,
			expectResponseBody: `{"type":"error","error":{"type":"OpenAIBackendError","code":"404","message":"{\n    \"error\": {\n        \"message\": \"The model ` + "`gpt-4.1-nano-wrong`" + ` does not exist or you do not have access to it.\",\n        \"type\": \"invalid_request_error\",\n        \"param\": null,\n        \"code\": \"model_not_found\"\n    }\n}\n"}}`,
			expectStatusCode:   http.StatusNotFound,
		},
		{
			name: testopenai.CassetteChatReasoning,
			expectResponseBody: `{
  "choices" : [ {
    "finish_reason" : "stop",
    "index" : 0,
    "message" : {
      "annotations" : [ ],
      "content" : "Let the cost of the ball be x dollars. Then the bat costs x + 1 dollars (since it costs $1 more than the ball).\n\nThe total cost is given by:\n  x + (x + 1) = 1.10\n\nCombine like terms:\n  2x + 1 = 1.10\n\nSubtract 1 from both sides:\n  2x = 0.10\n\nDivide by 2:\n  x = 0.05\n\nSo, the ball costs $0.05 (5 cents) and the bat costs $1.05.",
      "refusal" : null,
      "role" : "assistant"
    }
  } ],
  "created" : 1755133862,
  "id" : "chatcmpl-C4GmcU7sPLtZ16jI8fqxAtXVj1FX7",
  "model" : "o3-mini-2025-01-31",
  "object" : "chat.completion",
  "service_tier" : "default",
  "system_fingerprint" : "fp_e20469f047",
  "usage" : {
    "completion_tokens" : 264,
    "completion_tokens_details" : {
      "accepted_prediction_tokens" : 0,
      "audio_tokens" : 0,
      "reasoning_tokens" : 128,
      "rejected_prediction_tokens" : 0
    },
    "prompt_tokens" : 27,
    "prompt_tokens_details" : {
      "audio_tokens" : 0,
      "cached_tokens" : 0
    },
    "total_tokens" : 291
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
      "message": {
        "annotations": [],
        "content": "A tiny blue cartoon character.",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1755133869,
  "id": "chatcmpl-C4Gmj9b5BeLuDR5fgSeYUQobtSGV3",
  "model": "gpt-5-nano-2025-08-07",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 719,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 704,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 22,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 741
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: testopenai.CassetteChatTextToImageTool,
			expectResponseBody: `{
  "choices": [
    {
      "finish_reason": "length",
      "index": 0,
      "message": {
        "annotations": [],
        "content": "",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1755134085,
  "id": "chatcmpl-C4GqDfvxQkQLkZ367ncmnEGreKApR",
  "model": "gpt-5-nano-2025-08-07",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": null,
  "usage": {
    "completion_tokens": 150,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 150,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens": 206,
    "prompt_tokens_details": {
      "audio_tokens": 0,
      "cached_tokens": 0
    },
    "total_tokens": 356
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
        "content": "Please play the audio, and I'll describe what I hear in up to five words.",
        "refusal": null,
        "role": "assistant"
      }
    }
  ],
  "created": 1755133878,
  "id": "chatcmpl-C4GmsUaSG4ATqnzO0EmAo8RVj0QKm",
  "model": "gpt-4o-audio-preview-2025-06-03",
  "object": "chat.completion",
  "service_tier": "default",
  "system_fingerprint": "fp_0611516fca",
  "usage": {
    "completion_tokens": 17,
    "completion_tokens_details": {
      "accepted_prediction_tokens": 0,
      "audio_tokens": 0,
      "reasoning_tokens": 0,
      "rejected_prediction_tokens": 0,
      "text_tokens": 17
    },
    "prompt_tokens": 28,
    "prompt_tokens_details": {
      "audio_tokens": 1,
      "cached_tokens": 0,
      "image_tokens": 0,
      "text_tokens": 27
    },
    "total_tokens": 45
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: testopenai.CassetteChatTextToAudio,
			// TODO: This is a super hack.
			// test returns a very large audio response that we cannot fully match.
			// For now, we'll skip the body check and just verify status code.
			expectResponseBody: `SKIP_BODY_CHECK`,
			expectStatusCode:   http.StatusOK,
		},
		{
			name: testopenai.CassetteChatDetailedUsage,
			expectResponseBody: `{
  "choices" : [ {
    "finish_reason" : "length",
    "index" : 0,
    "message" : {
      "annotations" : [ ],
      "content" : "",
      "refusal" : null,
      "role" : "assistant"
    }
  } ],
  "created" : 1755137737,
  "id" : "chatcmpl-C4Hn7cxHHDt210R3NWm4trTgiLkrA",
  "model" : "gpt-5-nano-2025-08-07",
  "object" : "chat.completion",
  "service_tier" : "default",
  "system_fingerprint" : null,
  "usage" : {
    "completion_tokens" : 100,
    "completion_tokens_details" : {
      "accepted_prediction_tokens" : 0,
      "audio_tokens" : 0,
      "reasoning_tokens" : 100,
      "rejected_prediction_tokens" : 0
    },
    "prompt_tokens" : 12,
    "prompt_tokens_details" : {
      "audio_tokens" : 0,
      "cached_tokens" : 0
    },
    "total_tokens" : 112
  }
}`,
			expectStatusCode: http.StatusOK,
		},
		{
			name: testopenai.CassetteChatStreamingDetailedUsage,
			expectResponseBody: `data: {"id":"chatcmpl-C4Hq5a6K3RaPMymUv70btQNYv6SvY","object":"chat.completion.chunk","created":1755137921,"model":"gpt-5-nano-2025-08-07","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"finish_reason":null}],"usage":null,"obfuscation":"WuQNX"}

data: {"id":"chatcmpl-C4Hq5a6K3RaPMymUv70btQNYv6SvY","object":"chat.completion.chunk","created":1755137921,"model":"gpt-5-nano-2025-08-07","service_tier":"default","system_fingerprint":null,"choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":null,"obfuscation":"IPio8PDeczEmdPr"}

data: {"id":"chatcmpl-C4Hq5a6K3RaPMymUv70btQNYv6SvY","object":"chat.completion.chunk","created":1755137921,"model":"gpt-5-nano-2025-08-07","service_tier":"default","system_fingerprint":null,"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":100,"total_tokens":112,"prompt_tokens_details":{"cached_tokens":0,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":100,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}},"obfuscation":"t9"}

data: [DONE]

`,
			expectStatusCode: http.StatusOK,
		},
	}

	wasBadGateway := false
	for _, tc := range tests {
		if wasBadGateway {
			return // rather than also failing subsequent tests, which confuses root cause.
		}
		t.Run(tc.name.String(), func(t *testing.T) {
			req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d/v1", listenerPort), tc.name)
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
				if body[0] == '{' {
					assert.JSONEq(t, tc.expectResponseBody, string(body))
				} else {
					assert.Equal(t, tc.expectResponseBody, string(body))
				}
			}
		})
	}
}
