// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestNewAudioTranscriptionOpenAIToGCPVertexAITranslator(t *testing.T) {
	translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("override-model")
	require.NotNil(t, translator)

	impl, ok := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)
	require.True(t, ok)
	require.Equal(t, internalapi.ModelNameOverride("override-model"), impl.modelNameOverride)
}

func TestAudioTranscriptionOpenAIToGCPVertexAITranslator_SetContentType(t *testing.T) {
	translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
	impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)

	impl.SetContentType("multipart/form-data; boundary=test")
	require.Equal(t, "multipart/form-data; boundary=test", impl.contentType)
}

func TestDetectAudioMimeType(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"test.wav", "audio/wav"},
		{"test.mp3", "audio/mpeg"},
		{"test.m4a", "audio/mp4"},
		{"test.ogg", "audio/ogg"},
		{"test.flac", "audio/flac"},
		{"test.webm", "audio/webm"},
		{"test.aac", "audio/aac"},
		{"test.unknown", "audio/wav"},
		{"TEST.WAV", "audio/wav"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := detectAudioMimeType(tt.filename)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestAudioTranscriptionOpenAIToGCPVertexAITranslator_RequestBody(t *testing.T) {
	t.Run("basic request with multipart", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)

		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		part, _ := writer.CreateFormFile("file", "test.wav")
		_, _ = part.Write([]byte("audio data"))

		field, _ := writer.CreateFormField("model")
		_, _ = field.Write([]byte("whisper-1"))

		writer.Close()

		impl.SetContentType(writer.FormDataContentType())

		req := &openai.AudioTranscriptionRequest{
			Model: "whisper-1",
		}

		headerMutation, bodyMutation, err := translator.RequestBody(buf.Bytes(), req, false)
		require.NoError(t, err)
		require.NotNil(t, headerMutation)
		require.NotNil(t, bodyMutation)

		require.Len(t, headerMutation.SetHeaders, 2)
		require.Equal(t, ":path", headerMutation.SetHeaders[0].Header.Key)
		require.Contains(t, string(headerMutation.SetHeaders[0].Header.RawValue), "streamGenerateContent")
	})

	t.Run("with custom prompt", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)

		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		part, _ := writer.CreateFormFile("file", "test.wav")
		_, _ = part.Write([]byte("audio data"))

		writer.Close()

		impl.SetContentType(writer.FormDataContentType())

		req := &openai.AudioTranscriptionRequest{
			Model:  "whisper-1",
			Prompt: "Custom transcription prompt",
		}

		_, bodyMutation, err := translator.RequestBody(buf.Bytes(), req, false)
		require.NoError(t, err)
		require.NotNil(t, bodyMutation)
	})

	t.Run("model override", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("gemini-1.5-pro")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)

		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		part, _ := writer.CreateFormFile("file", "test.wav")
		_, _ = part.Write([]byte("audio data"))

		writer.Close()

		impl.SetContentType(writer.FormDataContentType())

		req := &openai.AudioTranscriptionRequest{
			Model: "whisper-1",
		}

		_, _, err := translator.RequestBody(buf.Bytes(), req, false)
		require.NoError(t, err)
		require.Equal(t, internalapi.RequestModel("gemini-1.5-pro"), impl.requestModel)
	})

	t.Run("different audio formats", func(t *testing.T) {
		formats := []struct {
			filename string
			mimeType string
		}{
			{"test.mp3", "audio/mpeg"},
			{"test.m4a", "audio/mp4"},
			{"test.flac", "audio/flac"},
		}

		for _, format := range formats {
			translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
			impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)

			var buf bytes.Buffer
			writer := multipart.NewWriter(&buf)

			part, _ := writer.CreateFormFile("file", format.filename)
			_, _ = part.Write([]byte("audio data"))

			writer.Close()

			impl.SetContentType(writer.FormDataContentType())

			req := &openai.AudioTranscriptionRequest{Model: "whisper-1"}

			_, bodyMutation, err := translator.RequestBody(buf.Bytes(), req, false)
			require.NoError(t, err)
			require.NotNil(t, bodyMutation)
		}
	})

	t.Run("fallback without multipart", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")

		audioData := []byte("raw audio data")
		req := &openai.AudioTranscriptionRequest{Model: "whisper-1"}

		_, bodyMutation, err := translator.RequestBody(audioData, req, false)
		require.NoError(t, err)
		require.NotNil(t, bodyMutation)
	})
}

func TestAudioTranscriptionOpenAIToGCPVertexAITranslator_ResponseHeaders(t *testing.T) {
	t.Run("streaming response", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)
		impl.stream = true

		headerMutation, err := translator.ResponseHeaders(nil)
		require.NoError(t, err)
		require.NotNil(t, headerMutation)
		require.Len(t, headerMutation.SetHeaders, 1)
		require.Equal(t, "content-type", headerMutation.SetHeaders[0].Header.Key)
		require.Equal(t, "text/event-stream", headerMutation.SetHeaders[0].Header.Value)
	})

	t.Run("non-streaming response", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)
		impl.stream = false

		headerMutation, err := translator.ResponseHeaders(nil)
		require.NoError(t, err)
		require.NotNil(t, headerMutation)
		require.Equal(t, "application/json", headerMutation.SetHeaders[0].Header.Value)
	})
}

func TestAudioTranscriptionOpenAIToGCPVertexAITranslator_HandleStreamingResponse(t *testing.T) {
	t.Run("complete streaming response", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)
		impl.stream = true
		impl.requestModel = "gemini-1.5-pro"

		resp := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{Text: "This is the transcription text"},
						},
					},
				},
			},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     50,
				CandidatesTokenCount: 100,
				TotalTokenCount:      150,
			},
		}

		respBody, _ := json.Marshal(resp)
		sseBody := []byte("data: " + string(respBody) + "\n\n")

		_, bodyMutation, tokenUsage, responseModel, err := impl.handleStreamingResponse(bytes.NewReader(sseBody), true)
		require.NoError(t, err)
		require.NotNil(t, bodyMutation)

		body := bodyMutation.GetBody()
		var openaiResp openai.AudioTranscriptionResponse
		err = json.Unmarshal(body, &openaiResp)
		require.NoError(t, err)
		require.Equal(t, "This is the transcription text", openaiResp.Text)

		inputTokens, _ := tokenUsage.InputTokens()
		outputTokens, _ := tokenUsage.OutputTokens()
		totalTokens, _ := tokenUsage.TotalTokens()
		require.Equal(t, uint32(50), inputTokens)
		require.Equal(t, uint32(100), outputTokens)
		require.Equal(t, uint32(150), totalTokens)
		require.Equal(t, internalapi.ResponseModel("gemini-1.5-pro"), responseModel)
	})

	t.Run("not end of stream", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)
		impl.stream = true

		resp := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{Text: "partial text"},
						},
					},
				},
			},
		}

		respBody, _ := json.Marshal(resp)
		sseBody := []byte("data: " + string(respBody) + "\n\n")

		_, bodyMutation, _, _, err := impl.handleStreamingResponse(bytes.NewReader(sseBody), false)
		require.NoError(t, err)
		require.Nil(t, bodyMutation)
	})

	t.Run("multiple chunks", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)
		impl.stream = true
		impl.requestModel = "gemini-1.5-pro"

		chunk1 := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{{Text: "First part"}},
					},
				},
			},
		}

		chunk2 := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{{Text: " second part"}},
					},
				},
			},
		}

		body1, _ := json.Marshal(chunk1)
		body2, _ := json.Marshal(chunk2)
		sseBody := []byte("data: " + string(body1) + "\n\ndata: " + string(body2) + "\n\n")

		_, bodyMutation, _, _, err := impl.handleStreamingResponse(bytes.NewReader(sseBody), true)
		require.NoError(t, err)
		require.NotNil(t, bodyMutation)

		body := bodyMutation.GetBody()
		var openaiResp openai.AudioTranscriptionResponse
		err = json.Unmarshal(body, &openaiResp)
		require.NoError(t, err)
		require.Equal(t, "First part second part", openaiResp.Text)
	})
}

func TestAudioTranscriptionOpenAIToGCPVertexAITranslator_HandleNonStreamingResponse(t *testing.T) {
	t.Run("complete non-streaming response", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)
		impl.stream = false
		impl.requestModel = "gemini-1.5-pro"

		resp := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{Text: "Complete transcription"},
						},
					},
				},
			},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     30,
				CandidatesTokenCount: 50,
				TotalTokenCount:      80,
			},
		}

		respBody, _ := json.Marshal(resp)

		_, bodyMutation, tokenUsage, responseModel, err := impl.handleNonStreamingResponse(bytes.NewReader(respBody), true)
		require.NoError(t, err)
		require.NotNil(t, bodyMutation)

		body := bodyMutation.GetBody()
		var openaiResp openai.AudioTranscriptionResponse
		err = json.Unmarshal(body, &openaiResp)
		require.NoError(t, err)
		require.Equal(t, "Complete transcription", openaiResp.Text)

		inputTokens, _ := tokenUsage.InputTokens()
		outputTokens, _ := tokenUsage.OutputTokens()
		totalTokens, _ := tokenUsage.TotalTokens()
		require.Equal(t, uint32(30), inputTokens)
		require.Equal(t, uint32(50), outputTokens)
		require.Equal(t, uint32(80), totalTokens)
		require.Equal(t, internalapi.ResponseModel("gemini-1.5-pro"), responseModel)
	})

	t.Run("not end of stream", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)
		impl.stream = false

		_, bodyMutation, _, _, err := impl.handleNonStreamingResponse(bytes.NewReader([]byte("")), false)
		require.NoError(t, err)
		require.Nil(t, bodyMutation)
	})

	t.Run("invalid json", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)
		impl.stream = false

		_, _, _, _, err := impl.handleNonStreamingResponse(bytes.NewReader([]byte("invalid")), true)
		require.Error(t, err)
	})
}

func TestAudioTranscriptionOpenAIToGCPVertexAITranslator_ResponseBody(t *testing.T) {
	t.Run("streaming mode", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)
		impl.stream = true
		impl.requestModel = "gemini-1.5-pro"

		resp := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{{Text: "test"}},
					},
				},
			},
		}

		respBody, _ := json.Marshal(resp)
		sseBody := []byte("data: " + string(respBody) + "\n\n")

		_, bodyMutation, _, _, err := translator.ResponseBody(nil, bytes.NewReader(sseBody), true)
		require.NoError(t, err)
		require.NotNil(t, bodyMutation)
	})

	t.Run("non-streaming mode", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)
		impl.stream = false
		impl.requestModel = "gemini-1.5-pro"

		resp := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{{Text: "test"}},
					},
				},
			},
		}

		respBody, _ := json.Marshal(resp)

		_, bodyMutation, _, _, err := translator.ResponseBody(nil, bytes.NewReader(respBody), true)
		require.NoError(t, err)
		require.NotNil(t, bodyMutation)
	})
}

func TestAudioTranscriptionOpenAIToGCPVertexAITranslator_ResponseError(t *testing.T) {
	translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")

	headerMutation, bodyMutation, err := translator.ResponseError(nil, nil)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
}

func TestParseGeminiStreamingChunks_Transcription(t *testing.T) {
	t.Run("valid sse chunks", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)

		chunk := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{{Text: "test"}},
					},
				},
			},
		}
		chunkBody, _ := json.Marshal(chunk)
		sseBody := []byte("data: " + string(chunkBody) + "\n\n")

		chunks, err := impl.parseGeminiStreamingChunks(bytes.NewReader(sseBody))
		require.NoError(t, err)
		require.Len(t, chunks, 1)
	})

	t.Run("multiple chunks with newlines", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)

		chunk1 := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: []*genai.Part{{Text: "test1"}}}}},
		}
		chunk2 := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: []*genai.Part{{Text: "test2"}}}}},
		}

		body1, _ := json.Marshal(chunk1)
		body2, _ := json.Marshal(chunk2)
		sseBody := []byte("data: " + string(body1) + "\n\ndata: " + string(body2) + "\n\n")

		chunks, err := impl.parseGeminiStreamingChunks(bytes.NewReader(sseBody))
		require.NoError(t, err)
		require.Len(t, chunks, 2)
	})

	t.Run("done marker", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)

		sseBody := []byte("data: [DONE]\n\n")

		chunks, err := impl.parseGeminiStreamingChunks(bytes.NewReader(sseBody))
		require.NoError(t, err)
		require.Empty(t, chunks)
	})

	t.Run("windows line endings", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)

		chunk := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: []*genai.Part{{Text: "test"}}}}},
		}
		chunkBody, _ := json.Marshal(chunk)
		sseBody := []byte("data: " + string(chunkBody) + "\r\n\r\n")

		chunks, err := impl.parseGeminiStreamingChunks(bytes.NewReader(sseBody))
		require.NoError(t, err)
		require.Len(t, chunks, 1)
	})

	t.Run("incomplete chunk buffering", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)

		incompleteBody := []byte("data: {\"candidates\":")

		chunks, err := impl.parseGeminiStreamingChunks(bytes.NewReader(incompleteBody))
		require.NoError(t, err)
		require.Empty(t, chunks)
	})

	t.Run("empty lines", func(t *testing.T) {
		translator := NewAudioTranscriptionOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioTranscriptionOpenAIToGCPVertexAITranslator)

		chunk := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: []*genai.Part{{Text: "test"}}}}},
		}
		chunkBody, _ := json.Marshal(chunk)
		sseBody := []byte("\n\ndata: " + string(chunkBody) + "\n\n\n\n")

		chunks, err := impl.parseGeminiStreamingChunks(bytes.NewReader(sseBody))
		require.NoError(t, err)
		require.Len(t, chunks, 1)
	})
}
