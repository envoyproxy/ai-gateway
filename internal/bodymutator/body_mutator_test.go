// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package bodymutator

import (
	"bytes"
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

func TestBodyMutator_MutateResponse_RemovesConfiguredFields(t *testing.T) {
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
}

func TestBodyMutator_MutateResponse_NoMutations(t *testing.T) {
	mutator := NewBodyMutator(nil, nil)
	body := []byte(`{"id":"chatcmpl-123","provider":"openai"}`)
	mutated, err := mutator.MutateResponse(body)
	require.NoError(t, err)
	require.Equal(t, body, mutated)
}

func TestBodyMutator_MutateResponse_EmptyRemoveList(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{Remove: []string{}}
	mutator := NewBodyMutator(bodyMutations, nil)
	body := []byte(`{"id":"chatcmpl-123","provider":"openai"}`)
	mutated, err := mutator.MutateResponse(body)
	require.NoError(t, err)
	require.Equal(t, body, mutated)
}

func TestBodyMutator_MutateResponseSSE_RemovesFieldsFromDataLines(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Remove: []string{"provider"},
	}
	mutator := NewBodyMutator(bodyMutations, nil)

	chunk := []byte("data: {\"id\":\"chatcmpl-123\",\"provider\":\"openai\",\"choices\":[]}\n\ndata: {\"id\":\"chatcmpl-124\",\"provider\":\"openai\",\"choices\":[]}")
	mutated := mutator.MutateResponseSSE(chunk, true)

	expected := bytes.Join([][]byte{
		[]byte("data: {\"id\":\"chatcmpl-123\",\"choices\":[]}"),
		{},
		[]byte("data: {\"id\":\"chatcmpl-124\",\"choices\":[]}"),
	}, []byte("\n"))
	require.Equal(t, expected, mutated)
}

func TestBodyMutator_MutateResponseSSE_PassesThroughDONELine(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Remove: []string{"provider"},
	}
	mutator := NewBodyMutator(bodyMutations, nil)

	chunk := []byte("data: [DONE]")
	mutated := mutator.MutateResponseSSE(chunk, true)
	require.Equal(t, chunk, mutated)
}

func TestBodyMutator_MutateResponseSSE_PassesThroughNonDataLines(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Remove: []string{"provider"},
	}
	mutator := NewBodyMutator(bodyMutations, nil)

	chunk := []byte("event: message\ndata: {\"id\":\"chatcmpl-123\",\"provider\":\"openai\"}")
	mutated := mutator.MutateResponseSSE(chunk, true)

	require.Contains(t, string(mutated), "event: message")
	require.NotContains(t, string(mutated), "provider")
	require.Contains(t, string(mutated), "chatcmpl-123")
}

func TestBodyMutator_MutateResponseSSE_NoMutations(t *testing.T) {
	mutator := NewBodyMutator(nil, nil)
	chunk := []byte("data: {\"id\":\"chatcmpl-123\",\"provider\":\"openai\"}")
	mutated := mutator.MutateResponseSSE(chunk, true)
	require.Equal(t, chunk, mutated)
}

// TestBodyMutator_MutateResponseSSE_SplitAcrossCalls verifies that an SSE event
// split mid-JSON across two ProcessResponseBody calls is correctly reassembled,
// with the configured field stripped from the completed line.
func TestBodyMutator_MutateResponseSSE_SplitAcrossCalls(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Remove: []string{"provider"},
	}
	mutator := NewBodyMutator(bodyMutations, nil)

	full := []byte("data: {\"id\":\"chatcmpl-123\",\"provider\":\"openai\",\"choices\":[]}\n")
	// Split mid-JSON so the first chunk lacks a trailing newline and an
	// incomplete JSON payload.
	splitAt := 30
	first := mutator.MutateResponseSSE(full[:splitAt], false)
	// First call has no complete line: everything is buffered.
	require.NotNil(t, first)
	require.Empty(t, first)

	second := mutator.MutateResponseSSE(full[splitAt:], true)

	combined := append(append([]byte{}, first...), second...)
	require.Equal(t, []byte("data: {\"id\":\"chatcmpl-123\",\"choices\":[]}\n"), combined)
	require.NotContains(t, string(combined), "provider")
}

// TestBodyMutator_MutateResponseSSE_CompletePlusPartial verifies that a chunk
// containing one complete event plus a partial second event emits the complete
// event (mutated) and buffers the partial.
func TestBodyMutator_MutateResponseSSE_CompletePlusPartial(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Remove: []string{"provider"},
	}
	mutator := NewBodyMutator(bodyMutations, nil)

	chunk := []byte("data: {\"id\":\"chatcmpl-123\",\"provider\":\"openai\"}\ndata: {\"id\":\"chatcmpl-124\",\"prov")
	out := mutator.MutateResponseSSE(chunk, false)

	require.Equal(t, []byte("data: {\"id\":\"chatcmpl-123\"}\n"), out)
	require.NotContains(t, string(out), "chatcmpl-124")

	// Complete the second event.
	rest := mutator.MutateResponseSSE([]byte("ider\":\"openai\"}\n"), true)
	require.Equal(t, []byte("data: {\"id\":\"chatcmpl-124\"}\n"), rest)
	require.NotContains(t, string(rest), "provider")
}

// TestBodyMutator_MutateResponseSSE_SplitAtNewlineBoundary verifies that a chunk
// ending exactly at the line-terminating newline emits the complete line, leaving
// nothing buffered for the next call.
func TestBodyMutator_MutateResponseSSE_SplitAtNewlineBoundary(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Remove: []string{"provider"},
	}
	mutator := NewBodyMutator(bodyMutations, nil)

	first := mutator.MutateResponseSSE([]byte("data: {\"id\":\"chatcmpl-123\",\"provider\":\"openai\"}\n"), false)
	require.Equal(t, []byte("data: {\"id\":\"chatcmpl-123\"}\n"), first)

	second := mutator.MutateResponseSSE([]byte("data: [DONE]\n"), true)
	require.Equal(t, []byte("data: [DONE]\n"), second)
}

// TestBodyMutator_MutateResponseSSE_InvalidJSONLinePassthrough verifies that a data
// line whose payload is not valid JSON is passed through unchanged while other
// data lines in the same chunk are still mutated.
func TestBodyMutator_MutateResponseSSE_InvalidJSONLinePassthrough(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Remove: []string{"provider"},
	}
	mutator := NewBodyMutator(bodyMutations, nil)

	chunk := []byte("data: not-json\ndata: {\"id\":\"chatcmpl-123\",\"provider\":\"openai\"}\n")
	out := mutator.MutateResponseSSE(chunk, true)

	require.Equal(t, []byte("data: not-json\ndata: {\"id\":\"chatcmpl-123\"}\n"), out)
}

// TestBodyMutator_MutateResponseSSE_FlushOnEndOfStream verifies that a buffered
// partial line without a trailing newline is flushed and mutated at end of stream.
func TestBodyMutator_MutateResponseSSE_FlushOnEndOfStream(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Remove: []string{"provider"},
	}
	mutator := NewBodyMutator(bodyMutations, nil)

	// No trailing newline; the whole line is buffered.
	buffered := mutator.MutateResponseSSE([]byte("data: {\"id\":\"chatcmpl-123\",\"provider\":\"openai\"}"), false)
	require.NotNil(t, buffered)
	require.Empty(t, buffered)

	// End of stream flushes the buffer with mutation applied.
	flushed := mutator.MutateResponseSSE(nil, true)
	require.Equal(t, []byte("data: {\"id\":\"chatcmpl-123\"}"), flushed)
	require.NotContains(t, string(flushed), "provider")
}

// TestBodyMutator_MutateResponseSSE_AllBytesBuffered verifies that when all input
// bytes are buffered (no complete line, not end of stream), a non-nil empty slice
// is returned so the caller replaces the chunk with an empty body rather than
// treating it as "no mutation".
func TestBodyMutator_MutateResponseSSE_AllBytesBuffered(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Remove: []string{"provider"},
	}
	mutator := NewBodyMutator(bodyMutations, nil)

	out := mutator.MutateResponseSSE([]byte("data: {\"id\":\"chatcmpl"), false)
	require.NotNil(t, out)
	require.Empty(t, out)
}
