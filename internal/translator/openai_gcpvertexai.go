// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/google/uuid"
	"google.golang.org/genai"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

const (
	LineFeedSSEDelimiter               = "\n\n"
	CarriageReturnSSEDelimiter         = "\r\r"
	CarriageReturnLineFeedSSEDelimiter = "\r\n\r\n"
)

// detectSSEDelimiter detects which SSE delimiter is being used in the data.
// It checks for delimiters in order of preference: CRLF, LF, CR.
// Returns the detected delimiter as a byte slice, or nil if no delimiter is found.
func detectSSEDelimiter(data []byte) []byte {
	if bytes.Contains(data, []byte(CarriageReturnLineFeedSSEDelimiter)) {
		return []byte(CarriageReturnLineFeedSSEDelimiter)
	}
	if bytes.Contains(data, []byte(LineFeedSSEDelimiter)) {
		return []byte(LineFeedSSEDelimiter)
	}
	if bytes.Contains(data, []byte(CarriageReturnSSEDelimiter)) {
		return []byte(CarriageReturnSSEDelimiter)
	}
	return nil
}

// gcpVertexAIError represents the structure of GCP Vertex AI error responses.
type gcpVertexAIError struct {
	Error gcpVertexAIErrorDetails `json:"error"`
}

type gcpVertexAIErrorDetails struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Status  string          `json:"status"`
	Details json.RawMessage `json:"details"`
}

// NewChatCompletionOpenAIToGCPVertexAITranslator implements [Factory] for OpenAI to GCP Gemini translation.
// This translator converts OpenAI ChatCompletion API requests to GCP Gemini API format.
func NewChatCompletionOpenAIToGCPVertexAITranslator(modelNameOverride internalapi.ModelNameOverride) OpenAIChatCompletionTranslator {
	return &openAIToGCPVertexAITranslatorV1ChatCompletion{modelNameOverride: modelNameOverride, toolCallIndex: int64(0)}
}

// openAIToGCPVertexAITranslatorV1ChatCompletion translates OpenAI Chat Completions API to GCP Vertex AI Gemini API.
// Note: This uses the Gemini native API directly, not Vertex AI's OpenAI-compatible API:
// https://cloud.google.com/vertex-ai/generative-ai/docs/model-reference/inference
type openAIToGCPVertexAITranslatorV1ChatCompletion struct {
	responseMode      geminiResponseMode
	modelNameOverride internalapi.ModelNameOverride
	stream            bool // Track if this is a streaming request.
	streamDelimiter   []byte
	bufferedBody      []byte // Buffer for incomplete JSON chunks.
	requestModel      internalapi.RequestModel
	toolCallIndex     int64
}

// RequestBody implements [OpenAIChatCompletionTranslator.RequestBody] for GCP Gemini.
// This method translates an OpenAI ChatCompletion request to a GCP Gemini API request.
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) RequestBody(_ []byte, openAIReq *openai.ChatCompletionRequest, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	o.requestModel = openAIReq.Model
	if o.modelNameOverride != "" {
		// Use modelName override if set.
		o.requestModel = o.modelNameOverride
	}

	// Set streaming flag.
	o.stream = openAIReq.Stream

	// Choose the correct endpoint based on streaming.
	var path string
	if o.stream {
		// For streaming requests, use the streamGenerateContent endpoint with SSE format.
		path = buildGCPModelPathSuffix(gcpModelPublisherGoogle, o.requestModel, gcpMethodStreamGenerateContent, "alt=sse")
	} else {
		path = buildGCPModelPathSuffix(gcpModelPublisherGoogle, o.requestModel, gcpMethodGenerateContent)
	}
	gcpReq, err := o.openAIMessageToGeminiMessage(openAIReq, o.requestModel)
	if err != nil {
		return nil, nil, fmt.Errorf("error converting OpenAI request to Gemini request: %w", err)
	}
	newBody, err = json.Marshal(gcpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling Gemini request: %w", err)
	}
	newHeaders = []internalapi.Header{
		{pathHeaderName, path},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// ResponseHeaders implements [OpenAIChatCompletionTranslator.ResponseHeaders].
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) ResponseHeaders(_ map[string]string) (
	newHeaders []internalapi.Header, err error,
) {
	if o.stream {
		// For streaming responses, set content-type to text/event-stream to match OpenAI API.
		newHeaders = []internalapi.Header{{contentTypeHeaderName, eventStreamContentType}}
	}
	return
}

// ResponseBody implements [OpenAIChatCompletionTranslator.ResponseBody] for GCP Gemini.
// This method translates a GCP Gemini API response to the OpenAI ChatCompletion format.
// GCP Vertex AI uses deterministic model mapping without virtualization, where the requested model
// is exactly what gets executed. The response does not contain a model field, so we return
// the request model that was originally sent.
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool, span tracing.ChatCompletionSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	if o.stream {
		return o.handleStreamingResponse(body, endOfStream, span)
	}

	// Non-streaming logic.
	var gcpResp genai.GenerateContentResponse
	if err = json.NewDecoder(body).Decode(&gcpResp); err != nil {
		return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("error decoding GCP response: %w", err)
	}

	responseModel = o.requestModel
	if gcpResp.ModelVersion != "" {
		// Use the model version from the response if available.
		responseModel = gcpResp.ModelVersion
	}

	// Convert to OpenAI format.
	openAIResp, err := o.geminiResponseToOpenAIMessage(gcpResp, responseModel)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("error converting GCP response to OpenAI format: %w", err)
	}

	// Marshal the OpenAI response.
	newBody, err = json.Marshal(openAIResp)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("error marshaling OpenAI response: %w", err)
	}

	// Update token usage if available.
	if gcpResp.UsageMetadata != nil {
		tokenUsage.SetInputTokens(uint32(gcpResp.UsageMetadata.PromptTokenCount))              //nolint:gosec
		tokenUsage.SetOutputTokens(uint32(gcpResp.UsageMetadata.CandidatesTokenCount))         //nolint:gosec
		tokenUsage.SetTotalTokens(uint32(gcpResp.UsageMetadata.TotalTokenCount))               //nolint:gosec
		tokenUsage.SetCachedInputTokens(uint32(gcpResp.UsageMetadata.CachedContentTokenCount)) //nolint:gosec
	}

	if span != nil {
		span.RecordResponse(openAIResp)
	}
	newHeaders = []internalapi.Header{{contentLengthHeaderName, strconv.Itoa(len(newBody))}}
	return
}

// handleStreamingResponse handles streaming responses from GCP Gemini API.
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) handleStreamingResponse(body io.Reader, endOfStream bool, span tracing.ChatCompletionSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	responseModel = o.requestModel
	// Parse GCP streaming chunks from buffered body and current input.
	chunks, err := o.parseGCPStreamingChunks(body)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("error parsing GCP streaming chunks: %w", err)
	}

	for _, chunk := range chunks {
		// Convert GCP chunk to OpenAI chunk.
		openAIChunk := o.convertGCPChunkToOpenAI(chunk)

		// Serialize to SSE format as expected by OpenAI API.
		err := serializeOpenAIChatCompletionChunk(*openAIChunk, &newBody)
		if err != nil {
			return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("error marshaling OpenAI chunk: %w", err)
		}

		if span != nil {
			span.RecordResponseChunk(openAIChunk)
		}

		// Extract token usage only in the last chunk.
		if chunk.UsageMetadata != nil && chunk.UsageMetadata.PromptTokenCount > 0 {
			// Convert usage to pointer if available.
			usage := ptr.To(geminiUsageToOpenAIUsage(chunk.UsageMetadata))

			usageChunk := openai.ChatCompletionResponseChunk{
				ID:      chunk.ResponseID,
				Created: openai.JSONUNIXTime(chunk.CreateTime),
				Object:  "chat.completion.chunk",
				Choices: []openai.ChatCompletionResponseChunkChoice{},
				// usage is nil for all chunks other than the last chunk
				Usage: usage,
				Model: o.requestModel,
			}

			// Serialize to SSE format as expected by OpenAI API.
			err := serializeOpenAIChatCompletionChunk(usageChunk, &newBody)
			if err != nil {
				return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("error marshaling OpenAI chunk: %w", err)
			}

			if span != nil {
				span.RecordResponseChunk(&usageChunk)
			}

			if chunk.UsageMetadata.PromptTokenCount >= 0 {
				tokenUsage.SetInputTokens(uint32(chunk.UsageMetadata.PromptTokenCount)) //nolint:gosec
			}
			if chunk.UsageMetadata.CandidatesTokenCount >= 0 {
				tokenUsage.SetOutputTokens(uint32(chunk.UsageMetadata.CandidatesTokenCount)) //nolint:gosec
			}
			if chunk.UsageMetadata.TotalTokenCount >= 0 {
				tokenUsage.SetTotalTokens(uint32(chunk.UsageMetadata.TotalTokenCount)) //nolint:gosec
			}
			if chunk.UsageMetadata.CachedContentTokenCount >= 0 {
				tokenUsage.SetCachedInputTokens(uint32(chunk.UsageMetadata.CachedContentTokenCount)) //nolint:gosec
			}
		}
	}

	if endOfStream {
		// Add the [DONE] marker to indicate end of stream as per OpenAI API specification.
		newBody = append(newBody, []byte("data: [DONE]\n")...)
	}
	return
}

// parseGCPStreamingChunks parses the buffered body to extract complete JSON chunks.
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) parseGCPStreamingChunks(body io.Reader) ([]genai.GenerateContentResponse, error) {
	var chunks []genai.GenerateContentResponse

	// Read all data from buffered body and new input into memory.
	bodyReader := io.MultiReader(bytes.NewReader(o.bufferedBody), body)
	allData, err := io.ReadAll(bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read streaming body: %w", err)
	}

	// If no data, return early.
	if len(allData) == 0 {
		return chunks, nil
	}

	// Detect which SSE delimiter is being used and store it for future streaming chunks.
	if o.streamDelimiter == nil {
		o.streamDelimiter = detectSSEDelimiter(allData)
	}

	// Split by the detected delimiter.
	var parts [][]byte
	if o.streamDelimiter != nil {
		parts = bytes.Split(allData, o.streamDelimiter)
	} else {
		parts = [][]byte{allData}
	}

	// Process all complete chunks (all but the last part).
	for _, part := range parts {
		part = bytes.TrimSpace(part)
		if len(part) == 0 {
			continue
		}

		// Remove "data: " prefix from SSE format if present.
		line := bytes.TrimPrefix(part, []byte("data: "))

		// Try to parse as JSON.
		var chunk genai.GenerateContentResponse
		if err := json.Unmarshal(line, &chunk); err == nil {
			chunks = append(chunks, chunk)
			o.bufferedBody = nil
		} else {
			// Failed to parse, buffer it for the next call.
			o.bufferedBody = line
		}
		// Ignore parse errors for individual chunks to maintain stream continuity.
	}

	return chunks, nil
}

// extractToolCallsFromGeminiPartsStream extracts tool calls from Gemini parts for streaming responses.
// Each tool call is assigned an incremental index starting from 0, matching OpenAI's streaming protocol.
// Returns ChatCompletionChunkChoiceDeltaToolCall types suitable for streaming responses, or nil if no tool calls are found.
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) extractToolCallsFromGeminiPartsStream(toolCalls []openai.ChatCompletionChunkChoiceDeltaToolCall, parts []*genai.Part) ([]openai.ChatCompletionChunkChoiceDeltaToolCall, error) {
	for _, part := range parts {
		if part == nil || part.FunctionCall == nil {
			continue
		}

		// Convert function call arguments to JSON string.
		args, err := json.Marshal(part.FunctionCall.Args)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal function arguments: %w", err)
		}

		// Generate a random ID for the tool call.
		toolCallID := uuid.New().String()

		toolCall := openai.ChatCompletionChunkChoiceDeltaToolCall{
			ID:   &toolCallID,
			Type: openai.ChatCompletionMessageToolCallTypeFunction,
			Function: openai.ChatCompletionMessageToolCallFunctionParam{
				Name:      part.FunctionCall.Name,
				Arguments: string(args),
			},
			Index: o.toolCallIndex,
		}
		// a new toolCall
		o.toolCallIndex++

		toolCalls = append(toolCalls, toolCall)
	}

	if len(toolCalls) == 0 {
		return nil, nil
	}

	return toolCalls, nil
}

// geminiCandidatesToOpenAIStreamingChoices converts Gemini candidates to OpenAI streaming choices.
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) geminiCandidatesToOpenAIStreamingChoices(candidates []*genai.Candidate) ([]openai.ChatCompletionResponseChunkChoice, error) {
	responseMode := o.responseMode
	choices := make([]openai.ChatCompletionResponseChunkChoice, 0, len(candidates))

	for i, candidate := range candidates {
		if candidate == nil {
			continue
		}

		// Create the streaming choice.
		choice := openai.ChatCompletionResponseChunkChoice{
			Index: int64(i),
		}

		toolCalls := []openai.ChatCompletionChunkChoiceDeltaToolCall{}
		var err error
		if candidate.Content != nil {
			delta := &openai.ChatCompletionResponseChunkChoiceDelta{
				Role: openai.ChatMessageRoleAssistant,
			}

			// Extract thought summary and text from parts for streaming (delta).
			thoughtSummary, content := extractTextAndThoughtSummaryFromGeminiParts(candidate.Content.Parts, responseMode)
			if thoughtSummary != "" {
				delta.ReasoningContent = &openai.StreamReasoningContent{
					Text: thoughtSummary,
				}
			}

			if content != "" {
				delta.Content = &content
			}

			// Extract tool calls if any.
			toolCalls, err = o.extractToolCallsFromGeminiPartsStream(toolCalls, candidate.Content.Parts)
			if err != nil {
				return nil, fmt.Errorf("error extracting tool calls: %w", err)
			}
			delta.ToolCalls = toolCalls

			choice.Delta = delta
		}
		choice.FinishReason = geminiFinishReasonToOpenAI(candidate.FinishReason, toolCalls)
		choices = append(choices, choice)
	}

	return choices, nil
}

// convertGCPChunkToOpenAI converts a GCP streaming chunk to OpenAI streaming format.
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) convertGCPChunkToOpenAI(chunk genai.GenerateContentResponse) *openai.ChatCompletionResponseChunk {
	// Convert candidates to OpenAI choices for streaming.
	choices, err := o.geminiCandidatesToOpenAIStreamingChoices(chunk.Candidates)
	if err != nil {
		// For now, create empty choices on error to prevent breaking the stream.
		choices = []openai.ChatCompletionResponseChunkChoice{}
	}

	return &openai.ChatCompletionResponseChunk{
		ID:      chunk.ResponseID,
		Created: openai.JSONUNIXTime(chunk.CreateTime),
		Object:  "chat.completion.chunk",
		Choices: choices,
		// usage is nil for all chunks other than the last chunk
		Usage: nil,
		Model: o.requestModel,
	}
}

// NewGenerationConfigThinkingConfig converts a ThinkingUnion to GenerationConfigThinkingConfig.
// It maps the values from the populated field of the union to the target struct.
func getGenerationConfigThinkingConfig(tu *openai.ThinkingUnion) *genai.ThinkingConfig {
	if tu == nil {
		return nil
	}

	result := &genai.ThinkingConfig{}

	if tu.OfEnabled != nil {

		result.IncludeThoughts = tu.OfEnabled.IncludeThoughts

		// Convert int64 to int32,
		//nolint:gosec // G115: BudgetTokens is known to be within int32 range.
		budget := int32(tu.OfEnabled.BudgetTokens)
		result.ThinkingBudget = &budget
	} else if tu.OfDisabled != nil {
		// If thinking is disabled, the target config should have default values.
		// The `omitempty` tags will ensure they aren't marshaled.
		result.IncludeThoughts = false
		result.ThinkingBudget = nil
	}

	return result
}

// openAIMessageToGeminiMessage converts an OpenAI ChatCompletionRequest to a GCP Gemini GenerateContentRequest.
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) openAIMessageToGeminiMessage(openAIReq *openai.ChatCompletionRequest, requestModel internalapi.RequestModel) (*gcp.GenerateContentRequest, error) {
	// Convert OpenAI messages to Gemini Contents and SystemInstruction.
	contents, systemInstruction, err := openAIMessagesToGeminiContents(openAIReq.Messages, requestModel)
	if err != nil {
		return nil, err
	}

	// Some models support only partialJSONSchema.
	parametersJSONSchemaAvailable := responseJSONSchemaAvailable(requestModel)
	// Convert OpenAI tools to Gemini tools.
	tools, err := openAIToolsToGeminiTools(openAIReq.Tools, parametersJSONSchemaAvailable)
	if err != nil {
		return nil, fmt.Errorf("error converting tools: %w", err)
	}

	// Convert tool config.
	toolConfig, err := openAIToolChoiceToGeminiToolConfig(openAIReq.ToolChoice)
	if err != nil {
		return nil, fmt.Errorf("error converting tool choice: %w", err)
	}

	// Convert generation config.
	generationConfig, responseMode, err := openAIReqToGeminiGenerationConfig(openAIReq, requestModel)
	if err != nil {
		return nil, fmt.Errorf("error converting generation config: %w", err)
	}
	o.responseMode = responseMode

	gcr := gcp.GenerateContentRequest{
		Contents:          contents,
		Tools:             tools,
		ToolConfig:        toolConfig,
		GenerationConfig:  generationConfig,
		SystemInstruction: systemInstruction,
	}
	if openAIReq.Thinking != nil {
		gcr.GenerationConfig.ThinkingConfig = getGenerationConfigThinkingConfig(openAIReq.Thinking)
	}

	// Apply vendor-specific fields after standard OpenAI-to-Gemini translation.
	// Vendor fields take precedence over translated fields when conflicts occur.
	o.applyVendorSpecificFields(openAIReq, &gcr, requestModel)

	return &gcr, nil
}

// applyVendorSpecificFields applies GCP Vertex AI vendor-specific fields to the Gemini request.
// These fields allow users to access advanced GCP-specific features not available in the OpenAI API.
// Vendor fields override any conflicting fields that were set during the standard translation process.
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) applyVendorSpecificFields(openAIReq *openai.ChatCompletionRequest, gcr *gcp.GenerateContentRequest, requestModel internalapi.RequestModel) {
	// Early return if no vendor fields are specified.
	if openAIReq.GCPVertexAIVendorFields == nil {
		return
	}

	gcpVendorFields := openAIReq.GCPVertexAIVendorFields
	// Apply vendor-specific generation config if present.
	if vendorGenConfig := gcpVendorFields.GenerationConfig; vendorGenConfig != nil {
		if gcr.GenerationConfig == nil {
			gcr.GenerationConfig = &genai.GenerationConfig{}
		}
		if vendorGenConfig.MediaResolution != "" && mediaResolutionAvailable(requestModel) {
			gcr.GenerationConfig.MediaResolution = vendorGenConfig.MediaResolution
		}
	}
	if gcpVendorFields.SafetySettings != nil {
		gcr.SafetySettings = gcpVendorFields.SafetySettings
	}
}

func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) geminiResponseToOpenAIMessage(gcr genai.GenerateContentResponse, responseModel string) (*openai.ChatCompletionResponse, error) {
	// Convert candidates to OpenAI choices.
	choices, err := geminiCandidatesToOpenAIChoices(gcr.Candidates, o.responseMode)
	if err != nil {
		return nil, fmt.Errorf("error converting choices: %w", err)
	}

	// Set up the OpenAI response.
	openaiResp := &openai.ChatCompletionResponse{
		ID:      gcr.ResponseID,
		Model:   responseModel,
		Choices: choices,
		Object:  "chat.completion",
		Created: openai.JSONUNIXTime(gcr.CreateTime),
		Usage:   geminiUsageToOpenAIUsage(gcr.UsageMetadata),
	}

	return openaiResp, nil
}

// translateGCPVertexAIErrorToOpenAI translates GCP Vertex AI error responses to OpenAI error format.
// This is a shared helper function used by multiple GCP Vertex AI translators.
// GCP error responses typically contain JSON with error details or plain text error messages.
func translateGCPVertexAIErrorToOpenAI(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	var buf []byte
	buf, err = io.ReadAll(body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read error body: %w", err)
	}

	// Assume all responses have a valid status code header.
	statusCode := respHeaders[statusHeaderName]

	openaiError := openai.Error{
		Type: "error",
		Error: openai.ErrorType{
			Type: gcpVertexAIBackendError,
			Code: &statusCode,
		},
	}

	var gcpError gcpVertexAIError
	// Try to parse as GCP error response structure.
	if err = json.Unmarshal(buf, &gcpError); err == nil {
		errMsg := gcpError.Error.Message
		if len(gcpError.Error.Details) > 0 {
			// If details are present and not null, append them to the error message.
			errMsg = fmt.Sprintf("Error: %s\nDetails: %s", errMsg, string(gcpError.Error.Details))
		}
		openaiError.Error.Type = gcpError.Error.Status
		openaiError.Error.Message = errMsg
	} else {
		// If not JSON, read the raw body as the error message.
		openaiError.Error.Message = string(buf)
	}

	newBody, err = json.Marshal(openaiError)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
	}
	newHeaders = []internalapi.Header{
		{contentTypeHeaderName, jsonContentType},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// ResponseError implements [OpenAIChatCompletionTranslator.ResponseError].
// Translate GCP Vertex AI exceptions to OpenAI error type.
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	return translateGCPVertexAIErrorToOpenAI(respHeaders, body)
}
