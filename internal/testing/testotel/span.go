package testotel

import (
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// MockSpan is a mock implementation of api.ChatCompletionSpan for testing purposes.
type MockSpan struct {
	Resp        *openai.ChatCompletionResponse
	RespChunks  []*openai.ChatCompletionResponseChunk
	ErrorStatus int
	ErrBody     string
}

// RecordResponseChunk implements api.ChatCompletionSpan.
func (s *MockSpan) RecordResponseChunk(resp *openai.ChatCompletionResponseChunk) {
	s.RespChunks = append(s.RespChunks, resp)
}

// RecordResponse implements api.ChatCompletionSpan.
func (s *MockSpan) RecordResponse(resp *openai.ChatCompletionResponse) {
	s.Resp = resp
}

// EndSpanOnError implements api.ChatCompletionSpan.
func (s *MockSpan) EndSpanOnError(statusCode int, body []byte) {
	s.ErrorStatus = statusCode
	s.ErrBody = string(body)
}

// EndSpan implements api.ChatCompletionSpan.
func (s *MockSpan) EndSpan() {}
