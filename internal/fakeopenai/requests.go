// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package fakeopenai

import (
	"bytes"
	"fmt"
	"net/http"
)

// CassetteName represents cassette names for testing.
type CassetteName string

// String returns the string representation of the cassette name.
func (c CassetteName) String() string {
	return string(c)
}

// Cassette names for testing.
const (
	// CassetteChatBasic is the canonical OpenAI chat completion request.
	CassetteChatBasic CassetteName = "chat-basic"

	// CassetteChatJSONMode is a chat completion request with JSON response format.
	CassetteChatJSONMode CassetteName = "chat-json-mode"

	// CassetteChatMultimodal is a multimodal chat request with text and image inputs.
	CassetteChatMultimodal CassetteName = "chat-multimodal"

	// CassetteChatMultiturn is a multi-turn conversation with message history.
	CassetteChatMultiturn CassetteName = "chat-multiturn"

	// CassetteChatNoMessages is a request missing the required messages field.
	CassetteChatNoMessages CassetteName = "chat-no-messages"

	// CassetteChatParallelTools is a chat completion with parallel function calling enabled.
	CassetteChatParallelTools CassetteName = "chat-parallel-tools"

	// CassetteChatStreaming is the canonical OpenAI chat completion request,
	// with streaming enabled.
	CassetteChatStreaming CassetteName = "chat-streaming"

	// CassetteChatTools is a chat completion request with function tools.
	CassetteChatTools CassetteName = "chat-tools"

	// CassetteChatUnknownModel is a request with a non-existent model.
	CassetteChatUnknownModel CassetteName = "chat-unknown-model"

	// CassetteChatBadRequest is a request with multiple validation errors.
	CassetteChatBadRequest CassetteName = "chat-bad-request"

	// CassetteChatBase64Image is a request with a base64-encoded image.
	CassetteChatBase64Image CassetteName = "chat-base64-image"
)

// NewRequest creates a new HTTP request for the given cassette.
//
// The returned request is an http.MethodPost with the body and
// CassetteNameHeader according to the pre-recorded cassette.
func NewRequest(baseURL string, cassetteName CassetteName) (*http.Request, error) {
	// Get the request body for this cassette.
	requestBody, ok := requestBodies[cassetteName]
	if !ok {
		return nil, fmt.Errorf("unknown cassette name: %s", cassetteName)
	}

	// Create the request.
	req, err := http.NewRequest(http.MethodPost, baseURL+"/chat/completions", bytes.NewReader([]byte(requestBody)))
	if err != nil {
		return nil, err
	}

	// Set headers.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cassette-Name", string(cassetteName))

	return req, nil
}

// requestBodies contains the actual request body for each cassette and are
// needed for re-recording the cassettes to get realistic responses.
//
// Prefer bodies in the OpenAI OpenAPI examples to making them up manually.
// See https://github.com/openai/openai-openapi/tree/manual_spec
var requestBodies = map[CassetteName]string{
	CassetteChatBasic: `{
  "model": "gpt-4.1-nano",
  "messages": [
    {
      "role": "user",
      "content": "Hello!"
    }
  ]
}`,
	CassetteChatStreaming: `{
  "model": "gpt-4.1-nano",
  "messages": [
    {
      "role": "developer",
      "content": "You are a helpful assistant."
    },
    {
      "role": "user",
      "content": "Hello!"
    }
  ],
  "stream": true,
  "stream_options": {
    "include_usage": true
  }
}`,
	CassetteChatTools: `{
  "model": "gpt-4.1-nano",
  "messages": [
    {
      "role": "user",
      "content": "What is the weather like in Boston today?"
    }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_current_weather",
        "description": "Get the current weather in a given location",
        "parameters": {
          "type": "object",
          "properties": {
            "location": {
              "type": "string",
              "description": "The city and state, e.g. San Francisco, CA"
            },
            "unit": {
              "type": "string",
              "enum": ["celsius", "fahrenheit"]
            }
          },
          "required": ["location"]
        }
      }
    }
  ],
  "tool_choice": "auto"
}`,
	CassetteChatMultimodal: `{
  "model": "gpt-4.1-nano",
  "messages": [
    {
      "role": "user",
      "content": [
        {
          "type": "text",
          "text": "What is in this image?"
        },
        {
          "type": "image_url",
          "image_url": {
            "url": "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg"
          }
        }
      ]
    }
  ],
  "max_tokens": 100
}`,
	CassetteChatMultiturn: `{
  "model": "gpt-4.1-nano",
  "messages": [
    {
      "role": "developer",
      "content": "You are a helpful assistant."
    },
    {
      "role": "user",
      "content": "Hello!"
    },
    {
      "role": "assistant",
      "content": "Hello! How can I assist you today?"
    },
    {
      "role": "user",
      "content": "What's the weather like?"
    }
  ],
  "temperature": 0.7
}`,
	CassetteChatJSONMode: `{
  "model": "gpt-4.1-nano",
  "messages": [
    {
      "role": "user",
      "content": "Generate a JSON object with three properties: name, age, and city."
    }
  ],
  "response_format": {
    "type": "json_object"
  }
}`,
	CassetteChatNoMessages: `{
  "model": "gpt-4.1-nano"
}`,
	CassetteChatParallelTools: `{
  "model": "gpt-4.1-nano",
  "messages": [
    {
      "role": "user",
      "content": "What is the weather like in San Francisco?"
    }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_current_weather",
        "description": "Get the current weather in a given location",
        "parameters": {
          "type": "object",
          "properties": {
            "location": {
              "type": "string",
              "description": "The city and state, e.g. San Francisco, CA"
            },
            "unit": {
              "type": "string",
              "enum": ["celsius", "fahrenheit"]
            }
          },
          "required": ["location"]
        }
      }
    }
  ],
  "tool_choice": "auto",
  "parallel_tool_calls": true
}`,
	CassetteChatBadRequest: `{
  "model": "gpt-4.1-nano",
  "messages": [
    {
      "role": "user",
      "content": null
    }
  ],
  "temperature": -0.5,
  "max_tokens": 0
}`,
	CassetteChatBase64Image: `{
  "model": "gpt-4.1-nano",
  "messages": [
    {
      "role": "user",
      "content": [
        {
          "type": "text",
          "text": "What's in this image?"
        },
        {
          "type": "image_url",
          "image_url": {
            "url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
          }
        }
      ]
    }
  ]
}`,
	CassetteChatUnknownModel: `{
  "model": "gpt-4.1-nano-wrong",
  "messages": [
    {
      "role": "user",
      "content": "Hello!"
    }
  ]
}`,
}
