// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"io"
	"strconv"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// GeminiGenerateContentTranslator handles Gemini native client requests
// (/v1beta/models/{model}:generateContent) routed to GCP Vertex AI.
//
// Both client and backend speak the same Gemini GenerateContentRequest/Response
// format, so this translator is a near-passthrough: it only rewrites the :path
// header to the full Vertex AI URL and leaves the body unchanged.
type GeminiGenerateContentTranslator = Translator[gcp.GenerateContentRequest, tracingapi.Span[struct{}, struct{}]]

// NewGeminiToGCPVertexAITranslator creates a passthrough translator for the Gemini
// native API. model is the effective model name (from path or backend override).
// streaming controls which GCP method suffix is used.
func NewGeminiToGCPVertexAITranslator(model string, streaming bool) GeminiGenerateContentTranslator {
	return &geminiToGCPVertexAITranslator{model: model, streaming: streaming}
}

type geminiToGCPVertexAITranslator struct {
	model     string
	streaming bool
}

// RequestBody implements [GeminiGenerateContentTranslator.RequestBody].
// Rewrites :path to the full GCP Vertex AI path. Also strips FunctionResponse.ID
// from all parts: the Google AI SDK (used by Gemini CLI in gemini-api auth mode)
// populates this field, but Vertex AI /v1 rejects it as an unknown field.
// Safe to strip: Vertex AI matches function_response to function_call by name, not id.
func (g *geminiToGCPVertexAITranslator) RequestBody(_ []byte, req *gcp.GenerateContentRequest, _ bool) (
	newHeaders []internalapi.Header, mutatedBody []byte, err error,
) {
	needsMutation := false
	for _, content := range req.Contents {
		for _, part := range content.Parts {
			if part.FunctionResponse != nil && part.FunctionResponse.ID != "" {
				part.FunctionResponse.ID = ""
				needsMutation = true
			}
		}
	}
	if needsMutation {
		if mutatedBody, err = json.Marshal(req); err != nil {
			return nil, nil, err
		}
	}

	var path string
	if g.streaming {
		path = buildGCPModelPathSuffix(gcpModelPublisherGoogle, g.model, gcpMethodStreamGenerateContent, "alt=sse")
	} else {
		path = buildGCPModelPathSuffix(gcpModelPublisherGoogle, g.model, gcpMethodGenerateContent)
	}
	newHeaders = []internalapi.Header{
		{pathHeaderName, path},
	}
	return
}

// ResponseHeaders implements [GeminiGenerateContentTranslator.ResponseHeaders].
func (g *geminiToGCPVertexAITranslator) ResponseHeaders(_ map[string]string) (newHeaders []internalapi.Header, err error) {
	if g.streaming {
		newHeaders = []internalapi.Header{{contentTypeHeaderName, eventStreamContentType}}
	}
	return
}

// ResponseBody implements [GeminiGenerateContentTranslator.ResponseBody].
// Passes the response through unchanged; token usage is not extracted (passthrough).
func (g *geminiToGCPVertexAITranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ tracingapi.Span[struct{}, struct{}]) (
	newHeaders []internalapi.Header, mutatedBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	// Read to drain the body so the upstream processor can continue, but return
	// nil mutatedBody so the original bytes are passed through untouched.
	mutatedBody, err = io.ReadAll(body)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", err
	}
	newHeaders = []internalapi.Header{
		{contentLengthHeaderName, strconv.Itoa(len(mutatedBody))},
	}
	responseModel = g.model
	return
}

// ResponseError implements [GeminiGenerateContentTranslator.ResponseError].
// Error responses from GCP Vertex AI are already in Gemini format — pass through.
func (g *geminiToGCPVertexAITranslator) ResponseError(_ map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, mutatedBody []byte, err error,
) {
	mutatedBody, err = io.ReadAll(body)
	if err != nil {
		return nil, nil, err
	}
	newHeaders = []internalapi.Header{
		{contentLengthHeaderName, strconv.Itoa(len(mutatedBody))},
	}
	return
}
