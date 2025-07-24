// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package jsonpatch

import (
	"fmt"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// ValidatePatches validates a collection of JSON patches for security and correctness.
func ValidatePatches(patches map[string][]openai.JSONPatch) error {
	totalPatchCount := 0

	for schemaName, patchList := range patches {
		if len(patchList) == 0 {
			continue
		}

		totalPatchCount += len(patchList)
		if totalPatchCount > MaxPatchCount {
			return fmt.Errorf("total patch count %d exceeds maximum allowed %d", totalPatchCount, MaxPatchCount)
		}

		// Validate schema name.
		if ok := filterapi.APISchemaName(schemaName).IsValid(); !(ok || schemaName == SchemaKeyAny) {
			return fmt.Errorf("invalid schema name: %s", schemaName)
		}

		// Validate individual patches.
		for i, patch := range patchList {
			if err := ValidatePatch(patch); err != nil {
				return fmt.Errorf("invalid patch %d for schema %s: %w", i, schemaName, err)
			}
		}
	}

	return nil
}

// ValidatePatch validates a single JSON patch operation.
func ValidatePatch(patch openai.JSONPatch) error {
	// Validate operation.
	if !IsSupportedOperation(patch.Op) {
		return fmt.Errorf("unsupported operation: %s", patch.Op)
	}

	// Validate path.
	if patch.Path == "" {
		return fmt.Errorf("path is required")
	}

	if err := ValidateJSONPointer(patch.Path); err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	// Validate value.
	if patch.Value == nil {
		return fmt.Errorf("value is required for %s operation", patch.Op)
	}

	return nil
}
