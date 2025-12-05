// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

// AudioSpeechRequest represents a request to the OpenAI /v1/audio/speech endpoint.
// https://platform.openai.com/docs/api-reference/audio/createSpeech
type AudioSpeechRequest struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          string   `json:"voice"`
	ResponseFormat string   `json:"response_format"`
	StreamFormat   string   `json:"stream_format,omitempty"`
	Speed          *float64 `json:"speed,omitempty"`
}
