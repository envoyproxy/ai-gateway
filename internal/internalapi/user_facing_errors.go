// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internalapi

import (
	"errors"
)

// User-facing errors that are safe to return in HTTP responses.
// These errors contain no sensitive information and can be directly
// exposed to clients with appropriate HTTP status codes.
//
// Usage: Use fmt.Errorf("%w: %s", ErrMalformedRequest, "specific details")
// to wrap these errors with additional context.
var (
	// ErrMalformedRequest indicates the request cannot be parsed (malformed JSON, etc.)
	// Should return HTTP 400 Bad Request.
	ErrMalformedRequest = errors.New("malformed request")

	// ErrInvalidRequestBody indicates the request was parsed but contains invalid values,
	// unsupported features, or doesn't match the expected schema for the target API.
	// Should return HTTP 422 Unprocessable Entity.
	ErrInvalidRequestBody = errors.New("invalid request body")
)

// GetUserFacingError checks if an error is a known user-facing error that's safe to expose.
// Returns the full error (with details) if it's safe, or nil if it should not be exposed to users.
// This preserves the detailed message from wrapped errors like fmt.Errorf("%w: details", ErrInvalidRequestBody).
func GetUserFacingError(err error) error {
	if errors.Is(err, ErrMalformedRequest) {
		return err
	}
	if errors.Is(err, ErrInvalidRequestBody) {
		return err
	}
	return nil
}
