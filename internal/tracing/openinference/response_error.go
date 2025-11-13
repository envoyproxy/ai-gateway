// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.
//
// Package openinference provides shared OpenInference helpers.
package openinference

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// RecordResponseError emits an "exception" event and sets error status on the span
// based on the provided HTTP status code and optional body message.
func RecordResponseError(span trace.Span, statusCode int, body string) {
	var errorType string
	switch statusCode {
	case 400:
		errorType = "BadRequestError"
	case 401:
		errorType = "AuthenticationError"
	case 403:
		errorType = "PermissionDeniedError"
	case 404:
		errorType = "NotFoundError"
	case 429:
		errorType = "RateLimitError"
	case 500, 502, 503:
		errorType = "InternalServerError"
	default:
		errorType = "Error"
	}

	errorMsg := fmt.Sprintf("Error code: %d", statusCode)
	if len(body) > 0 {
		errorMsg = fmt.Sprintf("Error code: %d - %s", statusCode, body)
	}

	span.AddEvent("exception", trace.WithAttributes(
		attribute.String("exception.type", errorType),
		attribute.String("exception.message", errorMsg),
	))
	span.SetStatus(codes.Error, errorMsg)
}
