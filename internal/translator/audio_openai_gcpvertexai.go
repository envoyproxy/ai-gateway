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
	"log/slog"

	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// NewAudioSpeechOpenAIToGCPVertexAITranslator implements AudioSpeechTranslator for OpenAI to GCP Vertex AI translation.
func NewAudioSpeechOpenAIToGCPVertexAITranslator(modelNameOverride internalapi.ModelNameOverride) AudioSpeechTranslator {
	return &audioSpeechOpenAIToGCPVertexAITranslator{modelNameOverride: modelNameOverride}
}

type audioSpeechOpenAIToGCPVertexAITranslator struct {
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
	stream            bool
	bufferedBody      []byte
	streamDelimiter   []byte
}

func (a *audioSpeechOpenAIToGCPVertexAITranslator) RequestBody(_ []byte, body *openai.AudioSpeechRequest, _ bool) (
	newHeaders []internalapi.Header,
	mutatedBody []byte,
	err error,
) {
	a.requestModel = body.Model
	if a.modelNameOverride != "" {
		a.requestModel = a.modelNameOverride
	}

	a.stream = true

	if body.ResponseFormat != "" && body.ResponseFormat != "mp3" {
		slog.Warn("ResponseFormat is not supported by Gemini TTS, defaulting to WAV output",
			"requested_format", body.ResponseFormat)
	}
	if body.Speed != nil && *body.Speed != 0 && *body.Speed != 1.0 {
		slog.Warn("Speed parameter is not supported by Gemini TTS, using default speed",
			"requested_speed", *body.Speed)
	}

	voiceName := mapOpenAIVoiceToGemini(body.Voice)

	geminiReq := gcp.GenerateContentRequest{
		Contents: []genai.Content{
			{
				Role: "user",
				Parts: []*genai.Part{
					genai.NewPartFromText(body.Input),
				},
			},
		},
		GenerationConfig: &genai.GenerationConfig{
			ResponseModalities: []genai.Modality{genai.ModalityAudio},
			Temperature:        floatPtr(1.0),
			SpeechConfig: &genai.SpeechConfig{
				VoiceConfig: &genai.VoiceConfig{
					PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{
						VoiceName: voiceName,
					},
				},
			},
		},
	}

	geminiReqBody, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling Gemini request: %w", err)
	}

	pathSuffix := buildGCPModelPathSuffix(gcpModelPublisherGoogle, a.requestModel, gcpMethodStreamGenerateContent, "alt=sse")

	newHeaders = append(newHeaders, internalapi.Header{":path", pathSuffix})

	slog.Info("translated audio/speech request to Gemini",
		"path", pathSuffix,
		"model", a.requestModel,
		"voice", voiceName,
		"body_length", len(geminiReqBody))

	return newHeaders, geminiReqBody, nil
}

func (a *audioSpeechOpenAIToGCPVertexAITranslator) ResponseHeaders(_ map[string]string) (
	newHeaders []internalapi.Header,
	err error,
) {
	if a.stream {
		newHeaders = append(newHeaders, internalapi.Header{"content-type", "text/event-stream"})
		return newHeaders, nil
	}
	return nil, nil
}

func (a *audioSpeechOpenAIToGCPVertexAITranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ any) (
	newHeaders []internalapi.Header,
	mutatedBody []byte,
	tokenUsage LLMTokenUsage,
	responseModel internalapi.ResponseModel,
	err error,
) {
	if a.stream {
		return a.handleStreamingResponse(body)
	}
	return nil, nil, LLMTokenUsage{}, "", nil
}

func (a *audioSpeechOpenAIToGCPVertexAITranslator) handleStreamingResponse(body io.Reader) (
	newHeaders []internalapi.Header, mutatedBody []byte, tokenUsage LLMTokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	chunks, err := a.parseGeminiStreamingChunks(body)
	if err != nil {
		return nil, nil, LLMTokenUsage{}, "", fmt.Errorf("error parsing Gemini streaming chunks: %w", err)
	}

	audioBuffer := bytes.Buffer{}

	for _, chunk := range chunks {
		if chunk.Candidates != nil {
			for _, candidate := range chunk.Candidates {
				if candidate.Content != nil {
					for _, part := range candidate.Content.Parts {
						if part.InlineData != nil && len(part.InlineData.Data) > 0 {
							audioBuffer.Write(part.InlineData.Data)
						}
					}
				}
			}
		}

		if chunk.UsageMetadata != nil {
			tokenUsage = LLMTokenUsage{
				InputTokens:  uint32(chunk.UsageMetadata.PromptTokenCount),     // nolint:gosec
				OutputTokens: uint32(chunk.UsageMetadata.CandidatesTokenCount), // nolint:gosec
				TotalTokens:  uint32(chunk.UsageMetadata.TotalTokenCount),      // nolint:gosec
			}
		}
	}

	return nil, audioBuffer.Bytes(), tokenUsage, a.requestModel, nil
}

func (a *audioSpeechOpenAIToGCPVertexAITranslator) parseGeminiStreamingChunks(body io.Reader) ([]genai.GenerateContentResponse, error) {
	var chunks []genai.GenerateContentResponse

	bodyReader := io.MultiReader(bytes.NewReader(a.bufferedBody), body)
	allData, err := io.ReadAll(bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read streaming body: %w", err)
	}

	if len(allData) == 0 {
		return chunks, nil
	}

	if a.streamDelimiter == nil {
		a.streamDelimiter = detectSSEDelimiter(allData)
	}

	var parts [][]byte
	if a.streamDelimiter != nil {
		parts = bytes.Split(allData, a.streamDelimiter)
	} else {
		parts = [][]byte{allData}
	}

	for _, part := range parts {
		part = bytes.TrimSpace(part)
		if len(part) == 0 {
			continue
		}

		line := bytes.TrimPrefix(part, []byte("data: "))

		if bytes.Equal(line, []byte("[DONE]")) {
			continue
		}

		var chunk genai.GenerateContentResponse
		if err := json.Unmarshal(line, &chunk); err == nil {
			chunks = append(chunks, chunk)
			a.bufferedBody = nil
		} else {
			a.bufferedBody = line
		}
	}

	return chunks, nil
}

func (a *audioSpeechOpenAIToGCPVertexAITranslator) ResponseError(_ map[string]string, body io.Reader) (
	newHeaders []internalapi.Header,
	mutatedBody []byte,
	err error,
) {
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, fmt.Errorf("error reading error response body: %w", err)
	}

	var gcpError gcpVertexAIError
	if unmarshalErr := json.Unmarshal(bodyBytes, &gcpError); unmarshalErr != nil {
		return nil, bodyBytes, nil
	}

	openAIError := openai.Error{
		Type: gcpVertexAIBackendError,
		Error: openai.ErrorType{
			Type:    gcpVertexAIBackendError,
			Message: gcpError.Error.Message,
			Code:    stringPtr(fmt.Sprintf("%d", gcpError.Error.Code)),
		},
	}

	errorBytes, err := json.Marshal(openAIError)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling OpenAI error: %w", err)
	}

	return nil, errorBytes, nil
}

func mapOpenAIVoiceToGemini(openAIVoice string) string {
	voiceMap := map[string]string{
		"alloy":   "Zephyr",
		"echo":    "Puck",
		"fable":   "Aoede",
		"onyx":    "Fenrir",
		"nova":    "Kore",
		"shimmer": "Thetis",
	}

	if geminiVoice, ok := voiceMap[openAIVoice]; ok {
		return geminiVoice
	}

	return "Zephyr"
}

func floatPtr(f float64) *float32 {
	f32 := float32(f)
	return &f32
}

func stringPtr(s string) *string {
	return &s
}
