// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package cache

import (
	"strconv"
	"strings"
	"time"
)

// Directives holds parsed Cache-Control header directives.
type Directives struct {
	// NoCache indicates the response should not be served from cache without revalidation.
	// For requests: bypass cache lookup but still cache the response.
	// For responses: treat as no-store for simplicity (no revalidation support).
	NoCache bool
	// NoStore indicates the response should not be stored in cache at all.
	// For requests: bypass cache entirely (no lookup, no store).
	// For responses: do not cache this response.
	NoStore bool
	// Private indicates the response is intended for a single user and should not
	// be stored in a shared cache.
	Private bool
	// MaxAge specifies the maximum time in seconds that the response is considered fresh.
	// nil if not specified.
	MaxAge *time.Duration
}

// ParseCacheControl parses a Cache-Control header value and returns the directives.
// The header value is case-insensitive and may contain multiple comma-separated directives.
//
// Supported directives:
//   - no-cache: bypass cache lookup (request) or require revalidation (response)
//   - no-store: do not cache at all
//   - private: do not store in shared cache
//   - max-age=N: use N seconds as TTL
//
// Example: "no-cache, max-age=3600" -> Directives{NoCache: true, MaxAge: 1h}
func ParseCacheControl(value string) Directives {
	var d Directives
	if value == "" {
		return d
	}

	// Split by comma and process each directive
	parts := strings.Split(value, ",")
	for _, part := range parts {
		// Trim whitespace and convert to lowercase for case-insensitive matching
		directive := strings.TrimSpace(strings.ToLower(part))

		switch {
		case directive == "no-cache":
			d.NoCache = true
		case directive == "no-store":
			d.NoStore = true
		case directive == "private":
			d.Private = true
		case strings.HasPrefix(directive, "max-age="):
			// Parse max-age value
			ageStr := strings.TrimPrefix(directive, "max-age=")
			if seconds, err := strconv.ParseInt(ageStr, 10, 64); err == nil && seconds >= 0 {
				duration := time.Duration(seconds) * time.Second
				d.MaxAge = &duration
			}
		}
		// Ignore unknown directives (public, must-revalidate, etc.)
	}

	return d
}

// ShouldSkipCacheLookup returns true if the request Cache-Control directives
// indicate that cache lookup should be skipped.
func (d Directives) ShouldSkipCacheLookup() bool {
	return d.NoStore || d.NoCache
}

// ShouldSkipCacheStore returns true if the directives indicate that
// the response should not be stored in cache.
func (d Directives) ShouldSkipCacheStore() bool {
	return d.NoStore || d.NoCache || d.Private
}
