// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package openinference provides OpenInference semantic conventions for
// OpenTelemetry tracing.
package openinference

import "fmt"

// OpenInference Span Kind constants.
//
// These constants define the type of operation represented by a span.
// Reference: https://github.com/Arize-ai/openinference/blob/main/spec/semantic_conventions.md
const (
	// SpanKind identifies the type of operation (required for all OpenInference spans).
	SpanKind = "openinference.span.kind"

	// SpanKindLLM indicates a Large Language Model operation.
	SpanKindLLM = "LLM"
)

// LLM Operation constants.
//
// These constants define attributes for Large Language Model operations.
// following OpenInference semantic conventions.
// Reference: https://github.com/Arize-ai/openinference/blob/main/spec/semantic_conventions.md#llm-spans
const (
	// LLMSystem identifies the AI system/product (e.g., "openai").
	LLMSystem = "llm.system"

	// LLMModelName specifies the model name (e.g., "gpt-4", "gpt-3.5-turbo").
	LLMModelName = "llm.model_name"

	// LLMInvocationParameters contains the invocation parameters as JSON string.
	LLMInvocationParameters = "llm.invocation_parameters"
)

// LLMSystem Values.
const (
	// LLMSystemOpenAI for OpenAI systems.
	LLMSystemOpenAI = "openai"
)

// Input/Output constants.
//
// These constants define attributes for capturing input and output data.
// Reference: https://github.com/Arize-ai/openinference/blob/main/spec/semantic_conventions.md#inputoutput
const (
	// InputValue contains the input data as a string (typically JSON).
	InputValue = "input.value"

	// InputMimeType specifies the MIME type of the input data.
	InputMimeType = "input.mime_type"

	// OutputValue contains the output data as a string (typically JSON).
	OutputValue = "output.value"

	// OutputMimeType specifies the MIME type of the output data.
	OutputMimeType = "output.mime_type"

	// MimeTypeJSON for JSON content.
	MimeTypeJSON = "application/json"
)

// LLM Message constants.
//
// These constants define attributes for LLM input and output messages using.
// flattened attribute format. Messages are indexed starting from 0.
// Reference: https://github.com/Arize-ai/openinference/blob/main/spec/semantic_conventions.md#llm-spans
const (
	// LLMInputMessages prefix for input message attributes.
	// Usage: llm.input_messages.{index}.message.role, llm.input_messages.{index}.message.content.
	LLMInputMessages = "llm.input_messages"

	// LLMOutputMessages prefix for output message attributes.
	// Usage: llm.output_messages.{index}.message.role, llm.output_messages.{index}.message.content.
	LLMOutputMessages = "llm.output_messages"

	// MessageRole suffix for message role (e.g., "user", "assistant", "system").
	MessageRole = "message.role"

	// MessageContent suffix for message content.
	MessageContent = "message.content"
)

// Token Count constants.
//
// These constants define attributes for token usage tracking.
// Reference: https://github.com/Arize-ai/openinference/blob/main/spec/semantic_conventions.md#llm-spans
const (
	// LLMTokenCountPrompt contains the number of tokens in the prompt.
	LLMTokenCountPrompt = "llm.token_count.prompt" // #nosec G101

	// LLMTokenCountCompletion contains the number of tokens in the completion.
	LLMTokenCountCompletion = "llm.token_count.completion" // #nosec G101

	// LLMTokenCountTotal contains the total number of tokens.
	LLMTokenCountTotal = "llm.token_count.total" // #nosec G101
)

// Tool Call constants.
//
// These constants define attributes for function/tool calling in LLM operations.
// Used when LLM responses include tool calls.
// Reference: Python OpenAI instrumentation (not in core spec).
const (
	// LLMTools contains the list of available tools as JSON.
	// Format: llm.tools.{index}.tool.json_schema.
	LLMTools = "llm.tools"

	// MessageToolCalls prefix for tool calls in messages.
	// Format: message.tool_calls.{index}.tool_call.{attribute}.
	MessageToolCalls = "message.tool_calls"

	// ToolCallID suffix for tool call ID.
	ToolCallID = "tool_call.id"

	// ToolCallFunctionName suffix for function name in a tool call.
	ToolCallFunctionName = "tool_call.function.name"

	// ToolCallFunctionArguments suffix for function arguments as JSON string.
	ToolCallFunctionArguments = "tool_call.function.arguments"
)

// Extended Token Count constants.
//
// These constants define additional token count attributes for detailed usage tracking.
// They provide granular information about token consumption for cost analysis and
// performance monitoring.
// Reference: OpenInference specification and Python OpenAI instrumentation.
const (
	// LLMTokenCountPromptCacheHit represents the number of prompt tokens successfully.
	// retrieved from cache (cache hits). This enables tracking of cache efficiency
	// and cost savings from cached prompts.
	LLMTokenCountPromptCacheHit = "llm.token_count.prompt_details.cache_read" // #nosec G101

	// LLMTokenCountPromptAudio represents the number of audio tokens in the prompt.
	// Used for multimodal models that support audio input.
	LLMTokenCountPromptAudio = "llm.token_count.prompt_details.audio" // #nosec G101

	// LLMTokenCountCompletionReasoning represents the number of tokens used for
	// reasoning or chain-of-thought processes in the completion. This helps track
	// the computational cost of complex reasoning tasks.
	LLMTokenCountCompletionReasoning = "llm.token_count.completion_details.reasoning" // #nosec G101

	// LLMTokenCountCompletionAudio represents the number of audio tokens in the.
	// completion. Used for models that generate audio output.
	LLMTokenCountCompletionAudio = "llm.token_count.completion_details.audio" // #nosec G101
)

// InputMessageAttribute creates an attribute key for input messages.
func InputMessageAttribute(index int, suffix string) string {
	return fmt.Sprintf("%s.%d.%s", LLMInputMessages, index, suffix)
}

// InputMessageContentAttribute creates an attribute key for input message content.
func InputMessageContentAttribute(messageIndex, contentIndex int, suffix string) string {
	return fmt.Sprintf("%s.%d.message.contents.%d.message_content.%s", LLMInputMessages, messageIndex, contentIndex, suffix)
}

// OutputMessageAttribute creates an attribute key for output messages.
func OutputMessageAttribute(index int, suffix string) string {
	return fmt.Sprintf("%s.%d.%s", LLMOutputMessages, index, suffix)
}

// OutputMessageToolCallAttribute creates an attribute key for a tool call.
func OutputMessageToolCallAttribute(messageIndex, toolCallIndex int, suffix string) string {
	return fmt.Sprintf("%s.%d.%s.%d.%s", LLMOutputMessages, messageIndex, MessageToolCalls, toolCallIndex, suffix)
}
