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
	"mime"
	"mime/multipart"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const defaultTranscriptionPrompt = "Transcribe this audio clip"

// detectAudioMimeType returns the MIME type based on the file extension
func detectAudioMimeType(filename string) string {
	ext := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(ext, ".wav"):
		return "audio/wav"
	case strings.HasSuffix(ext, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(ext, ".m4a"):
		return "audio/mp4"
	case strings.HasSuffix(ext, ".ogg"):
		return "audio/ogg"
	case strings.HasSuffix(ext, ".flac"):
		return "audio/flac"
	case strings.HasSuffix(ext, ".webm"):
		return "audio/webm"
	case strings.HasSuffix(ext, ".aac"):
		return "audio/aac"
	default:
		return "audio/wav" // default to WAV
	}
}

// NewAudioTranscriptionOpenAIToGCPVertexAITranslator creates a translator for OpenAI audio/transcriptions to GCP Vertex AI Gemini.
func NewAudioTranscriptionOpenAIToGCPVertexAITranslator(modelNameOverride internalapi.ModelNameOverride) AudioTranscriptionTranslator {
	return &audioTranscriptionOpenAIToGCPVertexAITranslator{
		modelNameOverride: modelNameOverride,
		usePublisherPath:  false, // default to using /v1beta/models path (Gemini direct API)
	}
}

type audioTranscriptionOpenAIToGCPVertexAITranslator struct {
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
	audioData         []byte
	usePublisherPath  bool
	contentType       string // Store content-type header
	stream            bool   // Whether to use streaming or non-streaming
	bufferedBody      []byte // Buffer for incomplete streaming chunks
}

// SetContentType sets the content-type header for multipart parsing
func (a *audioTranscriptionOpenAIToGCPVertexAITranslator) SetContentType(contentType string) {
	a.contentType = contentType
}

// SetUseGeminiDirectPath switches between /v1beta (Gemini) and /publishers (Vertex AI)
func (a *audioTranscriptionOpenAIToGCPVertexAITranslator) SetUseGeminiDirectPath(use bool) {
	a.usePublisherPath = !use
}

// RequestBody translates OpenAI AudioTranscription request to GCP Gemini API request.
func (a *audioTranscriptionOpenAIToGCPVertexAITranslator) RequestBody(
	rawBody []byte,
	body *openai.AudioTranscriptionRequest,
	onRetry bool,
) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	a.requestModel = body.Model
	if a.modelNameOverride != "" {
		a.requestModel = a.modelNameOverride
	}

	// Check if streaming should be enabled based on response_format
	// For now, we'll default to streaming to support both modes
	a.stream = true

	// Instruction / prompt
	instruction := defaultTranscriptionPrompt
	if body.Prompt != "" {
		instruction = body.Prompt
	}

	// Extract audio from multipart/form-data if possible
	var audioData []byte
	var mimeType string = "audio/wav" // default MIME type for audio

	mediaType, params, err := mime.ParseMediaType(a.contentType)
	if err == nil && strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary != "" {
			reader := multipart.NewReader(bytes.NewReader(rawBody), boundary)
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, nil, fmt.Errorf("error reading multipart: %w", err)
				}
				if part.FormName() == "file" {
					audioData, _ = io.ReadAll(part)

					// Try to get MIME type from Content-Type header first
					if ct := part.Header.Get("Content-Type"); ct != "" {
						mimeType = ct
					} else if fn := part.FileName(); fn != "" {
						// If no Content-Type, detect from filename extension
						mimeType = detectAudioMimeType(fn)
					}
					break
				}
			}
		}
	}

	// Fallback if multipart parsing failed
	if len(audioData) == 0 {
		audioData = rawBody
	}

	a.audioData = audioData

	// Build Gemini request with text instruction and inline audio data
	geminiReq := gcp.GenerateContentRequest{
		Contents: []genai.Content{
			{
				Role: "user",
				Parts: []*genai.Part{
					genai.NewPartFromText(instruction),
					{
						InlineData: &genai.Blob{
							MIMEType: mimeType,
							Data:     audioData,
						},
					},
				},
			},
		},
	}

	geminiReqBody, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling Gemini request: %w", err)
	}

	// Log the actual request body being sent (for debugging)
	slog.Debug("gemini request body", "body", string(geminiReqBody))

	// Determine path based on streaming mode
	var pathSuffix string
	if a.stream {
		// Use streamGenerateContent for streaming
		if a.usePublisherPath {
			pathSuffix = buildGCPModelPathSuffix(gcpModelPublisherGoogle, a.requestModel, gcpMethodStreamGenerateContent, "alt=sse")
		} else {
			pathSuffix = buildGeminiModelPath(a.requestModel, gcpMethodStreamGenerateContent, "alt=sse")
		}
	} else {
		// Use generateContent for non-streaming
		if a.usePublisherPath {
			pathSuffix = buildGCPModelPathSuffix(gcpModelPublisherGoogle, a.requestModel, gcpMethodGenerateContent)
		} else {
			pathSuffix = buildGeminiModelPath(a.requestModel, gcpMethodGenerateContent)
		}
	}

	headerMutation := &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{
				Header: &corev3.HeaderValue{
					Key:      ":path",
					RawValue: []byte(pathSuffix),
				},
			},
		},
	}

	bodyMutation := &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{
			Body: geminiReqBody,
		},
	}

	// Add Accept header for streaming requests to match the working curl
	if a.stream {
		if headerMutation == nil {
			headerMutation = &extprocv3.HeaderMutation{}
		}
		headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{Key: "accept", Value: "text/event-stream"},
		})
	}

	// Create debug version with placeholder for audio data
	debugContents := []genai.Content{
		{
			Role: "user",
			Parts: []*genai.Part{
				genai.NewPartFromText(instruction),
				{
					InlineData: &genai.Blob{
						MIMEType: mimeType,
						Data:     []byte(fmt.Sprintf("<AUDIO_DATA_%d_BYTES>", len(audioData))),
					},
				},
			},
		},
	}
	contentsJSON, _ := json.MarshalIndent(debugContents, "", "  ")

	// Logging
	slog.Info("translated audio/transcriptions request to Gemini",
		"contents_json", string(contentsJSON),
		"streaming", a.stream,
		"body", string(geminiReqBody))

	return headerMutation, bodyMutation, nil
}

// ResponseHeaders processes response headers from GCP Vertex AI.
func (a *audioTranscriptionOpenAIToGCPVertexAITranslator) ResponseHeaders(headers map[string]string) (*extprocv3.HeaderMutation, error) {
	if a.stream {
		// For streaming responses, set content-type to text/event-stream to match OpenAI API
		return &extprocv3.HeaderMutation{
			SetHeaders: []*corev3.HeaderValueOption{
				{Header: &corev3.HeaderValue{Key: "content-type", Value: "text/event-stream"}},
			},
		}, nil
	}
	// For non-streaming, ensure content-type is application/json
	return &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{Key: "content-type", Value: "application/json"}},
		},
	}, nil
}

// ResponseBody translates GCP Vertex AI response to OpenAI AudioTranscription response.
func (a *audioTranscriptionOpenAIToGCPVertexAITranslator) ResponseBody(
	headers map[string]string,
	body io.Reader,
	endOfStream bool,
) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, LLMTokenUsage, internalapi.ResponseModel, error) {
	if a.stream {
		return a.handleStreamingResponse(body, endOfStream)
	}
	return a.handleNonStreamingResponse(body, endOfStream)
}

// handleNonStreamingResponse handles non-streaming responses from Gemini
func (a *audioTranscriptionOpenAIToGCPVertexAITranslator) handleNonStreamingResponse(
	body io.Reader,
	endOfStream bool,
) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, LLMTokenUsage, internalapi.ResponseModel, error) {
	if !endOfStream {
		return nil, nil, LLMTokenUsage{}, "", nil
	}

	// Read the response body
	responseBody, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, LLMTokenUsage{}, "", fmt.Errorf("error reading response body: %w", err)
	}

	// Parse Gemini response
	var geminiResp genai.GenerateContentResponse
	if err := json.Unmarshal(responseBody, &geminiResp); err != nil {
		return nil, nil, LLMTokenUsage{}, "", fmt.Errorf("error unmarshaling Gemini response: %w", err)
	}

	// Extract transcription text from Gemini response
	var transcriptionText string
	if len(geminiResp.Candidates) > 0 && geminiResp.Candidates[0].Content != nil {
		for _, part := range geminiResp.Candidates[0].Content.Parts {
			if part.Text != "" {
				transcriptionText += part.Text
			}
		}
	}

	// Build OpenAI-compatible response
	openaiResp := openai.AudioTranscriptionResponse{
		Text: transcriptionText,
	}

	openaiRespBody, err := json.Marshal(openaiResp)
	if err != nil {
		return nil, nil, LLMTokenUsage{}, "", fmt.Errorf("error marshaling OpenAI response: %w", err)
	}

	// Calculate token usage
	tokenUsage := LLMTokenUsage{}
	if geminiResp.UsageMetadata != nil {
		tokenUsage.InputTokens = uint32(geminiResp.UsageMetadata.PromptTokenCount)      // nolint:gosec
		tokenUsage.OutputTokens = uint32(geminiResp.UsageMetadata.CandidatesTokenCount) // nolint:gosec
		tokenUsage.TotalTokens = uint32(geminiResp.UsageMetadata.TotalTokenCount)       // nolint:gosec
	}

	slog.Info("translated Gemini audio response to OpenAI (non-streaming)",
		"input_tokens", tokenUsage.InputTokens,
		"output_tokens", tokenUsage.OutputTokens,
		"transcription_length", len(transcriptionText))

	return nil, &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{Body: openaiRespBody},
	}, tokenUsage, a.requestModel, nil
}

// handleStreamingResponse handles streaming responses from Gemini
func (a *audioTranscriptionOpenAIToGCPVertexAITranslator) handleStreamingResponse(
	body io.Reader,
	endOfStream bool,
) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, LLMTokenUsage, internalapi.ResponseModel, error) {
	// Parse streaming chunks
	chunks, err := a.parseGeminiStreamingChunks(body)
	if err != nil {
		return nil, nil, LLMTokenUsage{}, "", fmt.Errorf("error parsing Gemini streaming chunks: %w", err)
	}

	// Accumulate transcription text from all chunks
	var transcriptionText string
	tokenUsage := LLMTokenUsage{}

	for _, chunk := range chunks {
		// Extract text from candidates
		if len(chunk.Candidates) > 0 && chunk.Candidates[0].Content != nil {
			for _, part := range chunk.Candidates[0].Content.Parts {
				if part.Text != "" {
					transcriptionText += part.Text
				}
			}
		}

		// Extract token usage if present (typically in last chunk)
		if chunk.UsageMetadata != nil {
			tokenUsage = LLMTokenUsage{
				InputTokens:  uint32(chunk.UsageMetadata.PromptTokenCount),      // nolint:gosec
				OutputTokens: uint32(chunk.UsageMetadata.CandidatesTokenCount),  // nolint:gosec
				TotalTokens:  uint32(chunk.UsageMetadata.TotalTokenCount),       // nolint:gosec
			}
		}
	}

	// Only return final response when stream ends
	if !endOfStream {
		return nil, nil, LLMTokenUsage{}, "", nil
	}

	// Build OpenAI-compatible response
	openaiResp := openai.AudioTranscriptionResponse{
		Text: transcriptionText,
	}

	openaiRespBody, err := json.Marshal(openaiResp)
	if err != nil {
		return nil, nil, LLMTokenUsage{}, "", fmt.Errorf("error marshaling OpenAI response: %w", err)
	}

	slog.Info("translated Gemini audio response to OpenAI (streaming)",
		"input_tokens", tokenUsage.InputTokens,
		"output_tokens", tokenUsage.OutputTokens,
		"transcription_length", len(transcriptionText))

	return nil, &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{Body: openaiRespBody},
	}, tokenUsage, a.requestModel, nil
}

// parseGeminiStreamingChunks parses the buffered body to extract complete JSON chunks
func (a *audioTranscriptionOpenAIToGCPVertexAITranslator) parseGeminiStreamingChunks(body io.Reader) ([]genai.GenerateContentResponse, error) {
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("error reading body: %w", err)
	}

	// Append new data to buffer
	a.bufferedBody = append(a.bufferedBody, bodyBytes...)

	var chunks []genai.GenerateContentResponse
	
	// Normalize line endings: replace \r\n with \n
	normalizedBody := bytes.ReplaceAll(a.bufferedBody, []byte("\r\n"), []byte("\n"))
	lines := bytes.Split(normalizedBody, []byte("\n\n"))

	// Keep the last incomplete chunk in buffer
	var remainingBuffer []byte
	for i, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// Skip SSE event prefixes (data: prefix)
		if bytes.HasPrefix(line, []byte("data: ")) {
			line = bytes.TrimPrefix(line, []byte("data: "))
			line = bytes.TrimSpace(line)
		}

		// Check for end marker [DONE] - though Gemini doesn't send this
		if bytes.Equal(line, []byte("[DONE]")) {
			continue
		}

		// Try to parse as JSON
		var chunk genai.GenerateContentResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			// If this is the last line and it's incomplete, keep it in buffer
			if i == len(lines)-1 {
				remainingBuffer = line
			}
			continue
		}

		chunks = append(chunks, chunk)
	}

	// Update buffer with remaining incomplete data
	a.bufferedBody = remainingBuffer

	return chunks, nil
}

// ResponseError handles error responses from GCP Vertex AI.
func (a *audioTranscriptionOpenAIToGCPVertexAITranslator) ResponseError(headers map[string]string, body io.Reader) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	return nil, nil, nil
}
