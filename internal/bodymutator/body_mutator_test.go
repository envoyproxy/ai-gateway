// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package bodymutator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestBodyMutator_Mutate_Set(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{Path: "service_tier", Value: "\"scale\""},
			{Path: "max_tokens", Value: "100"},
			{Path: "temperature", Value: "0.7"},
		},
	}

	originalBody := []byte(`{"model": "gpt-4", "service_tier": "default", "messages": []}`)
	mutator := NewBodyMutator(bodyMutations, originalBody)

	requestBody := []byte(`{"model": "gpt-4", "service_tier": "default", "messages": []}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	require.Equal(t, "scale", result["service_tier"])
	require.Equal(t, float64(100), result["max_tokens"])
	require.Equal(t, 0.7, result["temperature"])
	require.Equal(t, "gpt-4", result["model"])
}

func TestBodyMutator_Mutate_Remove(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Remove: []string{"service_tier", "internal_flag"},
	}

	originalBody := []byte(`{"model": "gpt-4", "service_tier": "default", "internal_flag": true, "messages": []}`)
	mutator := NewBodyMutator(bodyMutations, originalBody)

	requestBody := []byte(`{"model": "gpt-4", "service_tier": "default", "internal_flag": true, "messages": []}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	require.NotContains(t, result, "service_tier")
	require.NotContains(t, result, "internal_flag")
	require.Equal(t, "gpt-4", result["model"])
	require.Contains(t, result, "messages")
}

func TestBodyMutator_Mutate_SetAndRemove(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{Path: "service_tier", Value: "\"premium\""},
			{Path: "new_field", Value: "\"added\""},
		},
		Remove: []string{"internal_flag"},
	}

	originalBody := []byte(`{"model": "gpt-4", "service_tier": "default", "internal_flag": true}`)
	mutator := NewBodyMutator(bodyMutations, originalBody)

	requestBody := []byte(`{"model": "gpt-4", "service_tier": "default", "internal_flag": true}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	require.Equal(t, "premium", result["service_tier"])
	require.Equal(t, "added", result["new_field"])
	require.NotContains(t, result, "internal_flag")
	require.Equal(t, "gpt-4", result["model"])
}

func TestBodyMutator_Mutate_ComplexValues(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{Path: "object_field", Value: `{"nested": "value", "number": 42}`},
			{Path: "array_field", Value: `[1, 2, 3]`},
			{Path: "null_field", Value: "null"},
			{Path: "boolean_field", Value: "true"},
		},
	}

	originalBody := []byte(`{"model": "gpt-4"}`)
	mutator := NewBodyMutator(bodyMutations, originalBody)

	requestBody := []byte(`{"model": "gpt-4"}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	require.Equal(t, "gpt-4", result["model"])

	// Check object field
	objectField, ok := result["object_field"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "value", objectField["nested"])
	require.Equal(t, float64(42), objectField["number"])

	// Check array field
	arrayField, ok := result["array_field"].([]interface{})
	require.True(t, ok)
	require.Equal(t, []interface{}{float64(1), float64(2), float64(3)}, arrayField)

	// Check null field
	require.Nil(t, result["null_field"])

	// Check boolean field
	require.Equal(t, true, result["boolean_field"])
}

func TestBodyMutator_Mutate_NoMutations(t *testing.T) {
	mutator := NewBodyMutator(nil, nil)

	requestBody := []byte(`{"model": "gpt-4", "service_tier": "default"}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	require.Equal(t, requestBody, mutatedBody)
}

func TestBodyMutator_Mutate_InvalidJSON(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{Path: "service_tier", Value: "premium"},
		},
	}

	originalBody := []byte(`{"model": "gpt-4"}`)
	mutator := NewBodyMutator(bodyMutations, originalBody)

	invalidRequestBody := []byte(`{invalid json}`)

	// sjson is more graceful and can handle malformed JSON
	mutatedBody, err := mutator.Mutate(invalidRequestBody)
	require.NoError(t, err)
	require.NotNil(t, mutatedBody)

	// The result should have the mutation applied
	require.Contains(t, string(mutatedBody), "service_tier")
	require.Contains(t, string(mutatedBody), "premium")
}

func TestBodyMutator_Mutate_InvalidJSONValue(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{Path: "service_tier", Value: "not valid json but will be treated as string"},
			{Path: "valid_field", Value: "\"valid\""},
		},
	}

	originalBody := []byte(`{"model": "gpt-4"}`)
	mutator := NewBodyMutator(bodyMutations, originalBody)

	requestBody := []byte(`{"model": "gpt-4"}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	// Invalid JSON values should be treated as strings
	require.Equal(t, "not valid json but will be treated as string", result["service_tier"])
	require.Equal(t, "valid", result["valid_field"])
}

func TestMutateResponse(t *testing.T) {
	t.Run("removes configured fields", func(t *testing.T) {
		bodyMutations := &filterapi.HTTPBodyMutation{
			Remove: []string{"provider", "system_fingerprint"},
		}
		mutator := NewBodyMutator(bodyMutations, nil)

		body := []byte(`{"id":"chatcmpl-123","provider":"openai","system_fingerprint":"fp_abc","choices":[]}`)
		mutated, err := mutator.MutateResponse(body)
		require.NoError(t, err)

		var result map[string]interface{}
		err = json.Unmarshal(mutated, &result)
		require.NoError(t, err)

		require.NotContains(t, result, "provider")
		require.NotContains(t, result, "system_fingerprint")
		require.Equal(t, "chatcmpl-123", result["id"])
		require.Contains(t, result, "choices")
	})

	t.Run("no mutations configured", func(t *testing.T) {
		mutator := NewBodyMutator(nil, nil)
		body := []byte(`{"id":"chatcmpl-123","provider":"openai"}`)
		mutated, err := mutator.MutateResponse(body)
		require.NoError(t, err)
		require.Equal(t, body, mutated)
	})

	t.Run("empty remove list", func(t *testing.T) {
		bodyMutations := &filterapi.HTTPBodyMutation{Remove: []string{}}
		mutator := NewBodyMutator(bodyMutations, nil)
		body := []byte(`{"id":"chatcmpl-123","provider":"openai"}`)
		mutated, err := mutator.MutateResponse(body)
		require.NoError(t, err)
		require.Equal(t, body, mutated)
	})
}

func TestMutateResponseSSE(t *testing.T) {
	t.Run("removes fields from data lines", func(t *testing.T) {
		bodyMutations := &filterapi.HTTPBodyMutation{
			Remove: []string{"provider"},
		}
		mutator := NewBodyMutator(bodyMutations, nil)

		chunk := []byte("data: {\"id\":\"chatcmpl-123\",\"provider\":\"openai\",\"choices\":[]}\n\ndata: {\"id\":\"chatcmpl-124\",\"provider\":\"openai\",\"choices\":[]}")
		mutated, err := mutator.MutateResponseSSE(chunk)
		require.NoError(t, err)

		lines := make([][]byte, 0)
		for _, line := range []string{
			"data: {\"id\":\"chatcmpl-123\",\"choices\":[]}",
			"",
			"data: {\"id\":\"chatcmpl-124\",\"choices\":[]}",
		} {
			lines = append(lines, []byte(line))
		}
		// Verify provider is removed from both data lines.
		require.NotContains(t, string(mutated), "provider")
		require.Contains(t, string(mutated), "chatcmpl-123")
		require.Contains(t, string(mutated), "chatcmpl-124")
	})

	t.Run("passes through DONE line unchanged", func(t *testing.T) {
		bodyMutations := &filterapi.HTTPBodyMutation{
			Remove: []string{"provider"},
		}
		mutator := NewBodyMutator(bodyMutations, nil)

		chunk := []byte("data: [DONE]")
		mutated, err := mutator.MutateResponseSSE(chunk)
		require.NoError(t, err)
		require.Equal(t, chunk, mutated)
	})

	t.Run("passes through non-data lines unchanged", func(t *testing.T) {
		bodyMutations := &filterapi.HTTPBodyMutation{
			Remove: []string{"provider"},
		}
		mutator := NewBodyMutator(bodyMutations, nil)

		chunk := []byte("event: message\ndata: {\"id\":\"chatcmpl-123\",\"provider\":\"openai\"}")
		mutated, err := mutator.MutateResponseSSE(chunk)
		require.NoError(t, err)

		require.Contains(t, string(mutated), "event: message")
		require.NotContains(t, string(mutated), "provider")
		require.Contains(t, string(mutated), "chatcmpl-123")
	})

	t.Run("no mutations configured", func(t *testing.T) {
		mutator := NewBodyMutator(nil, nil)
		chunk := []byte("data: {\"id\":\"chatcmpl-123\",\"provider\":\"openai\"}")
		mutated, err := mutator.MutateResponseSSE(chunk)
		require.NoError(t, err)
		require.Equal(t, chunk, mutated)
	})
}
