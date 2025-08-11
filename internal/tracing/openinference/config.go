// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openinference

import (
	"os"
	"strconv"
)

// Environment variable names for trace configuration following Python OpenInference conventions.
// These environment variables control the privacy and observability settings for OpenInference tracing.
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
const (
	// EnvHideLLMInvocationParameters is the environment variable for TraceConfig.HideLLMInvocationParameters.
	EnvHideLLMInvocationParameters = "OPENINFERENCE_HIDE_LLM_INVOCATION_PARAMETERS"
	// EnvHideInputs is the environment variable for TraceConfig.HideInputs.
	EnvHideInputs = "OPENINFERENCE_HIDE_INPUTS"
	// EnvHideOutputs is the environment variable for TraceConfig.HideOutputs.
	EnvHideOutputs = "OPENINFERENCE_HIDE_OUTPUTS"
	// EnvHideInputMessages is the environment variable for TraceConfig.HideInputMessages.
	EnvHideInputMessages = "OPENINFERENCE_HIDE_INPUT_MESSAGES"
	// EnvHideOutputMessages is the environment variable for TraceConfig.HideOutputMessages.
	EnvHideOutputMessages = "OPENINFERENCE_HIDE_OUTPUT_MESSAGES"
	// EnvHideInputImages is the environment variable for TraceConfig.HideInputImages.
	EnvHideInputImages = "OPENINFERENCE_HIDE_INPUT_IMAGES"
	// EnvHideInputText is the environment variable for TraceConfig.HideInputText.
	EnvHideInputText = "OPENINFERENCE_HIDE_INPUT_TEXT"
	// EnvHideOutputText is the environment variable for TraceConfig.HideOutputText.
	EnvHideOutputText = "OPENINFERENCE_HIDE_OUTPUT_TEXT"
	// EnvHideEmbeddingVectors is the environment variable for TraceConfig.HideEmbeddingVectors.
	EnvHideEmbeddingVectors = "OPENINFERENCE_HIDE_EMBEDDING_VECTORS"
	// EnvBase64ImageMaxLength is the environment variable for TraceConfig.Base64ImageMaxLength.
	EnvBase64ImageMaxLength = "OPENINFERENCE_BASE64_IMAGE_MAX_LENGTH"
	// EnvHidePrompts is the environment variable for TraceConfig.HidePrompts.
	EnvHidePrompts = "OPENINFERENCE_HIDE_PROMPTS"
)

// Default values for trace configuration.
const (
	defaultHideLLMInvocationParameters = false
	defaultHidePrompts                 = false
	defaultHideInputs                  = false
	defaultHideOutputs                 = false
	defaultHideInputMessages           = false
	defaultHideOutputMessages          = false
	defaultHideInputImages             = false
	defaultHideInputText               = false
	defaultHideOutputText              = false
	defaultHideEmbeddingVectors        = false
	defaultBase64ImageMaxLength        = 32000
)

// RedactedValue is the value used when content is hidden for privacy.
const RedactedValue = "__REDACTED__"

// TraceConfig helps you modify the observability level of your tracing.
// For instance, you may want to keep sensitive information from being logged.
// for security reasons, or you may want to limit the size of the base64.
// encoded images to reduce payloads.
//
// Use NewTraceConfig to create this from defaults or NewTraceConfigFromEnv
// to prioritize environment variables.
//
// This implementation follows the OpenInference configuration specification:
// https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
type TraceConfig struct {
	// HideLLMInvocationParameters controls whether LLM invocation parameters are hidden.
	// This is independent of HideInputs.
	HideLLMInvocationParameters bool
	// HideInputs controls whether input values and messages are hidden.
	// When true, hides both input.value and all input messages.
	HideInputs bool
	// HideOutputs controls whether output values and messages are hidden.
	// When true, hides both output.value and all output messages.
	HideOutputs bool
	// HideInputMessages controls whether all input messages are hidden.
	// Input messages are hidden if either HideInputs OR HideInputMessages is true.
	HideInputMessages bool
	// HideOutputMessages controls whether all output messages are hidden.
	// Output messages are hidden if either HideOutputs OR HideOutputMessages is true.
	HideOutputMessages bool
	// HideInputImages controls whether images from input messages are hidden.
	// Only applies when input messages are not already hidden.
	HideInputImages bool
	// HideInputText controls whether text from input messages is hidden.
	// Only applies when input messages are not already hidden.
	HideInputText bool
	// HideOutputText controls whether text from output messages is hidden.
	// Only applies when output messages are not already hidden.
	HideOutputText bool
	// HideEmbeddingVectors controls whether embedding vectors are hidden.
	HideEmbeddingVectors bool
	// Base64ImageMaxLength limits the characters of a base64 encoding of an image.
	Base64ImageMaxLength int
	// HidePrompts controls whether LLM prompts are hidden.
	HidePrompts bool
}

// NewTraceConfig creates a new TraceConfig with default values.
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewTraceConfig() *TraceConfig {
	return &TraceConfig{
		HideLLMInvocationParameters: defaultHideLLMInvocationParameters,
		HideInputs:                  defaultHideInputs,
		HideOutputs:                 defaultHideOutputs,
		HideInputMessages:           defaultHideInputMessages,
		HideOutputMessages:          defaultHideOutputMessages,
		HideInputImages:             defaultHideInputImages,
		HideInputText:               defaultHideInputText,
		HideOutputText:              defaultHideOutputText,
		HideEmbeddingVectors:        defaultHideEmbeddingVectors,
		Base64ImageMaxLength:        defaultBase64ImageMaxLength,
		HidePrompts:                 defaultHidePrompts,
	}
}

// NewTraceConfigFromEnv creates a new TraceConfig with values from environment
// variables or their corresponding defaults.
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewTraceConfigFromEnv() *TraceConfig {
	return &TraceConfig{
		HideLLMInvocationParameters: getBoolEnv(EnvHideLLMInvocationParameters, defaultHideLLMInvocationParameters),
		HideInputs:                  getBoolEnv(EnvHideInputs, defaultHideInputs),
		HideOutputs:                 getBoolEnv(EnvHideOutputs, defaultHideOutputs),
		HideInputMessages:           getBoolEnv(EnvHideInputMessages, defaultHideInputMessages),
		HideOutputMessages:          getBoolEnv(EnvHideOutputMessages, defaultHideOutputMessages),
		HideInputImages:             getBoolEnv(EnvHideInputImages, defaultHideInputImages),
		HideInputText:               getBoolEnv(EnvHideInputText, defaultHideInputText),
		HideOutputText:              getBoolEnv(EnvHideOutputText, defaultHideOutputText),
		HideEmbeddingVectors:        getBoolEnv(EnvHideEmbeddingVectors, defaultHideEmbeddingVectors),
		Base64ImageMaxLength:        getIntEnv(EnvBase64ImageMaxLength, defaultBase64ImageMaxLength),
		HidePrompts:                 getBoolEnv(EnvHidePrompts, defaultHidePrompts),
	}
}

// getEnv reads a value from an environment variable and parses it using the provided parser.
// Returns defaultValue if the variable is not set or cannot be parsed.
func getEnv[T any](key string, defaultValue T, parse func(string) (T, error)) T {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parsed, err := parse(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

// getBoolEnv reads a boolean value from an environment variable.
// Returns defaultValue if the variable is not set or cannot be parsed.
func getBoolEnv(key string, defaultValue bool) bool {
	return getEnv(key, defaultValue, strconv.ParseBool)
}

// getIntEnv reads an integer value from an environment variable.
// Returns defaultValue if the variable is not set or cannot be parsed.
func getIntEnv(key string, defaultValue int) int {
	return getEnv(key, defaultValue, strconv.Atoi)
}
