// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestNewAudioSpeechOpenAIToGCPVertexAITranslator(t *testing.T) {
	translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("override-model")
	require.NotNil(t, translator)
	
	impl, ok := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
	require.True(t, ok)
	require.Equal(t, internalapi.ModelNameOverride("override-model"), impl.modelNameOverride)
	require.True(t, impl.usePublisherPath)
}

func TestAudioSpeechOpenAIToGCPVertexAITranslator_SetUseGeminiDirectPath(t *testing.T) {
	translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
	impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
	
	impl.SetUseGeminiDirectPath(true)
	require.False(t, impl.usePublisherPath)
	
	impl.SetUseGeminiDirectPath(false)
	require.True(t, impl.usePublisherPath)
}

func TestAudioSpeechOpenAIToGCPVertexAITranslator_RequestBody(t *testing.T) {
	t.Run("basic request", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		
		req := &openai.AudioSpeechRequest{
			Model: "tts-1",
			Input: "Hello, world!",
			Voice: "alloy",
		}
		rawBody, _ := json.Marshal(req)
		
		headerMutation, bodyMutation, err := translator.RequestBody(rawBody, req, false)
		require.NoError(t, err)
		require.NotNil(t, headerMutation)
		require.NotNil(t, bodyMutation)
		
		require.Len(t, headerMutation.SetHeaders, 1)
		require.Equal(t, ":path", headerMutation.SetHeaders[0].Header.Key)
		require.Contains(t, string(headerMutation.SetHeaders[0].Header.RawValue), "streamGenerateContent")
	})

	t.Run("model override", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("gemini-1.5-pro")
		
		req := &openai.AudioSpeechRequest{
			Model: "tts-1",
			Input: "Hello",
			Voice: "alloy",
		}
		
		_, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		
		impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
		require.Equal(t, internalapi.RequestModel("gemini-1.5-pro"), impl.requestModel)
	})

	t.Run("voice mapping", func(t *testing.T) {
		voices := []string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}
		
		for _, voice := range voices {
			translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
			
			req := &openai.AudioSpeechRequest{
				Model: "tts-1",
				Input: "Test",
				Voice: voice,
			}
			
			_, bodyMutation, err := translator.RequestBody(nil, req, false)
			require.NoError(t, err)
			require.NotNil(t, bodyMutation)
			
			body := bodyMutation.GetBody()
			require.NotNil(t, body)
			
			var parsedBody map[string]interface{}
			err = json.Unmarshal(body, &parsedBody)
			require.NoError(t, err)
		}
	})

	t.Run("gemini direct path", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
		impl.SetUseGeminiDirectPath(true)
		
		req := &openai.AudioSpeechRequest{
			Model: "gemini-1.5-pro",
			Input: "Test",
			Voice: "alloy",
		}
		
		headerMutation, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		
		path := string(headerMutation.SetHeaders[0].Header.RawValue)
		require.Contains(t, path, "/v1beta/models")
	})

	t.Run("publisher path", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
		impl.SetUseGeminiDirectPath(false)
		
		req := &openai.AudioSpeechRequest{
			Model: "gemini-1.5-pro",
			Input: "Test",
			Voice: "alloy",
		}
		
		headerMutation, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		
		path := string(headerMutation.SetHeaders[0].Header.RawValue)
		require.Contains(t, path, "publishers/")
	})
}

func TestAudioSpeechOpenAIToGCPVertexAITranslator_ResponseHeaders(t *testing.T) {
	t.Run("streaming response", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
		impl.stream = true
		
		headerMutation, err := translator.ResponseHeaders(nil)
		require.NoError(t, err)
		require.NotNil(t, headerMutation)
		require.Len(t, headerMutation.SetHeaders, 1)
		require.Equal(t, "content-type", headerMutation.SetHeaders[0].Header.Key)
		require.Equal(t, "text/event-stream", string(headerMutation.SetHeaders[0].Header.RawValue))
	})

	t.Run("non-streaming response", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
		impl.stream = false
		
		headerMutation, err := translator.ResponseHeaders(nil)
		require.NoError(t, err)
		require.Nil(t, headerMutation)
	})
}

func TestAudioSpeechOpenAIToGCPVertexAITranslator_ResponseBody(t *testing.T) {
	t.Run("streaming with audio data", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
		impl.stream = true
		impl.requestModel = "gemini-1.5-pro"
		
		resp := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{
								InlineData: &genai.Blob{
									MIMEType: "audio/wav",
									Data:     []byte("audio-data"),
								},
							},
						},
					},
				},
		},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     5,
			CandidatesTokenCount: 3,
			TotalTokenCount:      8,
		},
	}
	
	respBody, _ := json.Marshal(resp)
	sseBody := []byte("data: " + string(respBody) + "\n")
	
	_, bodyMutation, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader(sseBody), true)
	require.NoError(t, err)
	require.NotNil(t, bodyMutation)
	require.Equal(t, uint32(5), tokenUsage.InputTokens)
	require.Equal(t, uint32(3), tokenUsage.OutputTokens)
	require.Equal(t, uint32(8), tokenUsage.TotalTokens)
		require.Equal(t, internalapi.ResponseModel("gemini-1.5-pro"), responseModel)
	})

	t.Run("streaming multiple chunks", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
		impl.stream = true
		
		chunk1 := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{
								InlineData: &genai.Blob{
									Data: []byte("chunk1"),
								},
							},
						},
					},
				},
			},
		}
		
		chunk2 := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{
								InlineData: &genai.Blob{
									Data: []byte("chunk2"),
								},
							},
						},
					},
				},
			},
		}
		
		body1, _ := json.Marshal(chunk1)
		body2, _ := json.Marshal(chunk2)
		sseBody := []byte("data: " + string(body1) + "\n\ndata: " + string(body2) + "\n\n")
		
		_, bodyMutation, _, _, err := translator.ResponseBody(nil, bytes.NewReader(sseBody), true)
		require.NoError(t, err)
		require.NotNil(t, bodyMutation)
		
		body := bodyMutation.GetBody()
		require.NotNil(t, body)
		require.Contains(t, string(body), "chunk")
	})

	t.Run("non-streaming", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
		impl.stream = false
		
		_, bodyMutation, tokenUsage, _, err := translator.ResponseBody(nil, bytes.NewReader([]byte("")), true)
		require.NoError(t, err)
		require.Nil(t, bodyMutation)
		require.Equal(t, LLMTokenUsage{}, tokenUsage)
	})
}

func TestAudioSpeechOpenAIToGCPVertexAITranslator_ResponseError(t *testing.T) {
	t.Run("gcp error format", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		
		gcpError := map[string]interface{}{
			"error": map[string]interface{}{
				"code":    400,
				"message": "Invalid request",
			},
		}
		errorBody, _ := json.Marshal(gcpError)
		
		_, bodyMutation, err := translator.ResponseError(nil, bytes.NewReader(errorBody))
		require.NoError(t, err)
		require.NotNil(t, bodyMutation)
		
		body := bodyMutation.GetBody()
		var openaiError openai.Error
		err = json.Unmarshal(body, &openaiError)
		require.NoError(t, err)
		require.Equal(t, "GCPVertexAIBackendError", openaiError.Type)
	})

	t.Run("invalid error format", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		
		errorBody := []byte("not json")
		
		_, bodyMutation, err := translator.ResponseError(nil, bytes.NewReader(errorBody))
		require.NoError(t, err)
		require.NotNil(t, bodyMutation)
		require.Equal(t, errorBody, bodyMutation.GetBody())
	})
}

func TestParseGeminiStreamingChunks(t *testing.T) {
	t.Run("valid sse chunks", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
		
		chunk := genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{Text: "test"},
						},
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

	t.Run("multiple chunks", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
		
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
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
		
		sseBody := []byte("data: [DONE]\n")
		
		chunks, err := impl.parseGeminiStreamingChunks(bytes.NewReader(sseBody))
		require.NoError(t, err)
		require.Len(t, chunks, 0)
	})

	t.Run("incomplete chunk buffering", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
		impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
		
		incompleteBody := []byte("data: {\"candidates\":")
		
		chunks, err := impl.parseGeminiStreamingChunks(bytes.NewReader(incompleteBody))
		require.NoError(t, err)
		require.Len(t, chunks, 0)
		require.NotEmpty(t, impl.bufferedBody)
	})
}

func TestMapOpenAIVoiceToGemini(t *testing.T) {
	tests := []struct {
		openaiVoice string
		expected    string
	}{
		{"alloy", "Zephyr"},
		{"echo", "Puck"},
		{"fable", "Aoede"},
		{"onyx", "Fenrir"},
		{"nova", "Kore"},
		{"shimmer", "Thetis"},
		{"unknown", "Zephyr"},
	}
	
	for _, tt := range tests {
		t.Run(tt.openaiVoice, func(t *testing.T) {
			result := mapOpenAIVoiceToGemini(tt.openaiVoice)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestFloatPtr(t *testing.T) {
	result := floatPtr(1.5)
	require.NotNil(t, result)
	require.Equal(t, float32(1.5), *result)
}

func TestStringPtr(t *testing.T) {
	result := stringPtr("test")
	require.NotNil(t, result)
	require.Equal(t, "test", *result)
}

func TestAudioSpeechOpenAIToGCPVertexAITranslator_ReadError(t *testing.T) {
	translator := NewAudioSpeechOpenAIToGCPVertexAITranslator("")
	impl := translator.(*audioSpeechOpenAIToGCPVertexAITranslator)
	impl.stream = true
	
	errorReader := &errorReader{}
	
	_, _, _, _, err := translator.ResponseBody(nil, errorReader, true)
	require.Error(t, err)
}

type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, io.ErrUnexpectedEOF
}

