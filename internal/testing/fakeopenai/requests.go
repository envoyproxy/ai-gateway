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

// Cassette names for testing..
const (
	CassetteChatBadModel      = "chat-bad-model"
	CassetteChatBasic         = "chat-basic"
	CassetteChatJSONMode      = "chat-json-mode"
	CassetteChatMultimodal    = "chat-multimodal"
	CassetteChatMultiturn     = "chat-multiturn"
	CassetteChatNoMessages    = "chat-no-messages"
	CassetteChatParallelTools = "chat-parallel-tools"
	CassetteChatStreaming     = "chat-streaming"
	CassetteChatTools         = "chat-tools"
	CassetteChatUnknownModel  = "chat-unknown-model"
	CassetteEdgeBadRequest    = "edge-bad-request"
	CassetteEdgeBase64Image   = "edge-base64-image"
)

// NewRequest creates a new HTTP request for the given cassette name.
// It automatically sets the method to POST, content type to application/json,
// and includes the X-Cassette-Name header for cassette selection.
func NewRequest(baseURL, cassetteName string) (*http.Request, error) {
	// Get the request body for this cassette..
	requestBody, ok := requestBodies[cassetteName]
	if !ok {
		return nil, fmt.Errorf("unknown cassette name: %s", cassetteName)
	}

	// Create the request..
	req, err := http.NewRequest(http.MethodPost, baseURL+"/chat/completions", bytes.NewReader([]byte(requestBody)))
	if err != nil {
		return nil, err
	}

	// Set headers..
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cassette-Name", cassetteName)

	return req, nil
}

// requestBodies contains the actual request body for each cassette..
// These are kept private as they should only be accessed via NewRequest..
var requestBodies = map[string]string{
	CassetteChatBasic: `{
  "model": "gpt-4.1-nano",
  "messages": [
    {
      "role": "user",
      "content": "Answer in up to 3 words: Which ocean contains Bouvet Island?"
    }
  ]
}`,
	CassetteChatStreaming: `{
  "model": "gpt-4o-mini",
  "messages": [
    {
      "role": "user",
      "content": "Answer in up to 3 words: Which ocean contains Bouvet Island?"
    }
  ],
  "stream": true,
  "stream_options": {
    "include_usage": true
  }
}`,
	CassetteChatTools: `{
  "model": "gpt-4o-mini",
  "messages": [
    {
      "role": "user",
      "content": "Answer in up to 3 words: Which ocean contains Bouvet Island?"
    }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get the current weather in a given location",
        "parameters": {
          "type": "object",
          "properties": {
            "location": {
              "type": "string",
              "description": "The city and state, e.g. San Francisco, CA"
            }
          },
          "required": ["location"]
        }
      }
    }
  ]
}`,
	CassetteChatMultimodal: `{
  "model": "gpt-4o-mini",
  "messages": [
    {
      "role": "user",
      "content": [
        {
          "type": "text",
          "text": "Answer in up to 3 words: Which ocean contains Bouvet Island?"
        },
        {
          "type": "image_url",
          "image_url": {
            "url": "https://upload.wikimedia.org/wikipedia/commons/thumb/3/3a/Cat03.jpg/320px-Cat03.jpg"
          }
        }
      ]
    }
  ],
  "max_tokens": 100
}`,
	CassetteChatMultiturn: `{
  "model": "gpt-4o-mini",
  "messages": [
    {
      "role": "system",
      "content": "You are a helpful assistant."
    },
    {
      "role": "user",
      "content": "Answer in up to 3 words: Which ocean contains Bouvet Island?"
    },
    {
      "role": "assistant",
      "content": "Southern Ocean."
    },
    {
      "role": "user",
      "content": "Answer in up to 3 words: Which ocean contains Bouvet Island?"
    }
  ],
  "temperature": 0.7
}`,
	CassetteChatJSONMode: `{
  "model": "gpt-4o-mini",
  "messages": [
    {
      "role": "user",
      "content": "Answer in up to 3 words: Which ocean contains Bouvet Island?"
    }
  ],
  "response_format": {
    "type": "json_object"
  }
}`,
	CassetteChatNoMessages: `{
  "model": "gpt-4o-mini"
}`,
	CassetteChatBadModel: `{
  "model": "invalid-model-xyz",
  "messages": [
    {
      "role": "user",
      "content": "Answer in up to 3 words: Which ocean contains Bouvet Island?"
    }
  ]
}`,
	CassetteChatParallelTools: `{
  "model": "gpt-4o-mini",
  "messages": [
    {
      "role": "user",
      "content": "Answer in up to 3 words: Which ocean contains Bouvet Island?"
    }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get weather for a location",
        "parameters": {
          "type": "object",
          "properties": {
            "location": {"type": "string"}
          },
          "required": ["location"]
        }
      }
    }
  ],
  "tool_choice": "auto",
  "parallel_tool_calls": true
}`,
	CassetteEdgeBadRequest: `{
  "model": "gpt-4o-mini",
  "messages": [
    {
      "role": "user",
      "content": null
    }
  ],
  "temperature": -0.5,
  "max_tokens": 0
}`,
	CassetteEdgeBase64Image: `{
  "model": "gpt-4o-mini",
  "messages": [
    {
      "role": "user",
      "content": [
        {
          "type": "text",
          "text": "Answer in up to 3 words: Which ocean contains Bouvet Island?"
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
      "content": "Answer in up to 3 words: Which ocean contains Bouvet Island?"
    }
  ]
}`,
}
