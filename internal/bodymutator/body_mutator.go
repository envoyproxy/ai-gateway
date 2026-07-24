// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package bodymutator

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

var (
	// sseDataPrefix and sseDoneMessage mirror the SSE constants kept by the
	// translator and mcpproxy packages, which follow the same convention of
	// a package-local copy.
	sseDataPrefix  = []byte("data: ")
	sseDoneMessage = []byte("[DONE]")
)

type BodyMutator struct {
	// originalBody is the original request body for retry scenarios
	originalBody []byte

	// bodyMutations is the body mutations to apply
	bodyMutations *filterapi.HTTPBodyMutation

	// sseBuffer holds an incomplete trailing SSE line carried over between
	// MutateResponseSSE calls, since streamed chunks may split SSE events
	// at arbitrary byte boundaries.
	sseBuffer []byte
}

func NewBodyMutator(bodyMutations *filterapi.HTTPBodyMutation, originalBody []byte) *BodyMutator {
	return &BodyMutator{
		originalBody:  originalBody,
		bodyMutations: bodyMutations,
	}
}

// HasMutations reports whether this BodyMutator was constructed with a
// non-nil HTTPBodyMutation config. When false, Mutate is a no-op that
// returns its input unchanged, so callers can short-circuit the
// upstream filter's body-replacement path entirely.
func (b *BodyMutator) HasMutations() bool {
	return b != nil && b.bodyMutations != nil
}

// isJSONValue checks if a string represents a JSON value (not a plain string)
func isJSONValue(value string) bool {
	value = strings.TrimSpace(value)

	// Check for quoted strings (JSON strings)
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return true
	}

	// Check for numbers (integers or floats)
	if value == "0" || value == "true" || value == "false" || value == "null" {
		return true
	}

	// Check for positive/negative numbers
	if len(value) > 0 {
		first := value[0]
		if (first >= '0' && first <= '9') || first == '-' || first == '+' {
			// Simple number check - contains only digits, dots, +, -, e, E
			isNumber := true
			for _, r := range value {
				if (r < '0' || r > '9') && r != '.' && r != '-' && r != '+' && r != 'e' && r != 'E' {
					isNumber = false
					break
				}
			}
			if isNumber {
				return true
			}
		}
	}

	// Check for objects or arrays
	if strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}") {
		return true
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		return true
	}

	// Default to plain string
	return false
}

// Mutate mutates the request body based on the body mutations.
func (b *BodyMutator) Mutate(requestBody []byte) ([]byte, error) {
	if b.bodyMutations == nil {
		return requestBody, nil
	}

	mutatedBody := requestBody
	var err error

	// Apply removals first
	if len(b.bodyMutations.Remove) > 0 {
		for _, fieldName := range b.bodyMutations.Remove {
			if fieldName != "" {
				// TODO optimize by using SetBytesOption to avoid underlying sjson copy.
				mutatedBody, err = sjson.DeleteBytes(mutatedBody, fieldName)
				if err != nil {
					return nil, fmt.Errorf("failed to remove field %s: %w", fieldName, err)
				}
			}
		}
	}

	// Apply sets
	if len(b.bodyMutations.Set) > 0 {
		for _, field := range b.bodyMutations.Set {
			if field.Path != "" {
				// Check value type to determine appropriate sjson method
				// TODO handle JSON value check in configuration load time too.
				if isJSONValue(field.Value) {
					// Use SetRawBytes for JSON values (quoted strings, numbers, booleans, objects, arrays)
					mutatedBody, err = sjson.SetRawBytesOptions(mutatedBody, field.Path, []byte(field.Value), &sjson.Options{ReplaceInPlace: true})
				} else {
					// Use SetBytes for plain string values
					mutatedBody, err = sjson.SetBytesOptions(mutatedBody, field.Path, field.Value, &sjson.Options{ReplaceInPlace: true})
				}
				if err != nil {
					return nil, fmt.Errorf("failed to set field %s: %w", field.Path, err)
				}
			}
		}
	}

	return mutatedBody, nil
}

// MutateResponse removes configured fields from a non-streaming response body.
func (b *BodyMutator) MutateResponse(body []byte) ([]byte, error) {
	if b.bodyMutations == nil || len(b.bodyMutations.Remove) == 0 || len(body) == 0 {
		return body, nil
	}
	mutated := body
	var err error
	for _, fieldName := range b.bodyMutations.Remove {
		if fieldName != "" {
			mutated, err = sjson.DeleteBytes(mutated, fieldName)
			if err != nil {
				return nil, fmt.Errorf("failed to remove field %s: %w", fieldName, err)
			}
		}
	}
	return mutated, nil
}

// MutateResponseSSE removes configured fields from SSE chunk data lines.
//
// It is stateful: Envoy ext_proc delivers arbitrary byte chunks, so a single
// SSE "data: {...}" line may be split across two calls. Incomplete trailing
// lines are buffered and carried over to the next call; only complete lines
// (terminated by '\n') are emitted. When endOfStream is true, any remaining
// buffered data is flushed and mutated.
//
// Each emitted line is processed: if it starts with "data: " and contains
// JSON, the configured fields are removed. Lines that are not data lines
// (e.g., "[DONE]", empty lines) are passed through unchanged.
//
// The returned slice is never nil when mutations are configured: if all input
// bytes were buffered, a non-nil empty slice is returned so the caller treats
// it as "replace chunk with empty body" rather than "no body mutation".
// When no mutations are configured, the input chunk is returned unmodified
// (and may be nil).
func (b *BodyMutator) MutateResponseSSE(chunk []byte, endOfStream bool) []byte {
	if b.bodyMutations == nil || len(b.bodyMutations.Remove) == 0 {
		return chunk
	}
	b.sseBuffer = append(b.sseBuffer, chunk...)
	// Preallocate to the buffered size: removal only shrinks lines, so the
	// output never exceeds what is currently buffered.
	out := make([]byte, 0, len(b.sseBuffer))
	// Consume complete lines from a local view, leaving b.sseBuffer's start
	// fixed so the backing array can be reused below.
	working := b.sseBuffer
	for {
		i := bytes.IndexByte(working, '\n')
		if i < 0 {
			break
		}
		out = append(out, b.mutateSSELine(working[:i])...)
		out = append(out, '\n')
		working = working[i+1:]
	}
	if endOfStream {
		if len(working) > 0 {
			out = append(out, b.mutateSSELine(working)...)
		}
		b.sseBuffer = nil
	} else {
		// Slide the unconsumed tail back to the front of the backing array so
		// it stays reusable across calls instead of marching the slice start
		// forward and forcing reallocation churn on long streams. copy handles
		// the overlapping forward case correctly; an empty working yields
		// b.sseBuffer[:0], reusing the array.
		b.sseBuffer = b.sseBuffer[:copy(b.sseBuffer, working)]
	}
	return out
}

// mutateSSELine removes configured fields from a single SSE line. Non-"data: "
// lines and "data: [DONE]" are returned unchanged. On any sjson error the
// original line is returned unchanged, preserving the behavior of leaving
// unparseable lines alone.
func (b *BodyMutator) mutateSSELine(line []byte) []byte {
	if !bytes.HasPrefix(line, sseDataPrefix) {
		return line
	}
	data := line[len(sseDataPrefix):]
	// Detect and strip a CRLF carriage return so the payload is compared and
	// parsed cleanly; re-append it to the mutated output to preserve framing.
	hasCR := bytes.HasSuffix(data, []byte("\r"))
	if hasCR {
		data = data[:len(data)-1]
	}
	if bytes.Equal(data, sseDoneMessage) {
		return line
	}
	mutated := data
	for _, fieldName := range b.bodyMutations.Remove {
		if fieldName != "" {
			var err error
			mutated, err = sjson.DeleteBytes(mutated, fieldName)
			if err != nil {
				// If we can't parse this line as JSON, leave it unchanged.
				return line
			}
		}
	}
	out := make([]byte, 0, len(sseDataPrefix)+len(mutated)+1)
	out = append(out, sseDataPrefix...)
	out = append(out, mutated...)
	if hasCR {
		out = append(out, '\r')
	}
	return out
}
