// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai_test

import (
	"encoding/json"
	"fmt"

	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// Example_basicResponseRequest demonstrates a basic Responses API request
func Example_basicResponseRequest() {
	req := openai.ResponseRequest{
		Model: "gpt-4o",
		Input: openai.ResponseInputUnion{
			Value: "What is the capital of France?",
		},
		Temperature: ptr.To(0.7),
	}

	data, _ := json.MarshalIndent(req, "", "  ")
	fmt.Println(string(data))
	// Output:
	// {
	//   "input": "What is the capital of France?",
	//   "model": "gpt-4o",
	//   "temperature": 0.7
	// }
}

// Example_responseWithFunctionCalling demonstrates function calling
func Example_responseWithFunctionCalling() {
	req := openai.ResponseRequest{
		Model: "gpt-4o",
		Input: openai.ResponseInputUnion{
			Value: "What's the weather in San Francisco?",
		},
		Tools: []openai.ResponseTool{
			{
				Type: "function",
				Function: &openai.ResponseToolFunction{
					Name:        "get_weather",
					Description: "Get current weather for a location",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"location": map[string]string{
								"type":        "string",
								"description": "The city name",
							},
						},
						"required": []string{"location"},
					},
				},
			},
		},
	}

	data, _ := json.MarshalIndent(req, "", "  ")
	fmt.Println(string(data))
	// Output shows a properly formatted request with function tool
}

// Example_responseWithCodeInterpreter demonstrates code interpreter usage
func Example_responseWithCodeInterpreter() {
	req := openai.ResponseRequest{
		Model: "gpt-4.1",
		Input: openai.ResponseInputUnion{
			Value: "Solve the equation 3x + 11 = 14",
		},
		Instructions: "You are a math tutor. Write and run code to solve problems.",
		Tools: []openai.ResponseTool{
			{
				Type: "code_interpreter",
				Container: &openai.ResponseToolContainer{
					Type: "auto",
				},
			},
		},
	}

	data, _ := json.MarshalIndent(req, "", "  ")
	fmt.Println(string(data))
	// Output shows request with code_interpreter tool
}

// Example_responseChaining demonstrates conversation chaining
func Example_responseChaining() {
	// First request
	req1 := openai.ResponseRequest{
		Model: "gpt-4o",
		Input: openai.ResponseInputUnion{
			Value: "What is quantum computing?",
		},
	}

	// Simulate getting a response with ID "resp_abc123"
	_ = req1

	// Continue the conversation
	req2 := openai.ResponseRequest{
		PreviousResponseID: "resp_abc123",
		Input: openai.ResponseInputUnion{
			Value: "Explain it to a college freshman",
		},
	}

	data, _ := json.MarshalIndent(req2, "", "  ")
	fmt.Println(string(data))
	// Output:
	// {
	//   "input": "Explain it to a college freshman",
	//   "previous_response_id": "resp_abc123"
	// }
}

// Example_responseWithReasoning demonstrates reasoning model configuration
func Example_responseWithReasoning() {
	req := openai.ResponseRequest{
		Model: "o3-mini",
		Input: openai.ResponseInputUnion{
			Value: "Solve this complex problem step by step",
		},
		Reasoning: &openai.ResponseReasoning{
			Effort: "high",
		},
	}

	data, _ := json.MarshalIndent(req, "", "  ")
	fmt.Println(string(data))
	// Output:
	// {
	//   "input": "Solve this complex problem step by step",
	//   "model": "o3-mini",
	//   "reasoning": {
	//     "effort": "high"
	//   }
	// }
}

// Example_responseUnmarshal demonstrates unmarshaling a response
func Example_responseUnmarshal() {
	jsonData := []byte(`{
		"id": "resp_abc123",
		"object": "response",
		"created_at": 1741408624,
		"model": "gpt-4o",
		"status": "completed",
		"output": [
			{
				"type": "message",
				"role": "assistant",
				"content": "The capital of France is Paris."
			}
		],
		"output_text": "The capital of France is Paris.",
		"usage": {
			"input_tokens": 10,
			"output_tokens": 8,
			"total_tokens": 18
		}
	}`)

	var resp openai.ResponseResponse
	if err := json.Unmarshal(jsonData, &resp); err != nil {
		fmt.Printf("Failed to unmarshal: %v\n", err)
		return
	}

	fmt.Printf("Status: %s\n", resp.Status)
	fmt.Printf("Output Text: %s\n", resp.OutputText)
	fmt.Printf("Total Tokens: %d\n", resp.Usage.TotalTokens)
	// Output:
	// Status: completed
	// Output Text: The capital of France is Paris.
	// Total Tokens: 18
}

// Example_arrayInput demonstrates using array input with multiple items
func Example_arrayInput() {
	req := openai.ResponseRequest{
		Model: "gpt-4o",
		Input: openai.ResponseInputUnion{
			Value: []openai.ResponseInputItem{
				{
					Type:    "message",
					Role:    "user",
					Content: "Hello!",
				},
				{
					Type:    "message",
					Role:    "assistant",
					Content: "Hi! How can I help you?",
				},
				{
					Type:    "message",
					Role:    "user",
					Content: "What's the weather?",
				},
			},
		},
	}

	data, _ := json.MarshalIndent(req, "", "  ")
	fmt.Println(string(data))
	// Output shows request with array of input items
}
