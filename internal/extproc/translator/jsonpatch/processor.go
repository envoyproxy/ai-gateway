// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package jsonpatch

import (
	"encoding/json"
	"fmt"

	jsonpatch "github.com/evanphx/json-patch/v5"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// Processor handles JSON patch operations for AI Gateway requests.
type Processor struct {
	patches map[string][]openai.JSONPatch
}

// NewProcessor creates a new JSON patch processor.
func NewProcessor(patches map[string][]openai.JSONPatch) (*Processor, error) {
	if len(patches) == 0 {
		return nil, fmt.Errorf("no patches provided")
	}

	// Validate all patches first.
	return &Processor{
		patches: patches,
	}, nil
}

func (p *Processor) HasPatchesForSchema(schemaName string) bool {
	if p.patches == nil {
		return false
	}

	_, anySchemaPresent := p.patches[SchemaKeyAny]
	_, specificSchemaPresent := p.patches[schemaName]

	return anySchemaPresent || specificSchemaPresent
}

// ApplyPatches applies JSON patches to a request body based on the backend schema name.
func (p *Processor) ApplyPatches(body []byte, schemaName string) ([]byte, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("request body cannot be empty")
	}
	if len(p.patches) == 0 {
		return body, nil
	}

	result := body
	var err error

	// Apply "ANY" patches first (these apply to all backends).
	if anyPatches, exists := p.patches[SchemaKeyAny]; exists && len(anyPatches) > 0 {
		result, err = p.applyPatchList(result, anyPatches, SchemaKeyAny)
		if err != nil {
			return nil, fmt.Errorf("failed to apply ANY patches: %w", err)
		}
	}

	// Apply schema-specific patches.
	if schemaPatches, exists := p.patches[schemaName]; exists && len(schemaPatches) > 0 {
		result, err = p.applyPatchList(result, schemaPatches, schemaName)
		if err != nil {
			return nil, fmt.Errorf("failed to apply %s patches: %w", schemaName, err)
		}
	}

	return result, nil
}

// applyPatchList applies a list of patches to the request body.
func (p *Processor) applyPatchList(body []byte, patches []openai.JSONPatch, schemaName string) ([]byte, error) {
	if len(patches) == 0 {
		return body, nil
	}

	// Marshal patches to JSON format.
	patchBytes, err := json.Marshal(patches)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal patches: %w", err)
	}

	// Create JSON patch object.
	patch, err := jsonpatch.DecodePatch(patchBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to decode patches: %w", err)
	}

	// Apply patches to the body.
	result, err := patch.ApplyWithOptions(body, &jsonpatch.ApplyOptions{
		SupportNegativeIndices:   true,
		EnsurePathExistsOnAdd:    true,
		AllowMissingPathOnRemove: false,
		EscapeHTML:               false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to apply patches for schema %s: %w", schemaName, err)
	}

	return result, nil
}

// ExtractPatches extracts JSON patches from the extra_body field.
func ExtractPatches(extraBody *openai.ExtraBody) (map[string][]openai.JSONPatch, error) {
	if extraBody == nil || extraBody.AIGateway == nil || extraBody.AIGateway.JSONPatches == nil {
		return nil, nil
	}

	patches := extraBody.AIGateway.JSONPatches

	// Validate patches before returning.
	if err := ValidatePatches(patches); err != nil {
		return nil, fmt.Errorf("invalid patches in extra_body: %w", err)
	}

	return patches, nil
}
