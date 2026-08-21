// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsJSONMediaType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{name: "plain", contentType: "application/json", want: true},
		{name: "parameterized", contentType: "application/json; charset=UTF-8", want: true},
		{name: "case variation", contentType: "Application/JSON; Charset=utf-8", want: true},
		{name: "malformed", contentType: `application/json; charset="unterminated`, want: false},
		{name: "missing", contentType: "", want: false},
		{name: "non JSON", contentType: "text/event-stream", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isJSONMediaType(tt.contentType))
		})
	}
}
