// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package redaction provides utilities for redacting sensitive information
// from requests and responses for safe debug logging.
package redaction

import (
	"fmt"
	"hash/crc32"
)

// ComputeContentHash computes a fast, non-cryptographic hash for content uniqueness tracking.
// This hash is used for debugging purposes, particularly for:
// - Tracking cache hits/misses by correlating identical content across requests
// - Identifying duplicate or similar requests without exposing actual content
// - Debugging issues by matching redacted logs to specific content patterns
//
// We use CRC32 instead of cryptographic hashes (SHA256) because:
// - Much faster computation (important when redacting large messages with many parts)
// - Sufficient collision resistance for debugging and uniqueness tracking
// - Not used for security purposes, only for correlation and debugging
//
// Returns an 8-character hex string representation of the CRC32 hash.
func ComputeContentHash(s string) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(s)))
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
