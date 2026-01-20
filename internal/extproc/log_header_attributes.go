// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import "sync"

var (
	logRequestHeaderAttributesMu sync.RWMutex
	logRequestHeaderAttributes   map[string]string
)

// SetLogRequestHeaderAttributes sets the mapping of request headers to access log attributes.
// This is configured at startup and read during request processing.
func SetLogRequestHeaderAttributes(attrs map[string]string) {
	logRequestHeaderAttributesMu.Lock()
	logRequestHeaderAttributes = attrs
	logRequestHeaderAttributesMu.Unlock()
}

func getLogRequestHeaderAttributes() map[string]string {
	logRequestHeaderAttributesMu.RLock()
	defer logRequestHeaderAttributesMu.RUnlock()
	if len(logRequestHeaderAttributes) == 0 {
		return nil
	}
	out := make(map[string]string, len(logRequestHeaderAttributes))
	for k, v := range logRequestHeaderAttributes {
		out[k] = v
	}
	return out
}
