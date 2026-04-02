// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	internaljson "github.com/envoyproxy/ai-gateway/internal/json"
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
// Passes the response through unchanged and extracts token usage from usageMetadata.
func (g *geminiToGCPVertexAITranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ tracingapi.Span[struct{}, struct{}]) (
	newHeaders []internalapi.Header, mutatedBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	mutatedBody, err = io.ReadAll(body)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", err
	}
	newHeaders = []internalapi.Header{
		{contentLengthHeaderName, strconv.Itoa(len(mutatedBody))},
	}
	responseModel = g.model

	// Extract token counts from usageMetadata for cost accounting and rate limiting.
	// The response body is passed through to the client unchanged.
	if g.streaming {
		tokenUsage = extractUsageFromSSE(mutatedBody)
	} else {
		var resp gcp.GenerateContentResponse
		if jsonErr := internaljson.Unmarshal(mutatedBody, &resp); jsonErr == nil && resp.UsageMetadata != nil {
			applyUsageMetadata(resp.UsageMetadata, &tokenUsage)
		}
	}
	return
}

// applyUsageMetadata populates a TokenUsage from a Gemini UsageMetadata response field.
func applyUsageMetadata(u *gcp.GenerateContentUsageMetadata, usage *metrics.TokenUsage) {
	usage.SetInputTokens(u.PromptTokenCount)
	usage.SetOutputTokens(u.CandidatesTokenCount)
	usage.SetTotalTokens(u.TotalTokenCount)
	if u.CachedContentTokenCount > 0 {
		usage.SetCachedInputTokens(u.CachedContentTokenCount)
	}
}

// extractUsageFromSSE scans a Gemini SSE response body for the usageMetadata field.
// Vertex AI includes usageMetadata in the final data event of a streamGenerateContent response.
func extractUsageFromSSE(body []byte) (usage metrics.TokenUsage) {
	const dataPrefix = "data: "
	for line, rest, ok := bytes.Cut(body, []byte("\n")); ok || len(line) > 0; line, rest, ok = bytes.Cut(rest, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte(dataPrefix)) {
			if !ok {
				break
			}
			continue
		}
		payload := line[len(dataPrefix):]
		var resp gcp.GenerateContentResponse
		if internaljson.Unmarshal(payload, &resp) == nil && resp.UsageMetadata != nil {
			applyUsageMetadata(resp.UsageMetadata, &usage)
			// Don't break — take the last event that carries usageMetadata.
		}
		if !ok {
			break
		}
	}
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
