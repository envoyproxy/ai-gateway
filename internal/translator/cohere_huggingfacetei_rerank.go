// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	teischema "github.com/envoyproxy/ai-gateway/internal/apischema/huggingfacetei"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// teiRerankPath is the native rerank endpoint of a TEI server. TEI serves a single
// model per instance, so the path is fixed and unversioned.
const teiRerankPath = "/rerank"

// NewRerankCohereToHuggingFaceTEITranslator implements [Factory] for Cohere Rerank v2 to
// HuggingFace Text Embeddings Inference (TEI) translation.
func NewRerankCohereToHuggingFaceTEITranslator(modelNameOverride internalapi.ModelNameOverride) CohereRerankTranslator {
	return &cohereToHuggingFaceTEITranslatorV2Rerank{modelNameOverride: modelNameOverride}
}

// cohereToHuggingFaceTEITranslatorV2Rerank translates Cohere Rerank API v2 requests to the
// native TEI /rerank API and TEI responses back to the Cohere v2 format:
// https://huggingface.github.io/text-embeddings-inference
type cohereToHuggingFaceTEITranslatorV2Rerank struct {
	modelNameOverride internalapi.ModelNameOverride
	// requestModel stores the effective model for this request (override or provided).
	// TEI does not accept or echo a model, so this is only used for reporting.
	requestModel internalapi.RequestModel
	// topN limits the number of results returned. TEI has no top_n parameter, so it is
	// applied to the response by the translator.
	topN *int
}

// RequestBody implements [CohereRerankTranslator.RequestBody].
func (t *cohereToHuggingFaceTEITranslatorV2Rerank) RequestBody(_ []byte, req *cohereschema.RerankV2Request, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	t.requestModel = req.Model
	if t.modelNameOverride != "" {
		t.requestModel = t.modelNameOverride
	}
	t.topN = req.TopN

	teiReq := teischema.RerankRequest{
		Query: req.Query,
		Texts: req.Documents,
	}
	newBody, err = json.Marshal(teiReq)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal TEI rerank request: %w", err)
	}

	newHeaders = []internalapi.Header{
		{pathHeaderName, teiRerankPath},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// ResponseHeaders implements [CohereRerankTranslator.ResponseHeaders].
func (t *cohereToHuggingFaceTEITranslatorV2Rerank) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [CohereRerankTranslator.ResponseBody].
// TEI does not report token usage, so no token accounting is possible.
func (t *cohereToHuggingFaceTEITranslatorV2Rerank) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracingapi.RerankSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	var teiResp teischema.RerankResponse
	if decodeErr := json.NewDecoder(body).Decode(&teiResp); decodeErr != nil {
		return nil, nil, tokenUsage, t.requestModel, fmt.Errorf("failed to unmarshal body: %w", decodeErr)
	}

	// TEI returns results sorted by score in descending order; top_n only needs truncation.
	results := teiResp
	if t.topN != nil && *t.topN >= 0 && *t.topN < len(results) {
		results = results[:*t.topN]
	}
	resp := cohereschema.RerankV2Response{
		Results: make([]*cohereschema.RerankV2Result, len(results)),
	}
	for i, r := range results {
		resp.Results[i] = &cohereschema.RerankV2Result{
			Index:          r.Index,
			RelevanceScore: r.Score,
		}
	}

	// Record the response in the span if successful.
	if span != nil {
		span.RecordResponse(&resp)
	}

	newBody, err = json.Marshal(resp)
	if err != nil {
		return nil, nil, tokenUsage, t.requestModel, fmt.Errorf("failed to marshal body: %w", err)
	}
	newHeaders = []internalapi.Header{
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}

	// TEI responses do not echo a model; report the effective request model.
	responseModel = t.requestModel
	return
}

// ResponseError implements [CohereRerankTranslator.ResponseError].
// TEI errors ({"error": "...", "error_type": "..."}) and non-JSON errors are wrapped
// into a Cohere v2 error body.
func (t *cohereToHuggingFaceTEITranslatorV2Rerank) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	buf, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read error body: %w", err)
	}
	message := string(buf)
	if v, ok := respHeaders[contentTypeHeaderName]; ok && strings.Contains(v, jsonContentType) {
		var teiErr teischema.Error
		if unmarshalErr := json.Unmarshal(buf, &teiErr); unmarshalErr == nil && teiErr.Error != "" {
			message = teiErr.Error
		}
	}
	cohereErr := cohereschema.RerankV2Error{
		Message: &message,
	}
	newBody, err = json.Marshal(cohereErr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
	}
	newHeaders = append(newHeaders,
		internalapi.Header{contentTypeHeaderName, jsonContentType},
		internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	)
	return
}
