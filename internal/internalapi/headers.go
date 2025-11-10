// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internalapi

// Header represents a single HTTP header as a key-value pair.
type Header [2]string

// Key returns the header key.
func (h Header) Key() string {
	return h[0]
}

// Value returns the header value.
func (h Header) Value() string {
	return h[1]
}
