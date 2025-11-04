// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package bodymutator

import (
	"encoding/json"
	"fmt"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

type BodyMutator struct {
	// originalBody is the original request body for retry scenarios
	originalBody []byte

	// bodyMutations is the body mutations to apply
	bodyMutations *filterapi.HTTPBodyMutation
}

func NewBodyMutator(bodyMutations *filterapi.HTTPBodyMutation, originalBody []byte) *BodyMutator {
	return &BodyMutator{
		originalBody:  originalBody,
		bodyMutations: bodyMutations,
	}
}

// Mutate mutates the request body based on the body mutations and restores original body if mutated previously.
func (b *BodyMutator) Mutate(requestBody []byte, onRetry bool) ([]byte, error) {
	if b.bodyMutations == nil {
		return requestBody, nil
	}

	if onRetry && b.originalBody != nil {
		// On retry, restore the original body first
		requestBody = b.originalBody
	}

	// Parse the request body as JSON
	var bodyData map[string]interface{}
	if err := json.Unmarshal(requestBody, &bodyData); err != nil {
		return nil, fmt.Errorf("failed to parse request body as JSON: %w", err)
	}

	// Apply removals first
	if len(b.bodyMutations.Remove) > 0 {
		for _, fieldName := range b.bodyMutations.Remove {
			if fieldName != "" {
				delete(bodyData, fieldName)
			}
		}
	}

	// Apply sets
	if len(b.bodyMutations.Set) > 0 {
		for _, field := range b.bodyMutations.Set {
			if field.Path != "" {
				// Parse the JSON value
				var parsedValue interface{}
				if err := json.Unmarshal([]byte(field.Value), &parsedValue); err != nil {
					// If it's not valid JSON, treat it as a string
					parsedValue = field.Value
				}
				bodyData[field.Path] = parsedValue
			}
		}
	}

	// Marshal back to JSON
	mutatedBody, err := json.Marshal(bodyData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal mutated body: %w", err)
	}

	return mutatedBody, nil
}
