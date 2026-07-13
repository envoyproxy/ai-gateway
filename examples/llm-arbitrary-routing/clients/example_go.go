package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// RoutingPlan defines which backends to try and in what order.
type RoutingPlan struct {
	Backends        []string `json:"backends"`
	FallbackEnabled bool     `json:"fallbackEnabled"`
}

// ChatRequest is a simplified OpenAI chat completion request.
type ChatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func main() {
	// Define routing plan: try Azure first, fall back to GCP
	plan := RoutingPlan{
		Backends:        []string{"azure-primary", "gcp-ptu"},
		FallbackEnabled: true,
	}

	// Encode as base64 JSON
	planJSON, err := json.Marshal(plan)
	if err != nil {
		panic(err)
	}
	encodedPlan := base64.StdEncoding.EncodeToString(planJSON)

	// Create chat completion request
	chatReq := ChatRequest{
		Model: "gpt-4",
		Messages: []struct {
			Role    string
			Content string
		}{
			{
				Role:    "user",
				Content: "What is the capital of France?",
			},
		},
	}

	reqBody, err := json.Marshal(chatReq)
	if err != nil {
		panic(err)
	}

	// Create HTTP request
	req, err := http.NewRequest(
		"POST",
		"http://localhost:8080/v1/chat/completions",
		bytes.NewBuffer(reqBody),
	)
	if err != nil {
		panic(err)
	}

	// Set headers
	req.Header.Set("x-ai-eg-routing-plan", encodedPlan)
	req.Header.Set("Content-Type", "application/json")

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(body))
}

// Example output on fallback:
// Attempt 1: x-ai-eg-routing-plan directs to azure-primary
//   -> DFP sets :authority=snc-oai-llmproxy-dev-eastus2.openai.azure.com
//   -> Azure responds 503
//   -> Envoy retries
// Attempt 2: AI Gateway picks backends[1] (gcp-ptu)
//   -> DFP sets :authority=us-central1-aiplatform.googleapis.com
//   -> GCP returns 200 ✓
