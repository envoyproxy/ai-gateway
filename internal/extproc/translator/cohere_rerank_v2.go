// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strconv"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/tidwall/sjson"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// NewRerankCohereToCohereTranslator implements [Factory] for Cohere Rerank v2 translation.
func NewRerankCohereToCohereTranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) CohereRerankTranslator {
	return &cohereToCohereTranslatorV2Rerank{modelNameOverride: modelNameOverride, path: path.Join("/", apiVersion, "rerank")} // e.g., /v2/rerank
}

// cohereToCohereTranslatorV2Rerank is a passthrough translator for Cohere Rerank API v2.
// May apply model overrides but otherwise preserves the Cohere format:
// https://docs.cohere.com/reference/rerank
type cohereToCohereTranslatorV2Rerank struct {
	modelNameOverride internalapi.ModelNameOverride
	// The path of the rerank endpoint to be used for the request. It is prefixed with the API path prefix.
	path string
}

// RequestBody implements [CohereRerankTranslator.RequestBody].
func (t *cohereToCohereTranslatorV2Rerank) RequestBody(original []byte, _ *cohereschema.RerankV2Request, onRetry bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	var newBody []byte
	if t.modelNameOverride != "" {
		// Override the model if configured.
		newBody, err = sjson.SetBytesOptions(original, "model", t.modelNameOverride, sjsonOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model name: %w", err)
		}
	}

	// Always set the path header to the rerank endpoint so that the request is routed correctly.
	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{Key: ":path", RawValue: []byte(t.path)}},
		},
	}

	if onRetry && len(newBody) == 0 {
		newBody = original
	}

	if len(newBody) > 0 {
		bodyMutation = &extprocv3.BodyMutation{Mutation: &extprocv3.BodyMutation_Body{Body: newBody}}
		headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{Header: &corev3.HeaderValue{
			Key:      "content-length",
			RawValue: []byte(strconv.Itoa(len(newBody))),
		}})
	}
	return
}

// ResponseHeaders implements [CohereRerankTranslator.ResponseHeaders].
func (t *cohereToCohereTranslatorV2Rerank) ResponseHeaders(map[string]string) (headerMutation *extprocv3.HeaderMutation, err error) {
	return nil, nil
}

// ResponseBody implements [CohereRerankTranslator.ResponseBody].
// For rerank, token usage is provided via meta.tokens.input_tokens when available.
func (t *cohereToCohereTranslatorV2Rerank) ResponseBody(_ map[string]string, body io.Reader, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	var resp cohereschema.RerankV2Response
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to unmarshal body: %w", err)
	}

	// Token accounting: rerank only has input tokens; output tokens do not apply.
	if resp.Meta != nil && resp.Meta.Tokens != nil {
		if resp.Meta.Tokens.InputTokens != nil {
			// Cohere uses float; round down to uint32 like embeddings.
			tokenUsage.InputTokens = uint32(*resp.Meta.Tokens.InputTokens) //nolint:gosec
			tokenUsage.TotalTokens = tokenUsage.InputTokens
		}
		if resp.Meta.Tokens.OutputTokens != nil {
			tokenUsage.OutputTokens = uint32(*resp.Meta.Tokens.OutputTokens) //nolint:gosec
			tokenUsage.TotalTokens += tokenUsage.OutputTokens
		}
	}

	// Cohere rerank responses do not echo model; report empty responseModel.
	responseModel = ""
	return
}

// ResponseError implements [CohereRerankTranslator.ResponseError].
// If connection fails or a non-JSON error is returned, wrap it into a JSON error body.
func (t *cohereToCohereTranslatorV2Rerank) ResponseError(respHeaders map[string]string, body io.Reader) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	if v, ok := respHeaders[contentTypeHeaderName]; ok && v != jsonContentType {
		buf, err := io.ReadAll(body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read error body: %w", err)
		}
		message := string(buf)
		// Wrap as a minimal Cohere v2 error JSON for consistency.
		cohereErr := cohereschema.RerankV2Error{
			Message: &message,
			ID:      &statusCode,
		}
		mut := &extprocv3.BodyMutation_Body{}
		mut.Body, err = json.Marshal(cohereErr)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
		}
		headerMutation = &extprocv3.HeaderMutation{}
		setContentLength(headerMutation, mut.Body)
		return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, nil
	}
	return nil, nil, nil
}
