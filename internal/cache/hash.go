// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package cache

import (
	"crypto/sha256"
	"encoding/hex"
)

// KeyPrefix is the prefix used for all cache keys to namespace them in Redis.
const KeyPrefix = "aigw:response:"

// HashBody computes a SHA-256 hash of the request body and returns it as a cache key.
// The key is prefixed with KeyPrefix to namespace it in Redis.
func HashBody(body []byte) string {
	hash := sha256.Sum256(body)
	return KeyPrefix + hex.EncodeToString(hash[:])
}
