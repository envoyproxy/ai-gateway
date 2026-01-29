// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package redaction provides utilities for redacting sensitive information
// from requests and responses for safe debug logging.
package redaction

import (
	"crypto/sha256"
	"fmt"
)

// ComputeContentHash computes a cryptographic hash for content uniqueness tracking.
// This hash is used for debugging purposes and unique content detection, particularly for:
// - Tracking cache hits/misses by correlating identical content across requests
// - Identifying duplicate or similar requests without exposing actual content
// - Debugging issues by matching redacted logs to specific content patterns
// - Unique detection of content for deduplication and analysis
//
// We use SHA256 for:
// - Strong collision resistance for reliable unique detection
// - Cryptographic properties suitable for content identification
// - Standard hash function widely used for content addressing
//
// Returns a 16-character hex string (first 64 bits of SHA256 hash) for compact representation
// while maintaining strong collision resistance.
func ComputeContentHash(s string) string {
	hash := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", hash)[:16]
}

// RedactString replaces sensitive string content with a placeholder containing length and hash.
// The hash allows correlating logs with specific content for debugging without exposing
// the actual sensitive data.
//
// Format: [REDACTED LENGTH=n HASH=xxxxxxxx]
//
// Example: "secret API key 12345" becomes "[REDACTED LENGTH=19 HASH=a3f5e8c2]"
func RedactString(s string) string {
	if s == "" {
		return ""
	}
	hash := ComputeContentHash(s)
	return fmt.Sprintf("[REDACTED LENGTH=%d HASH=%s]", len(s), hash)
}
