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

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// NewAudioSpeechOpenAIToGCPVertexAITranslator creates a translator for OpenAI audio/speech to GCP Vertex AI Gemini.
func NewAudioSpeechOpenAIToGCPVertexAITranslator(modelNameOverride internalapi.ModelNameOverride) AudioSpeechTranslator {
	return &audioSpeechOpenAIToGCPVertexAITranslator{modelNameOverride: modelNameOverride, usePublisherPath: true}
}

// audioSpeechOpenAIToGCPVertexAITranslator translates OpenAI Audio Speech API to GCP Vertex AI Gemini API.
// This translator converts text-to-speech requests from OpenAI format to Gemini's multimodal audio generation.
type audioSpeechOpenAIToGCPVertexAITranslator struct {
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
	stream            bool
	bufferedBody      []byte // Buffer for incomplete JSON chunks in streaming responses
	usePublisherPath  bool
}

// RequestBody implements [AudioSpeechTranslator.RequestBody] for GCP Gemini.
// This method translates an OpenAI AudioSpeech request to a GCP Gemini API request with audio response modalities.
func (a *audioSpeechOpenAIToGCPVertexAITranslator) RequestBody(_ []byte, body *openai.AudioSpeechRequest, _ bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	a.requestModel = body.Model
	if a.modelNameOverride != "" {
		a.requestModel = a.modelNameOverride
	}

	// Gemini audio generation supports streaming
	a.stream = true // Always use streaming for audio generation

	// Map OpenAI voice to Gemini voice config
	voiceName := mapOpenAIVoiceToGemini(body.Voice)

	// Build Gemini request with audio response modalities
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

	// Marshal the Gemini request
	geminiReqBody, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling Gemini request: %w", err)
	}

	// Build the path suffix for streaming audio generation
	pathSuffix := buildGCPModelPathSuffix(gcpModelPublisherGoogle, a.requestModel, gcpMethodStreamGenerateContent, "alt=sse")
	if !a.usePublisherPath {
		pathSuffix = buildGeminiModelPath(a.requestModel, gcpMethodStreamGenerateContent, "alt=sse")
	}

	headerMutation, bodyMutation := buildRequestMutations(pathSuffix, geminiReqBody)

	slog.Info("translated audio/speech request to Gemini",
		"path", pathSuffix,
		"use_publisher_path", a.usePublisherPath,
		"model", a.requestModel,
		"voice", voiceName,
		"body_length", len(geminiReqBody))

	return headerMutation, bodyMutation, nil
}

// ResponseHeaders implements [AudioSpeechTranslator.ResponseHeaders].
func (a *audioSpeechOpenAIToGCPVertexAITranslator) ResponseHeaders(_ map[string]string) (*extprocv3.HeaderMutation, error) {
	if a.stream {
		// For streaming responses, set content-type to match expected audio format
		headerMutation := &extprocv3.HeaderMutation{
			SetHeaders: []*corev3.HeaderValueOption{
				{
					Header: &corev3.HeaderValue{
						Key:      "content-type",
						RawValue: []byte("text/event-stream"),
					},
				},
			},
		}
		return headerMutation, nil
	}
	return nil, nil
}

// ResponseBody implements [AudioSpeechTranslator.ResponseBody].
// This method handles the streaming response from Gemini and extracts audio data.
func (a *audioSpeechOpenAIToGCPVertexAITranslator) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, LLMTokenUsage, internalapi.ResponseModel, error) {
	if a.stream {
		return a.handleStreamingResponse(body, endOfStream)
	}
	return nil, nil, LLMTokenUsage{}, "", nil
}

// handleStreamingResponse processes streaming audio responses from Gemini
func (a *audioSpeechOpenAIToGCPVertexAITranslator) handleStreamingResponse(body io.Reader, endOfStream bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, responseModel string, err error,
) {
	// Parse GCP streaming chunks
	chunks, err := a.parseGeminiStreamingChunks(body)
	if err != nil {
		return nil, nil, LLMTokenUsage{}, "", fmt.Errorf("error parsing Gemini streaming chunks: %w", err)
	}

	audioBuffer := bytes.Buffer{}

	// Extract audio data from chunks
	for _, chunk := range chunks {
		if chunk.Candidates != nil {
			for _, candidate := range chunk.Candidates {
				if candidate.Content != nil {
					for _, part := range candidate.Content.Parts {
						// Extract inline audio data if present
						if part.InlineData != nil && len(part.InlineData.Data) > 0 {
							// The audio data is base64 encoded in the response
							audioBuffer.Write(part.InlineData.Data)
						}
					}
				}
			}
		}

		// Extract token usage if present (typically in last chunk)
		if chunk.UsageMetadata != nil {
			tokenUsage = LLMTokenUsage{
				InputTokens:  uint32(chunk.UsageMetadata.PromptTokenCount),
				OutputTokens: uint32(chunk.UsageMetadata.CandidatesTokenCount),
				TotalTokens:  uint32(chunk.UsageMetadata.TotalTokenCount),
			}
		}
	}

	mut := &extprocv3.BodyMutation_Body{
		Body: audioBuffer.Bytes(),
	}

	return nil, &extprocv3.BodyMutation{Mutation: mut}, tokenUsage, a.requestModel, nil
}

// parseGeminiStreamingChunks parses the buffered body to extract complete JSON chunks
func (a *audioSpeechOpenAIToGCPVertexAITranslator) parseGeminiStreamingChunks(body io.Reader) ([]genai.GenerateContentResponse, error) {
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("error reading body: %w", err)
	}

	// Append new data to buffer
	a.bufferedBody = append(a.bufferedBody, bodyBytes...)

	var chunks []genai.GenerateContentResponse
	lines := bytes.Split(a.bufferedBody, []byte("\n"))

	// Keep the last incomplete line in buffer
	var remainingBuffer []byte
	for i, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// Skip SSE event prefixes
		if bytes.HasPrefix(line, []byte("data: ")) {
			line = bytes.TrimPrefix(line, []byte("data: "))
		}

		// Check for end marker
		if bytes.Equal(line, []byte("[DONE]")) {
			continue
		}

		// Try to parse as JSON
		var chunk genai.GenerateContentResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			// If this is not the last line, it's an error
			if i < len(lines)-1 {
				return nil, fmt.Errorf("error unmarshaling chunk: %w", err)
			}
			// Keep incomplete last line in buffer
			remainingBuffer = line
			continue
		}

		chunks = append(chunks, chunk)
	}

	// Update buffer with remaining incomplete data
	a.bufferedBody = remainingBuffer

	return chunks, nil
}

// ResponseError implements [AudioSpeechTranslator.ResponseError].
func (a *audioSpeechOpenAIToGCPVertexAITranslator) ResponseError(headers map[string]string, body io.Reader) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	// Read the error response from GCP
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, fmt.Errorf("error reading error response body: %w", err)
	}

	var gcpError gcpVertexAIError
	if err := json.Unmarshal(bodyBytes, &gcpError); err != nil {
		// If we can't parse as GCP error, return the raw body
		return nil, &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: bodyBytes},
		}, nil
	}

	// Convert to OpenAI error format
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

	return nil, &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{Body: errorBytes},
	}, nil
}

// mapOpenAIVoiceToGemini maps OpenAI voice names to Gemini voice names
func mapOpenAIVoiceToGemini(openAIVoice string) string {
	// Map OpenAI voices to Gemini prebuilt voices
	// Gemini supports: Puck, Charon, Kore, Fenrir, Aoede, Zephyr, Thetis
	// OpenAI supports: alloy, echo, fable, onyx, nova, shimmer
	voiceMap := map[string]string{
		"alloy":   "Zephyr", // Neutral, balanced
		"echo":    "Puck",   // Male voice
		"fable":   "Aoede",  // Expressive
		"onyx":    "Fenrir", // Deep male
		"nova":    "Kore",   // Female voice
		"shimmer": "Thetis", // Soft female
	}

	if geminiVoice, ok := voiceMap[openAIVoice]; ok {
		return geminiVoice
	}

	// Default to Zephyr if unknown voice
	return "Zephyr"
}

func (a *audioSpeechOpenAIToGCPVertexAITranslator) SetPublisherPathEnabled(enabled bool) {
	a.usePublisherPath = enabled
}

// floatPtr returns a pointer to a float32
func floatPtr(f float64) *float32 {
	f32 := float32(f)
	return &f32
}

// stringPtr returns a pointer to a string
func stringPtr(s string) *string {
	return &s
}
