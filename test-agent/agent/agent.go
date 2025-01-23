package agent

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/envoyproxy/ai-gateway/internal/tool"
	"github.com/tmc/langchaingo"
)

// AgentRequest represents the request structure for the agent.
type AgentRequest struct {
	Model      string            `json:"model"`
	Action     string            `json:"action"`
	Parameters map[string]string `json:"parameters"`
}

// AgentResponse represents the response structure from the agent.
type AgentResponse struct {
	Result string `json:"result"`
}

// CallAgent handles the agent logic to call the Bedrock model and then call a tool.
func CallAgent(w http.ResponseWriter, r *http.Request) {
	var req AgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("failed to decode request: %v", err), http.StatusBadRequest)
		return
	}

	// Call the Bedrock model using langchaingo.
	bedrockResult, err := callBedrockModel(req.Model, req.Parameters)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to call Bedrock model: %v", err), http.StatusInternalServerError)
		return
	}

	// Call the tool.
	toolResult, err := tool.CallTool(req.Parameters["tool"], req.Parameters)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to call tool: %v", err), http.StatusInternalServerError)
		return
	}

	// Combine the results and send the response.
	resp := AgentResponse{
		Result: fmt.Sprintf("%s and %s", bedrockResult, toolResult),
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
	}
}

// callBedrockModel calls the Bedrock model using langchaingo.
func callBedrockModel(model string, parameters map[string]string) (string, error) {
	client := langchaingo.NewClient("your-api-key")
	request := langchaingo.Request{
		Model: model,
		Inputs: []langchaingo.Input{
			{
				Name: "input.1",
				Data: parameters,
			},
		},
	}

	response, err := client.Infer(request)
	if err != nil {
		return "", fmt.Errorf("failed to call Bedrock model: %w", err)
	}

	return response.Outputs[0].Data, nil
}
