// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"mime"
	"net/http"
	"strings"
)

func hasMediaType(header http.Header, want string) bool {
	got, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err == nil {
		return got == want
	}

	raw := header.Get("Content-Type")
	if i := strings.IndexByte(raw, ';'); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw) == want
}
