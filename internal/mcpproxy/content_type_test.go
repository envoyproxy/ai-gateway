// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHasMediaType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
		want        string
		match       bool
	}{
		{
			name:        "exact match",
			contentType: "application/json",
			want:        "application/json",
			match:       true,
		},
		{
			name:        "match with parameters",
			contentType: "application/json; charset=utf-8",
			want:        "application/json",
			match:       true,
		},
		{
			name:        "fallback on malformed parameters",
			contentType: "application/json; charset==utf-8",
			want:        "application/json",
			match:       true,
		},
		{
			name:        "fallback trims whitespace",
			contentType: " text/event-stream ; charset==utf-8",
			want:        "text/event-stream",
			match:       true,
		},
		{
			name:        "different media type",
			contentType: "text/plain",
			want:        "application/json",
			match:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			header := http.Header{"Content-Type": []string{tt.contentType}}
			require.Equal(t, tt.match, hasMediaType(header, tt.want))
		})
	}
}
