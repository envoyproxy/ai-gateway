// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package jsonpatch

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func TestValidatePatches(t *testing.T) {
	tests := []struct {
		name       string
		patches    map[string][]openai.JSONPatch
		wantErrMsg string
	}{
		{
			name: "valid patches",
			patches: map[string][]openai.JSONPatch{
				"GCPVertexAI": {
					{Op: "add", Path: "/cachedContent", Value: "test-cache"},
					{Op: "replace", Path: "/temperature", Value: 0.5},
				},
			},
		},
		{
			name: "invalid operation",
			patches: map[string][]openai.JSONPatch{
				"GCPVertexAI": {
					{Op: "invalid", Path: "/test", Value: "value"},
				},
			},
			wantErrMsg: "unsupported operation: invalid",
		},
		{
			name: "too many patches",
			patches: func() map[string][]openai.JSONPatch {
				patches := make([]openai.JSONPatch, MaxPatchCount+1)
				for i := range patches {
					patches[i] = openai.JSONPatch{Op: "add", Path: "/test", Value: "value"}
				}
				return map[string][]openai.JSONPatch{"GCPVertexAI": patches}
			}(),
			wantErrMsg: "total patch count 101 exceeds maximum allowed 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePatches(tt.patches)
			if tt.wantErrMsg != "" {
				require.ErrorContains(t, err, tt.wantErrMsg)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidatePatch(t *testing.T) {
	tests := []struct {
		name       string
		patch      openai.JSONPatch
		wantErrMsg string
	}{
		{
			name:  "valid add operation",
			patch: openai.JSONPatch{Op: "add", Path: "/test", Value: "value"},
		},
		{
			name:  "valid replace operation",
			patch: openai.JSONPatch{Op: "replace", Path: "/existing", Value: 123},
		},
		{
			name:       "invalid operation",
			patch:      openai.JSONPatch{Op: "invalid", Path: "/test"},
			wantErrMsg: "unsupported operation: invalid",
		},
		{
			name:       "missing path",
			patch:      openai.JSONPatch{Op: "add", Value: "value"},
			wantErrMsg: "path is required",
		},
		{
			name:       "invalid JSON pointer",
			patch:      openai.JSONPatch{Op: "add", Path: "invalid", Value: "value"},
			wantErrMsg: "invalid path: JSON pointer must start with '/'",
		},
		{
			name:       "add without value",
			patch:      openai.JSONPatch{Op: "add", Path: "/test"},
			wantErrMsg: "value is required for add operation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePatch(tt.patch)
			if tt.wantErrMsg != "" {
				require.ErrorContains(t, err, tt.wantErrMsg)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestExtractPatches(t *testing.T) {
	tests := []struct {
		name       string
		extraBody  *openai.ExtraBody
		want       map[string][]openai.JSONPatch
		wantErrMsg string
	}{
		{
			name:      "nil extra body",
			extraBody: nil,
			want:      nil,
		},
		{
			name: "valid patches",
			extraBody: &openai.ExtraBody{
				AIGateway: &openai.AIGatewayExtensions{
					JSONPatches: map[string][]openai.JSONPatch{
						"GCPVertexAI": {
							{Op: "add", Path: "/cachedContent", Value: "test"},
						},
					},
				},
			},
			want: map[string][]openai.JSONPatch{
				"GCPVertexAI": {
					{Op: "add", Path: "/cachedContent", Value: "test"},
				},
			},
		},
		{
			name: "invalid patches",
			extraBody: &openai.ExtraBody{
				AIGateway: &openai.AIGatewayExtensions{
					JSONPatches: map[string][]openai.JSONPatch{
						"GCPVertexAI": {
							{Op: "invalid", Path: "/test", Value: "value"},
						},
					},
				},
			},
			want:       nil,
			wantErrMsg: "unsupported operation: invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractPatches(tt.extraBody)
			if tt.wantErrMsg != "" {
				require.ErrorContains(t, err, tt.wantErrMsg)
				return
			}
			require.NoError(t, err)

			if d := cmp.Diff(tt.want, got); d != "" {
				t.Errorf("ExtractPatches() mismatch (-want +got):\n%s", d)
			}
		})
	}
}

func TestNewProcessor(t *testing.T) {
	tests := []struct {
		name       string
		patches    map[string][]openai.JSONPatch
		wantErrMsg string
	}{
		{
			name: "valid patches",
			patches: map[string][]openai.JSONPatch{
				"GCPVertexAI": {
					{Op: "add", Path: "/cachedContent", Value: "test"},
				},
			},
		},
		{
			name:       "no patches",
			patches:    nil,
			wantErrMsg: "no patches provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewProcessor(tt.patches)
			if tt.wantErrMsg != "" {
				require.ErrorContains(t, err, tt.wantErrMsg)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, p)
		})
	}
}

func TestApplyPatches(t *testing.T) {
	tests := []struct {
		name       string
		body       []byte
		patches    map[string][]openai.JSONPatch
		schemaName string
		want       string
		wantErrMsg string
	}{
		{
			name: "add operation",
			body: []byte(`{"model": "test"}`),
			patches: map[string][]openai.JSONPatch{
				"GCPVertexAI": {
					{Op: "add", Path: "/cachedContent", Value: "cache-key"},
				},
			},
			schemaName: "GCPVertexAI",
			want:       `{"model":"test","cachedContent":"cache-key"}`,
		},
		{
			name: "add existing key",
			body: []byte(`{"model": "test"}`),
			patches: map[string][]openai.JSONPatch{
				"GCPVertexAI": {
					{Op: "add", Path: "/model", Value: "new-test"},
				},
			},
			schemaName: "GCPVertexAI",
			want:       `{"model":"new-test"}`,
		},
		{
			name: "nested add operation",
			body: []byte(`{"model": "test"}`),
			patches: map[string][]openai.JSONPatch{
				"GCPVertexAI": {
					{Op: "add", Path: "/generationConfig/routingConfig/manualMode/modelName", Value: "test-model"},
				},
			},
			schemaName: "GCPVertexAI",
			want:       `{"model":"test","generationConfig":{"routingConfig":{"manualMode":{"modelName":"test-model"}}}}`,
		},
		{
			name: "ANY patches apply to all schemas",
			body: []byte(`{"model": "test"}`),
			patches: map[string][]openai.JSONPatch{
				SchemaKeyAny: {
					{Op: "add", Path: "/universal", Value: "applies-to-all"},
				},
			},
			schemaName: "GCPVertexAI",
			want:       `{"model":"test","universal":"applies-to-all"}`,
		},
		{
			name: "both ANY and schema-specific patches",
			body: []byte(`{"model": "test"}`),
			patches: map[string][]openai.JSONPatch{
				SchemaKeyAny: {
					{Op: "add", Path: "/universal", Value: "for-all"},
				},
				"GCPVertexAI": {
					{Op: "add", Path: "/specific", Value: "for-gcp"},
				},
			},
			schemaName: "GCPVertexAI",
			want:       `{"model":"test","universal":"for-all","specific":"for-gcp"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			processor, err := NewProcessor(tc.patches)
			if err != nil {
				t.Fatalf("NewProcessor() error = %v", err)
				return
			}
			got, err := processor.ApplyPatches(tc.body, tc.schemaName)
			require.NoError(t, err)

			// Compare JSON content, not string representation.
			if diff := cmp.Diff(tc.want, string(got)); diff != "" {
				t.Errorf("ApplyPatches() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHasPatchesForSchema(t *testing.T) {
	tests := []struct {
		name       string
		patches    map[string][]openai.JSONPatch
		schemaName string
		want       bool
	}{
		{
			name:       "nil patches",
			patches:    nil,
			schemaName: "GCPVertexAI",
			want:       false,
		},
		{
			name:       "empty patches",
			patches:    map[string][]openai.JSONPatch{},
			schemaName: "GCPVertexAI",
			want:       false,
		},
		{
			name: "has specific schema patches",
			patches: map[string][]openai.JSONPatch{
				"GCPVertexAI": {
					{Op: "add", Path: "/cachedContent", Value: "test"},
				},
			},
			schemaName: "GCPVertexAI",
			want:       true,
		},
		{
			name: "has ANY schema patches",
			patches: map[string][]openai.JSONPatch{
				SchemaKeyAny: {
					{Op: "add", Path: "/universal", Value: "test"},
				},
			},
			schemaName: "GCPVertexAI",
			want:       true,
		},
		{
			name: "has both ANY and specific schema patches",
			patches: map[string][]openai.JSONPatch{
				SchemaKeyAny: {
					{Op: "add", Path: "/universal", Value: "test"},
				},
				"GCPVertexAI": {
					{Op: "add", Path: "/specific", Value: "test"},
				},
			},
			schemaName: "GCPVertexAI",
			want:       true,
		},
		{
			name: "has patches for different schema only",
			patches: map[string][]openai.JSONPatch{
				"OpenAI": {
					{Op: "add", Path: "/openai_specific", Value: "test"},
				},
			},
			schemaName: "GCPVertexAI",
			want:       false,
		},
		{
			name: "has patches for different schema and ANY",
			patches: map[string][]openai.JSONPatch{
				SchemaKeyAny: {
					{Op: "add", Path: "/universal", Value: "test"},
				},
				"OpenAI": {
					{Op: "add", Path: "/openai_specific", Value: "test"},
				},
			},
			schemaName: "GCPVertexAI",
			want:       true,
		},
		{
			name: "empty schema name with ANY patches",
			patches: map[string][]openai.JSONPatch{
				SchemaKeyAny: {
					{Op: "add", Path: "/universal", Value: "test"},
				},
			},
			schemaName: "",
			want:       true,
		},
		{
			name: "empty schema name without ANY patches",
			patches: map[string][]openai.JSONPatch{
				"GCPVertexAI": {
					{Op: "add", Path: "/specific", Value: "test"},
				},
			},
			schemaName: "",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := &Processor{patches: tt.patches}

			got := processor.HasPatchesForSchema(tt.schemaName)
			require.Equal(t, tt.want, got)
		})
	}
}
