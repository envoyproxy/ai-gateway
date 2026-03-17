// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package endpointspec

import (
	"cmp"
	"fmt"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/translator"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// GenerateContentEndpointSpec implements [Spec] for Gemini native paths:
//
//	POST /v1beta/models/{model}:generateContent
//	POST /v1beta/models/{model}:streamGenerateContent
//
// The model is not in the request body for this format — it lives in the URL
// path. The factory populates ModelFromPath by parsing the URL path directly.
type GenerateContentEndpointSpec struct {
	// ModelFromPath is the model name extracted from the request URL path.
	ModelFromPath string
	// Streaming is true when the path ends with :streamGenerateContent.
	Streaming bool
}

// ParseBody implements [Spec.ParseBody].
// Model and streaming state come from the spec fields (injected from the URL path),
// not from the body — GenerateContentRequest has no model field.
func (s GenerateContentEndpointSpec) ParseBody(
	body []byte,
	_ bool,
) (internalapi.OriginalModel, *gcp.GenerateContentRequest, bool, []byte, error) {
	var req gcp.GenerateContentRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return "", nil, false, nil, fmt.Errorf("%w: failed to parse JSON for Gemini generateContent: %w",
			internalapi.ErrMalformedRequest, err)
	}
	return s.ModelFromPath, &req, s.Streaming, nil, nil
}

// GetTranslator implements [Spec.GetTranslator].
// If the backend has a ModelNameOverride configured it takes precedence over the
// model extracted from the path; otherwise the path model is used as-is.
func (s GenerateContentEndpointSpec) GetTranslator(
	schema filterapi.VersionedAPISchema,
	modelNameOverride string,
) (translator.GeminiGenerateContentTranslator, error) {
	effectiveModel := cmp.Or(modelNameOverride, s.ModelFromPath)
	switch schema.Name {
	case filterapi.APISchemaGCPVertexAI:
		return translator.NewGeminiToGCPVertexAITranslator(effectiveModel, s.Streaming), nil
	default:
		return nil, fmt.Errorf("Gemini native endpoint only supports GCPVertexAI backend, got: %s", schema.Name)
	}
}

// RedactSensitiveInfoFromRequest implements [Spec.RedactSensitiveInfoFromRequest].
func (GenerateContentEndpointSpec) RedactSensitiveInfoFromRequest(
	req *gcp.GenerateContentRequest,
) (*gcp.GenerateContentRequest, error) {
	// Placeholder — contents and systemInstruction could be redacted in a future pass.
	return req, nil
}

// GenerateContentTracer is the tracing type for Gemini native generate-content requests.
// A noop tracer is used because the passthrough translator does not parse response
// structure deeply enough to emit meaningful spans.
type GenerateContentTracer = tracingapi.RequestTracer[gcp.GenerateContentRequest, struct{}, struct{}]
