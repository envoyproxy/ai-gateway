// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

type AudioTranscriptionRequest struct {
	Model                  string   `json:"model"`
	Language               string   `json:"language,omitempty"`
	Prompt                 string   `json:"prompt,omitempty"`
	ResponseFormat         string   `json:"response_format,omitempty"`
	Temperature            *float64 `json:"temperature,omitempty"`
	TimestampGranularities []string `json:"timestamp_granularities,omitempty"`
}

type AudioTranscriptionResponse struct {
	Text     string                 `json:"text"`
	Language string                 `json:"language,omitempty"`
	Duration float64                `json:"duration,omitempty"`
	Segments []TranscriptionSegment `json:"segments,omitempty"`
	Words    []TranscriptionWord    `json:"words,omitempty"`
}

type TranscriptionSegment struct {
	ID               int     `json:"id"`
	Seek             int     `json:"seek"`
	Start            float64 `json:"start"`
	End              float64 `json:"end"`
	Text             string  `json:"text"`
	Tokens           []int   `json:"tokens"`
	Temperature      float64 `json:"temperature"`
	AvgLogprob       float64 `json:"avg_logprob"`
	CompressionRatio float64 `json:"compression_ratio"`
	NoSpeechProb     float64 `json:"no_speech_prob"`
}

type TranscriptionWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}
