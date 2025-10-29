// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package anthropic

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessageContent_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		want    MessageContent
		wantErr bool
	}{
		{
			name:    "string content",
			jsonStr: `"Hello, world!"`,
			want:    MessageContent{Text: "Hello, world!"},
			wantErr: false,
		},
		{
			name:    "array content",
			jsonStr: `[{}, {}]`,
			want:    MessageContent{Array: []MessageContentArrayElement{{}, {}}},
			wantErr: false,
		},
		{
			name:    "invalid content",
			jsonStr: `12345`,
			want:    MessageContent{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mc MessageContent
			err := mc.UnmarshalJSON([]byte(tt.jsonStr))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, mc)
		})
	}
}
