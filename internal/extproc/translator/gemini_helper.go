// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"mime"
	"net/url"
	"path"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const (
	gcpModelPublisherGoogle        = "google"
	gcpModelPublisherAnthropic     = "anthropic"
	gcpMethodGenerateContent       = "generateContent"
	gcpMethodStreamGenerateContent = "streamGenerateContent"
	gcpMethodRawPredict            = "rawPredict"
	httpHeaderKeyContentLength     = "Content-Length"
)

// geminiResponseMode represents the type of response mode for Gemini requests
type geminiResponseMode string

const (
	responseModeNone  geminiResponseMode = "NONE"
	responseModeText  geminiResponseMode = "TEXT"
	responseModeJSON  geminiResponseMode = "JSON"
	responseModeEnum  geminiResponseMode = "ENUM"
	responseModeRegex geminiResponseMode = "REGEX"
)

// -------------------------------------------------------------
// Request Conversion Helper for OpenAI to GCP Gemini Translator
// -------------------------------------------------------------.

// openAIMessagesToGeminiContents converts OpenAI messages to Gemini Contents and SystemInstruction.
func openAIMessagesToGeminiContents(messages []openai.ChatCompletionMessageParamUnion) ([]genai.Content, *genai.Content, error) {
	var gcpContents []genai.Content
	var systemInstruction *genai.Content
	knownToolCalls := make(map[string]string)
	var gcpParts []*genai.Part

	for _, msgUnion := range messages {
		switch {
		case msgUnion.OfDeveloper != nil:
			msg := msgUnion.OfDeveloper
			inst, err := developerMsgToGeminiParts(*msg)
			if err != nil {
				return nil, nil, fmt.Errorf("error converting developer message: %w", err)
			}
			if len(inst) != 0 {
				if systemInstruction == nil {
					systemInstruction = &genai.Content{}
				}
				systemInstruction.Parts = append(systemInstruction.Parts, inst...)
			}
		case msgUnion.OfSystem != nil:
			msg := msgUnion.OfSystem
			devMsg := systemMsgToDeveloperMsg(*msg)
			inst, err := developerMsgToGeminiParts(devMsg)
			if err != nil {
				return nil, nil, fmt.Errorf("error converting developer message: %w", err)
			}
			if len(inst) != 0 {
				if systemInstruction == nil {
					systemInstruction = &genai.Content{}
				}
				systemInstruction.Parts = append(systemInstruction.Parts, inst...)
			}
		case msgUnion.OfUser != nil:
			msg := msgUnion.OfUser
			parts, err := userMsgToGeminiParts(*msg)
			if err != nil {
				return nil, nil, fmt.Errorf("error converting user message: %w", err)
			}
			gcpParts = append(gcpParts, parts...)
		case msgUnion.OfTool != nil:
			msg := msgUnion.OfTool
			part, err := toolMsgToGeminiParts(*msg, knownToolCalls)
			if err != nil {
				return nil, nil, fmt.Errorf("error converting tool message: %w", err)
			}
			gcpParts = append(gcpParts, part)
		case msgUnion.OfAssistant != nil:
			// Flush any accumulated user/tool parts before assistant.
			if len(gcpParts) > 0 {
				gcpContents = append(gcpContents, genai.Content{Role: genai.RoleUser, Parts: gcpParts})
				gcpParts = nil
			}
			msg := msgUnion.OfAssistant
			assistantParts, toolCalls, err := assistantMsgToGeminiParts(*msg)
			if err != nil {
				return nil, nil, fmt.Errorf("error converting assistant message: %w", err)
			}
			maps.Copy(knownToolCalls, toolCalls)
			gcpContents = append(gcpContents, genai.Content{Role: genai.RoleModel, Parts: assistantParts})
		default:
			return nil, nil, fmt.Errorf("invalid role in message")
		}
	}

	// If there are any remaining parts after processing all messages, add them as user content.
	if len(gcpParts) > 0 {
		gcpContents = append(gcpContents, genai.Content{Role: genai.RoleUser, Parts: gcpParts})
	}
	return gcpContents, systemInstruction, nil
}

// developerMsgToGeminiParts converts OpenAI developer message to Gemini Content.
func developerMsgToGeminiParts(msg openai.ChatCompletionDeveloperMessageParam) ([]*genai.Part, error) {
	var parts []*genai.Part

	switch contentValue := msg.Content.Value.(type) {
	case string:
		if contentValue != "" {
			parts = append(parts, genai.NewPartFromText(contentValue))
		}
	case []openai.ChatCompletionContentPartTextParam:
		if len(contentValue) > 0 {
			for _, textParam := range contentValue {
				if textParam.Text != "" {
					parts = append(parts, genai.NewPartFromText(textParam.Text))
				}
			}
		}
	default:
		return nil, fmt.Errorf("unsupported content type in developer message: %T", contentValue)

	}
	return parts, nil
}

// userMsgToGeminiParts converts OpenAI user message to Gemini Parts.
func userMsgToGeminiParts(msg openai.ChatCompletionUserMessageParam) ([]*genai.Part, error) {
	var parts []*genai.Part
	switch contentValue := msg.Content.Value.(type) {
	case string:
		if contentValue != "" {
			parts = append(parts, genai.NewPartFromText(contentValue))
		}
	case []openai.ChatCompletionContentPartUserUnionParam:
		for _, content := range contentValue {
			switch {
			case content.OfText != nil:
				parts = append(parts, genai.NewPartFromText(content.OfText.Text))
			case content.OfImageURL != nil:
				imgURL := content.OfImageURL.ImageURL.URL
				if imgURL == "" {
					// If image URL is empty, we skip it.
					continue
				}

				parsedURL, err := url.Parse(imgURL)
				if err != nil {
					return nil, fmt.Errorf("invalid image URL: %w", err)
				}

				if parsedURL.Scheme == "data" {
					mimeType, imgBytes, err := parseDataURI(imgURL)
					if err != nil {
						return nil, fmt.Errorf("failed to parse data URI: %w", err)
					}
					parts = append(parts, genai.NewPartFromBytes(imgBytes, mimeType))
				} else {
					// Identify mimeType based in image url.
					mimeType := mimeTypeImageJPEG // Default to jpeg if unknown.
					if mt := mime.TypeByExtension(path.Ext(imgURL)); mt != "" {
						mimeType = mt
					}

					parts = append(parts, genai.NewPartFromURI(imgURL, mimeType))
				}
			case content.OfInputAudio != nil:
				// Audio content is currently not supported in this implementation.
				return nil, fmt.Errorf("audio content not supported yet")
			case content.OfFile != nil:
				// File content is currently not supported in this implementation.
				return nil, fmt.Errorf("file content not supported yet")
			}
		}
	default:
		return nil, fmt.Errorf("unsupported content type in user message: %T", contentValue)
	}
	return parts, nil
}

// toolMsgToGeminiParts converts OpenAI tool message to Gemini Parts.
func toolMsgToGeminiParts(msg openai.ChatCompletionToolMessageParam, knownToolCalls map[string]string) (*genai.Part, error) {
	var part *genai.Part
	name := knownToolCalls[msg.ToolCallID]
	funcResponse := ""
	switch contentValue := msg.Content.Value.(type) {
	case string:
		funcResponse = contentValue
	case []openai.ChatCompletionContentPartTextParam:
		for _, textParam := range contentValue {
			if textParam.Text != "" {
				funcResponse += textParam.Text
			}
		}
	default:
		return nil, fmt.Errorf("unsupported content type in tool message: %T", contentValue)
	}

	part = genai.NewPartFromFunctionResponse(name, map[string]any{"output": funcResponse})
	return part, nil
}

// assistantMsgToGeminiParts converts OpenAI assistant message to Gemini Parts and known tool calls.
func assistantMsgToGeminiParts(msg openai.ChatCompletionAssistantMessageParam) ([]*genai.Part, map[string]string, error) {
	var parts []*genai.Part

	// Handle tool calls in the assistant message.
	knownToolCalls := make(map[string]string)
	for i, toolCall := range msg.ToolCalls {
		knownToolCalls[*toolCall.ID] = toolCall.Function.Name
		
		argsStr := strings.TrimSpace(toolCall.Function.Arguments)
		
		// Detailed logging for debugging
		slog.Debug("processing tool call",
			"index", i,
			"tool_name", toolCall.Function.Name,
			"tool_id", *toolCall.ID,
			"args_length", len(argsStr),
			"args_first_100", truncateString(argsStr, 100))
		
		// Check for common serialization errors
		if strings.Contains(argsStr, "}{") {
			slog.Error("detected multiple JSON objects in tool call arguments",
				"tool_name", toolCall.Function.Name,
				"tool_id", *toolCall.ID,
				"full_args", argsStr)
			return nil, nil, fmt.Errorf("tool call '%s' contains multiple concatenated JSON objects (found '}{' pattern): %q",
				toolCall.Function.Name, argsStr)
		}
		
		// Check for empty or malformed start
		if len(argsStr) > 0 && !strings.HasPrefix(argsStr, "{") {
			slog.Error("tool call arguments don't start with '{'",
				"tool_name", toolCall.Function.Name,
				"tool_id", *toolCall.ID,
				"first_char", string(argsStr[0]),
				"full_args", argsStr)
			return nil, nil, fmt.Errorf("tool call '%s' arguments must be a JSON object starting with '{': %q",
				toolCall.Function.Name, argsStr)
		}
		
		var parsedArgs map[string]any
		if err := json.Unmarshal([]byte(argsStr), &parsedArgs); err != nil {
			// Enhanced error with context
			var syntaxErr *json.SyntaxError
			if errors.As(err, &syntaxErr) {
				// Show the problematic area
				start := maxInt(0, int(syntaxErr.Offset)-50)
				end := minInt(len(argsStr), int(syntaxErr.Offset)+50)
				context := argsStr[start:end]
				
				slog.Error("JSON syntax error in tool call arguments",
					"tool_name", toolCall.Function.Name,
					"tool_id", *toolCall.ID,
					"error_offset", syntaxErr.Offset,
					"error_context", context,
					"full_args", argsStr)
				
				return nil, nil, fmt.Errorf("invalid JSON in tool call '%s' at position %d: %w. Context: %q. Full: %q",
					toolCall.Function.Name, syntaxErr.Offset, err, context, argsStr)
			}
			
			slog.Error("failed to parse tool call arguments as JSON",
				"tool_name", toolCall.Function.Name,
				"tool_id", *toolCall.ID,
				"error", err.Error(),
				"full_args", argsStr)
			
			return nil, nil, fmt.Errorf("function arguments should be valid json string. failed to parse function arguments for tool '%s' (id: %s): %w. Arguments: %q",
				toolCall.Function.Name, *toolCall.ID, err, argsStr)
		}
		
		slog.Debug("successfully parsed tool call arguments",
			"tool_name", toolCall.Function.Name,
			"parsed_keys", getMapKeys(parsedArgs))
		
		parts = append(parts, genai.NewPartFromFunctionCall(toolCall.Function.Name, parsedArgs))
	}

	// Handle content in the assistant message.
	switch v := msg.Content.Value.(type) {
	case string:
		if v != "" {
			parts = append(parts, genai.NewPartFromText(v))
		}
	case []openai.ChatCompletionAssistantMessageParamContent:
		for _, contPart := range v {
			switch contPart.Type {
			case openai.ChatCompletionAssistantMessageParamContentTypeText:
				if contPart.Text != nil && *contPart.Text != "" {
					parts = append(parts, genai.NewPartFromText(*contPart.Text))
				}
			case openai.ChatCompletionAssistantMessageParamContentTypeRefusal:
				// Refusal messages are currently ignored in this implementation.
			default:
				return nil, nil, fmt.Errorf("unsupported content type in assistant message: %s", contPart.Type)
			}
		}
	case nil:
		// No content provided, this is valid.
	default:
		return nil, nil, fmt.Errorf("unsupported content type in assistant message: %T", v)
	}

	return parts, knownToolCalls, nil
}

// openAIToolsToGeminiTools converts OpenAI tools to Gemini tools.
// This function combines all the openai tools into a single Gemini Tool as distinct function declarations.
// This is mainly done because some Gemini models do not support multiple tools in a single request.
// This behavior might need to change in future based on model capabilities.
// Example Input
// [
//
//	{
//	  "type": "function",
//	  "function": {
//	    "name": "add",
//	    "description": "Add two numbers",
//	    "parameters": {
//	      "properties": {
//	        "a": {
//	          "type": "integer"
//	        },
//	        "b": {
//	          "type": "integer"
//	        }
//	      },
//	      "required": [
//	        "a",
//	        "b"
//	      ],
//	      "type": "object"
//	    }
//	  }
//	}
//
// ]
//
// Example Output
// [
//
//	{
//	  "functionDeclarations": [
//	    {
//	      "description": "Add two numbers",
//	      "name": "add",
//	      "parametersJsonSchema": {
//	        "properties": {
//	          "a": {
//	            "type": "integer"
//	          },
//	          "b": {
//	            "type": "integer"
//	          }
//	        },
//	        "required": [
//	          "a",
//	          "b"
//	        ],
//	        "type": "object"
//	      }
//	    }
//	  ]
//	}
//
// ].
func openAIToolsToGeminiTools(openaiTools []openai.Tool) ([]genai.Tool, error) {
	if len(openaiTools) == 0 {
		return nil, nil
	}
	var functionDecls []*genai.FunctionDeclaration
	for _, tool := range openaiTools {
		if tool.Type == openai.ToolTypeFunction {
			if tool.Function != nil {
				functionDecl := &genai.FunctionDeclaration{
					Name:                 tool.Function.Name,
					Description:          tool.Function.Description,
					ParametersJsonSchema: tool.Function.Parameters,
				}
				functionDecls = append(functionDecls, functionDecl)
			}
		}
	}
	if len(functionDecls) == 0 {
		return nil, nil
	}
	return []genai.Tool{{FunctionDeclarations: functionDecls}}, nil
}

// openAIToolChoiceToGeminiToolConfig converts OpenAI tool_choice to Gemini ToolConfig.
// Example Input
//
//	{
//	 "type": "function",
//	 "function": {
//	   "name": "myfunc"
//	 }
//	}
//
// Example Output
//
//	{
//	 "functionCallingConfig": {
//	   "mode": "ANY",
//	   "allowedFunctionNames": [
//	     "myfunc"
//	   ]
//	 }
//	}
func openAIToolChoiceToGeminiToolConfig(toolChoice *openai.ChatCompletionToolChoiceUnion) (*genai.ToolConfig, error) {
	if toolChoice == nil {
		return nil, nil
	}
	switch tc := toolChoice.Value.(type) {
	case string:
		switch tc {
		case "auto":
			return &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAuto}}, nil
		case "none":
			return &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeNone}}, nil
		case "required":
			return &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAny}}, nil
		default:
			return nil, fmt.Errorf("unsupported tool choice: '%s'", tc)
		}
	case openai.ChatCompletionNamedToolChoice:
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode:                 genai.FunctionCallingConfigModeAny,
				AllowedFunctionNames: []string{tc.Function.Name},
			},
			RetrievalConfig: nil,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported tool choice type: %T", toolChoice)
	}
}

// it only works with gemini2.5 according to https://ai.google.dev/gemini-api/docs/structured-output#json-schema, separate it as a small function to make it easier to maintain
func responseJSONSchemaAvailable(requestModel internalapi.RequestModel) bool {
	return strings.Contains(requestModel, "gemini") && strings.Contains(requestModel, "2.5")
}

// openAIReqToGeminiGenerationConfig converts OpenAI request to Gemini GenerationConfig.
func openAIReqToGeminiGenerationConfig(openAIReq *openai.ChatCompletionRequest, requestModel internalapi.RequestModel) (*genai.GenerationConfig, geminiResponseMode, error) {
	responseMode := responseModeNone
	gc := &genai.GenerationConfig{}
	if openAIReq.Temperature != nil {
		f := float32(*openAIReq.Temperature)
		gc.Temperature = &f
	}
	if openAIReq.TopP != nil {
		f := float32(*openAIReq.TopP)
		gc.TopP = &f
	}

	if openAIReq.Seed != nil {
		seed := int32(*openAIReq.Seed) // nolint:gosec
		gc.Seed = &seed
	}

	if openAIReq.TopLogProbs != nil {
		logProbs := int32(*openAIReq.TopLogProbs) // nolint:gosec
		gc.Logprobs = &logProbs
	}

	if openAIReq.LogProbs != nil {
		gc.ResponseLogprobs = *openAIReq.LogProbs
	}

	formatSpecifiedCount := 0

	if openAIReq.ResponseFormat != nil {
		formatSpecifiedCount++
		switch {
		case openAIReq.ResponseFormat.OfText != nil:
			responseMode = responseModeText
			gc.ResponseMIMEType = mimeTypeTextPlain
		case openAIReq.ResponseFormat.OfJSONObject != nil:
			responseMode = responseModeJSON
			gc.ResponseMIMEType = mimeTypeApplicationJSON
		case openAIReq.ResponseFormat.OfJSONSchema != nil:
			gc.ResponseMIMEType = mimeTypeApplicationJSON
			var schemaMap map[string]any
			if err := json.Unmarshal([]byte(openAIReq.ResponseFormat.OfJSONSchema.JSONSchema.Schema), &schemaMap); err != nil {
				return nil, responseMode, fmt.Errorf("invalid JSON schema: %w", err)
			}

			responseMode = responseModeJSON

			if responseJSONSchemaAvailable(requestModel) {
				gc.ResponseJsonSchema = schemaMap
			} else {
				convertedSchema, err := jsonSchemaToGemini(schemaMap)
				if err != nil {
					return nil, responseMode, fmt.Errorf("invalid JSON schema: %w", err)
				}
				gc.ResponseSchema = convertedSchema

			}
		}
	}

	if openAIReq.GuidedChoice != nil {
		formatSpecifiedCount++
		if existSchema := gc.ResponseSchema != nil || gc.ResponseJsonSchema != nil; existSchema {
			return nil, responseMode, fmt.Errorf("duplicate json scheme specifications")
		}

		responseMode = responseModeEnum
		gc.ResponseMIMEType = mimeTypeApplicationEnum
		gc.ResponseSchema = &genai.Schema{Type: "STRING", Enum: openAIReq.GuidedChoice}
	}
	if openAIReq.GuidedRegex != "" {
		formatSpecifiedCount++
		if existSchema := gc.ResponseSchema != nil || gc.ResponseJsonSchema != nil; existSchema {
			return nil, responseMode, fmt.Errorf("duplicate json scheme specifications")
		}
		responseMode = responseModeRegex
		gc.ResponseMIMEType = mimeTypeApplicationJSON
		gc.ResponseSchema = &genai.Schema{Type: "STRING", Pattern: openAIReq.GuidedRegex}
	}
	if openAIReq.GuidedJSON != nil {
		formatSpecifiedCount++
		if existSchema := gc.ResponseSchema != nil || gc.ResponseJsonSchema != nil; existSchema {
			return nil, responseMode, fmt.Errorf("duplicate json scheme specifications")
		}
		responseMode = responseModeJSON

		gc.ResponseMIMEType = mimeTypeApplicationJSON
		gc.ResponseJsonSchema = openAIReq.GuidedJSON
	}

	// ResponseFormat and guidedJSON/guidedChoice/guidedRegex are mutually exclusive.
	// Verify only one is specified.
	if formatSpecifiedCount > 1 {
		return nil, responseMode, fmt.Errorf("multiple format specifiers specified. only one of responseFormat, guidedChoice, guidedRegex, guidedJSON can be specified")
	}

	if openAIReq.N != nil {
		gc.CandidateCount = int32(*openAIReq.N) // nolint:gosec
	}
	if openAIReq.MaxTokens != nil {
		gc.MaxOutputTokens = int32(*openAIReq.MaxTokens) // nolint:gosec
	}
	if openAIReq.PresencePenalty != nil {
		gc.PresencePenalty = openAIReq.PresencePenalty
	}
	if openAIReq.FrequencyPenalty != nil {
		gc.FrequencyPenalty = openAIReq.FrequencyPenalty
	}
	if openAIReq.Stop.OfString.Valid() {
		gc.StopSequences = []string{openAIReq.Stop.OfString.String()}
	} else if openAIReq.Stop.OfStringArray != nil {
		gc.StopSequences = openAIReq.Stop.OfStringArray
	}
	return gc, responseMode, nil
}

// --------------------------------------------------------------
// Response Conversion Helper for GCP Gemini to OpenAI Translator
// --------------------------------------------------------------.

// geminiCandidatesToOpenAIChoices converts Gemini candidates to OpenAI choices.
func geminiCandidatesToOpenAIChoices(candidates []*genai.Candidate, responseMode geminiResponseMode) ([]openai.ChatCompletionResponseChoice, error) {
	choices := make([]openai.ChatCompletionResponseChoice, 0, len(candidates))

	for idx, candidate := range candidates {
		if candidate == nil {
			continue
		}

		// Skip candidates that only contain empty content with finishReason STOP
		// These are completion signals from Gemini, not actual content
		// This happens in mode ANY when Gemini sends a second candidate with empty text
		if candidate.FinishReason == genai.FinishReasonStop {
			// Check if content is nil or has no parts
			if candidate.Content == nil || candidate.Content.Parts == nil || len(candidate.Content.Parts) == 0 {
				slog.Debug("skipping candidate with finishReason STOP and no content",
					"candidate_index", idx)
				continue
			}
			
			// Check if all parts are empty (no text and no function calls)
			isEmptyContent := true
			for _, part := range candidate.Content.Parts {
				if part != nil && (part.Text != "" || part.FunctionCall != nil) {
					isEmptyContent = false
					break
				}
			}
			if isEmptyContent {
				slog.Debug("skipping candidate with finishReason STOP and empty content",
					"candidate_index", idx)
				continue
			}
		}

		// Create the choice.
		choice := openai.ChatCompletionResponseChoice{
			Index: int64(idx),
		}

		toolCalls := []openai.ChatCompletionMessageToolCallParam{}
		var err error

		if candidate.Content != nil {
			message := openai.ChatCompletionResponseChoiceMessage{
				Role: openai.ChatMessageRoleAssistant,
			}
			// Extract text from parts.
			content := extractTextFromGeminiParts(candidate.Content.Parts, responseMode)
			message.Content = &content

			// Extract tool calls if any.
			toolCalls, err = extractToolCallsFromGeminiParts(toolCalls, candidate.Content.Parts)
			if err != nil {
				return nil, fmt.Errorf("error extracting tool calls: %w", err)
			}
			message.ToolCalls = toolCalls

			// If there's no content but there are tool calls, set content to nil.
			if content == "" && len(toolCalls) > 0 {
				message.Content = nil
			}

			choice.Message = message
		}

		if candidate.SafetyRatings != nil {
			if choice.Message.Role == "" {
				choice.Message.Role = openai.ChatMessageRoleAssistant
			}

			choice.Message.SafetyRatings = candidate.SafetyRatings
		}

		// Handle logprobs if available.
		if candidate.LogprobsResult != nil {
			choice.Logprobs = geminiLogprobsToOpenAILogprobs(*candidate.LogprobsResult)
		}

		choice.FinishReason = geminiFinishReasonToOpenAI(candidate.FinishReason, toolCalls)

		choices = append(choices, choice)
	}

	return choices, nil
}

// Define a type constraint that includes both stream and non-stream tool call slice types.
type toolCallSlice interface {
	[]openai.ChatCompletionMessageToolCallParam | []openai.ChatCompletionChunkChoiceDeltaToolCall
}

// geminiFinishReasonToOpenAI converts Gemini finish reason to OpenAI finish reason.
func geminiFinishReasonToOpenAI[T toolCallSlice](reason genai.FinishReason, toolCalls T) openai.ChatCompletionChoicesFinishReason {
	switch reason {
	case genai.FinishReasonStop:
		if len(toolCalls) > 0 {
			return openai.ChatCompletionChoicesFinishReasonToolCalls
		}
		return openai.ChatCompletionChoicesFinishReasonStop
	case genai.FinishReasonMaxTokens:
		return openai.ChatCompletionChoicesFinishReasonLength
	case "":
		// For intermediate chunks in a streaming response, the finish reason is an empty string.
		// This is normal behavior and should not be treated as an error.
		return ""
	default:
		return openai.ChatCompletionChoicesFinishReasonContentFilter
	}
}

// extractTextFromGeminiParts extracts text from Gemini parts.
func extractTextFromGeminiParts(parts []*genai.Part, responseMode geminiResponseMode) string {
	var text string
	for _, part := range parts {
		if part != nil && part.Text != "" {
			if responseMode == responseModeRegex {
				// GCP doesn't natively support REGEX response modes, so we instead express them as json schema.
				// This causes the response to be wrapped in double-quotes.
				// E.g. `"positive"` (the double-quotes at the start and end are unwanted)
				// Here we remove the wrapping double-quotes.
				part.Text = strings.TrimPrefix(part.Text, "\"")
				part.Text = strings.TrimSuffix(part.Text, "\"")
			}
			text += part.Text
		}
	}
	return text
}

// unwrapIncorrectlyQuotedValues fixes Gemini's incorrectly quoted string values.
// When Gemini returns {"key": "\"value\""}, we normalize it to {"key": "value"}.
// This prevents malformed arguments from being sent back to Gemini in subsequent requests.
func unwrapIncorrectlyQuotedValues(argsMap map[string]any) {
	for key, value := range argsMap {
		if strValue, ok := value.(string); ok {
			// Check if the string value is a quoted string (starts and ends with ")
			if len(strValue) >= 2 && strValue[0] == '"' && strValue[len(strValue)-1] == '"' {
				// Unwrap the quotes
				unwrapped := strValue[1 : len(strValue)-1]
				// Unescape any escaped quotes inside
				unwrapped = strings.ReplaceAll(unwrapped, `\"`, `"`)
				argsMap[key] = unwrapped
				slog.Debug("unwrapped incorrectly quoted value",
					"key", key,
					"original", strValue,
					"unwrapped", unwrapped)
			}
		}
	}
}

// extractToolCallsFromGeminiParts extracts tool calls from Gemini parts.
func extractToolCallsFromGeminiParts(toolCalls []openai.ChatCompletionMessageToolCallParam, parts []*genai.Part) ([]openai.ChatCompletionMessageToolCallParam, error) {
	for _, part := range parts {
		if part == nil || part.FunctionCall == nil {
			continue
		}

		// First, unwrap any incorrectly quoted values in the args map
		if part.FunctionCall.Args != nil {
			unwrapIncorrectlyQuotedValues(part.FunctionCall.Args)
		}

		// Convert function call arguments to JSON string.
		args, err := json.Marshal(part.FunctionCall.Args)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal function arguments: %w", err)
		}

		argsStr := string(args)
		
		// Defensive: Clean duplicate JSON objects that Gemini sometimes returns
		// This can happen when using specific tool choice with AllowedFunctionNames
		if strings.Contains(argsStr, "}{") {
			slog.Warn("gemini returned duplicate JSON objects in tool call response, extracting first valid object",
				"function", part.FunctionCall.Name,
				"original_length", len(argsStr))
			
			// Extract first complete JSON object
			braceCount := 0
			for idx, char := range argsStr {
				if char == '{' {
					braceCount++
				} else if char == '}' {
					braceCount--
					if braceCount == 0 {
						argsStr = argsStr[:idx+1]
						slog.Info("sanitized gemini tool call response",
							"function", part.FunctionCall.Name,
							"cleaned_length", len(argsStr))
						break
					}
				}
			}
		}

		// Generate a random ID for the tool call.
		toolCallID := uuid.New().String()

		toolCall := openai.ChatCompletionMessageToolCallParam{
			ID:   &toolCallID,
			Type: openai.ChatCompletionMessageToolCallTypeFunction,
			Function: openai.ChatCompletionMessageToolCallFunctionParam{
				Name:      part.FunctionCall.Name,
				Arguments: argsStr,
			},
		}

		toolCalls = append(toolCalls, toolCall)
	}

	if len(toolCalls) == 0 {
		return nil, nil
	}

	return toolCalls, nil
}

// extractToolCallsFromGeminiPartsStream extracts tool calls from Gemini parts for streaming responses.
// Each tool call is assigned an incremental index starting from 0, matching OpenAI's streaming protocol.
// Returns ChatCompletionChunkChoiceDeltaToolCall types suitable for streaming responses, or nil if no tool calls are found.
// Note: Gemini sends complete tool calls in every streaming chunk, not incremental deltas.
// The caller is responsible for deduplicating based on the generated IDs.
func extractToolCallsFromGeminiPartsStream(toolCalls []openai.ChatCompletionChunkChoiceDeltaToolCall, parts []*genai.Part) ([]openai.ChatCompletionChunkChoiceDeltaToolCall, error) {
	toolCallIndex := int64(0)

	for _, part := range parts {
		if part == nil || part.FunctionCall == nil {
			continue
		}

		// First, unwrap any incorrectly quoted values in the args map
		if part.FunctionCall.Args != nil {
			unwrapIncorrectlyQuotedValues(part.FunctionCall.Args)
		}

		// Convert function call arguments to JSON string.
		args, err := json.Marshal(part.FunctionCall.Args)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal function arguments: %w", err)
		}

		argsStr := string(args)
		
		// Defensive: Clean duplicate JSON objects from Gemini streaming responses
		if strings.Contains(argsStr, "}{") {
			slog.Warn("gemini stream returned duplicate JSON objects, extracting first valid object",
				"function", part.FunctionCall.Name)
			
			braceCount := 0
			for idx, char := range argsStr {
				if char == '{' {
					braceCount++
				} else if char == '}' {
					braceCount--
					if braceCount == 0 {
						argsStr = argsStr[:idx+1]
						break
					}
				}
			}
		}

		// Generate a deterministic ID based on function name and index
		// This allows callers to track which tool calls have already been sent
		toolCallID := fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, toolCallIndex)

		toolCall := openai.ChatCompletionChunkChoiceDeltaToolCall{
			ID:   &toolCallID,
			Type: openai.ChatCompletionMessageToolCallTypeFunction,
			Function: openai.ChatCompletionMessageToolCallFunctionParam{
				Name:      part.FunctionCall.Name,
				Arguments: argsStr,
			},
			Index: toolCallIndex,
		}

		toolCalls = append(toolCalls, toolCall)
		toolCallIndex++
	}

	if len(toolCalls) == 0 {
		return nil, nil
	}

	return toolCalls, nil
}

// geminiUsageToOpenAIUsage converts Gemini usage metadata to OpenAI usage.
func geminiUsageToOpenAIUsage(metadata *genai.GenerateContentResponseUsageMetadata) openai.Usage {
	if metadata == nil {
		return openai.Usage{}
	}

	return openai.Usage{
		CompletionTokens: int(metadata.CandidatesTokenCount) + int(metadata.ThoughtsTokenCount),
		PromptTokens:     int(metadata.PromptTokenCount),
		TotalTokens:      int(metadata.TotalTokenCount),
		PromptTokensDetails: &openai.PromptTokensDetails{
			CachedTokens: int(metadata.CachedContentTokenCount),
		},
		CompletionTokensDetails: &openai.CompletionTokensDetails{
			ReasoningTokens: int(metadata.ThoughtsTokenCount),
		},
	}
}

// geminiLogprobsToOpenAILogprobs converts Gemini logprobs to OpenAI logprobs.
func geminiLogprobsToOpenAILogprobs(logprobsResult genai.LogprobsResult) openai.ChatCompletionChoicesLogprobs {
	if len(logprobsResult.ChosenCandidates) == 0 {
		return openai.ChatCompletionChoicesLogprobs{}
	}

	content := make([]openai.ChatCompletionTokenLogprob, 0, len(logprobsResult.ChosenCandidates))

	for i := 0; i < len(logprobsResult.ChosenCandidates); i++ {
		chosen := logprobsResult.ChosenCandidates[i]

		var topLogprobs []openai.ChatCompletionTokenLogprobTopLogprob

		// Process top candidates if available.
		if i < len(logprobsResult.TopCandidates) && logprobsResult.TopCandidates[i] != nil {
			topCandidates := logprobsResult.TopCandidates[i].Candidates
			if len(topCandidates) > 0 {
				topLogprobs = make([]openai.ChatCompletionTokenLogprobTopLogprob, 0, len(topCandidates))
				for _, tc := range topCandidates {
					topLogprobs = append(topLogprobs, openai.ChatCompletionTokenLogprobTopLogprob{
						Token:   tc.Token,
						Logprob: float64(tc.LogProbability),
					})
				}
			}
		}

		// Create token logprob.
		tokenLogprob := openai.ChatCompletionTokenLogprob{
			Token:       chosen.Token,
			Logprob:     float64(chosen.LogProbability),
			TopLogprobs: topLogprobs,
		}

		content = append(content, tokenLogprob)
	}

	// Return the logprobs.
	return openai.ChatCompletionChoicesLogprobs{
		Content: content,
	}
}

// buildGCPModelPathSuffix constructs a path Suffix with an optional queryParams where each string is in the form of "%s=%s".
func buildGCPModelPathSuffix(publisher, model, gcpMethod string, queryParams ...string) string {
	pathSuffix := fmt.Sprintf("publishers/%s/models/%s:%s", publisher, model, gcpMethod)

	if len(queryParams) > 0 {
		pathSuffix += "?" + strings.Join(queryParams, "&")
	}
	return pathSuffix
}

func buildGeminiModelPath(model string, gcpMethod string, queryParams ...string) string {
	path := fmt.Sprintf("/v1beta/models/%s:%s", model, gcpMethod)
	if len(queryParams) > 0 {
		path += "?" + strings.Join(queryParams, "&")
	}
	return path
}

// geminiCandidatesToOpenAIStreamingChoices converts Gemini candidates to OpenAI streaming choices.
func geminiCandidatesToOpenAIStreamingChoices(candidates []*genai.Candidate, responseMode geminiResponseMode) ([]openai.ChatCompletionResponseChunkChoice, error) {
	choices := make([]openai.ChatCompletionResponseChunkChoice, 0, len(candidates))

	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}

		// Skip candidates that only contain empty content with finishReason STOP
		// These are completion signals from Gemini streaming, not actual content
		// This happens in mode ANY when Gemini sends a second candidate with empty text
		if candidate.FinishReason == genai.FinishReasonStop {
			// Check if content is nil or has no parts
			if candidate.Content == nil || candidate.Content.Parts == nil || len(candidate.Content.Parts) == 0 {
				slog.Debug("skipping streaming candidate with finishReason STOP and no content")
				continue
			}
			
			// Check if all parts are empty (no text and no function calls)
			isEmptyContent := true
			for _, part := range candidate.Content.Parts {
				if part != nil && (part.Text != "" || part.FunctionCall != nil) {
					isEmptyContent = false
					break
				}
			}
			if isEmptyContent {
				slog.Debug("skipping streaming candidate with finishReason STOP and empty content")
				continue
			}
		}

		// Create the streaming choice.
		choice := openai.ChatCompletionResponseChunkChoice{
			Index: 0,
		}

		toolCalls := []openai.ChatCompletionChunkChoiceDeltaToolCall{}
		var err error
		if candidate.Content != nil {
			delta := &openai.ChatCompletionResponseChunkChoiceDelta{
				Role: openai.ChatMessageRoleAssistant,
			}

			// Extract text from parts for streaming (delta).
			content := extractTextFromGeminiParts(candidate.Content.Parts, responseMode)
			if content != "" {
				delta.Content = &content
			}

			// Extract tool calls if any.
			toolCalls, err = extractToolCallsFromGeminiPartsStream(toolCalls, candidate.Content.Parts)
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

// Helper functions for enhanced error handling

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
