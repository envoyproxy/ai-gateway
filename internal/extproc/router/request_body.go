package router

import (
	"encoding/json"
	"fmt"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// RequestBodyParser is a function that parses the body of the request.
type RequestBodyParser func(path string, body *extprocv3.HttpBody) (modelName string, rb RequestBody, err error)

// NewRequestBodyParser creates a new RequestBodyParser based on the schema.
func NewRequestBodyParser(inputSchema extprocconfig.VersionedAPISchema) (RequestBodyParser, error) {
	if inputSchema.Schema == extprocconfig.APISchemaOpenAI {
		return openAIParseBody, nil
	}
	return nil, fmt.Errorf("unsupported API schema: %s", inputSchema)
}

// RequestBody is the union of all request body types.
type RequestBody any

// openAIParseBody parses the body of the request for OpenAI.
func openAIParseBody(path string, body *extprocv3.HttpBody) (modelName string, rb RequestBody, err error) {
	if path == "/v1/chat/completions" {
		var openAIReq openai.ChatCompletionRequest
		if err := json.Unmarshal(body.Body, &openAIReq); err != nil {
			return "", nil, fmt.Errorf("failed to unmarshal body: %w", err)
		}
		return openAIReq.Model, &openAIReq, nil
	} else {
		return "", nil, fmt.Errorf("unsupported path: %s", path)
	}
}
