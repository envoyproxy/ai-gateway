package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func main() {
	// Define routing plan
	plan := map[string]interface{}{
		"backends":        []string{"azure-primary", "gcp-ptu"},
		"fallbackEnabled": true,
	}

	planJSON, _ := json.Marshal(plan)
	encodedPlan := base64.StdEncoding.EncodeToString(planJSON)

	// Create request
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`)
	req, _ := http.NewRequest("POST", "http://localhost:8080/v1/chat/completions", bytes.NewBuffer(body))

	// Set headers
	req.Header.Set("x-ai-eg-routing-plan", encodedPlan)
	req.Header.Set("Content-Type", "application/json")

	// Send
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	fmt.Println(string(respBody))
}
