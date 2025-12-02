// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

func TestAuthorizeRequest(t *testing.T) {
	makeToken := func(scopes ...string) string {
		claims := jwt.MapClaims{}
		if len(scopes) > 0 {
			claims["scope"] = scopes
		}
		token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
		signed, _ := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		return signed
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := &MCPProxy{l: logger}

	tests := []struct {
		name          string
		auth          *filterapi.MCPRouteAuthorization
		header        string
		backendName   string
		toolName      string
		args          map[string]any
		expectAllowed bool
		expectScopes  []string
	}{
		{
			name: "matching tool and scope",
			auth: &filterapi.MCPRouteAuthorization{
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: filterapi.MCPAuthorizationSource{
							JWTSource: filterapi.JWTSource{Scopes: []string{"read", "write"}},
						},
						Target: filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{BackendName: "backend1", ToolName: "tool1"}},
						},
					},
				},
			},
			header:        "Bearer " + makeToken("read", "write"),
			backendName:   "backend1",
			toolName:      "tool1",
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "matching tool scope and arguments regex",
			auth: &filterapi.MCPRouteAuthorization{
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: filterapi.MCPAuthorizationSource{
							JWTSource: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								BackendName: "backend1",
								ToolName:    "tool1",
								Arguments: map[string]string{
									"mode":  "fast|slow",
									"user":  "u-[0-9]+",
									"debug": "true",
								},
							}},
						},
					},
				},
			},
			header:      "Bearer " + makeToken("read"),
			backendName: "backend1",
			toolName:    "tool1",
			args: map[string]any{
				"mode":  "fast",
				"user":  "u-123",
				"debug": "true",
			},
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "numeric argument matches via JSON string",
			auth: &filterapi.MCPRouteAuthorization{
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: filterapi.MCPAuthorizationSource{
							JWTSource: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								BackendName: "backend1",
								ToolName:    "tool1",
								Arguments: map[string]string{
									"count": "^4[0-9]$",
								},
							}},
						},
					},
				},
			},
			header:        "Bearer " + makeToken("read"),
			backendName:   "backend1",
			toolName:      "tool1",
			args:          map[string]any{"count": 42},
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "object argument can be matched via JSON string",
			auth: &filterapi.MCPRouteAuthorization{
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: filterapi.MCPAuthorizationSource{
							JWTSource: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								BackendName: "backend1",
								ToolName:    "tool1",
								Arguments: map[string]string{
									"payload": `"kind":"test"`,
								},
							}},
						},
					},
				},
			},
			header:      "Bearer " + makeToken("read"),
			backendName: "backend1",
			toolName:    "tool1",
			args: map[string]any{
				"payload": struct {
					Kind  string `json:"kind"`
					Value int    `json:"value"`
				}{
					Kind:  "test",
					Value: 123,
				},
			},
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "matching tool but insufficient scopes not allowed",
			auth: &filterapi.MCPRouteAuthorization{
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: filterapi.MCPAuthorizationSource{
							JWTSource: filterapi.JWTSource{Scopes: []string{"read", "write"}},
						},
						Target: filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{BackendName: "backend1", ToolName: "tool1"}},
						},
					},
				},
			},
			header:        "Bearer " + makeToken("read"),
			backendName:   "backend1",
			toolName:      "tool1",
			expectAllowed: false,
			expectScopes:  []string{"read", "write"},
		},
		{
			name: "argument regex mismatch denied",
			auth: &filterapi.MCPRouteAuthorization{
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: filterapi.MCPAuthorizationSource{
							JWTSource: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								BackendName: "backend1",
								ToolName:    "tool1",
								Arguments: map[string]string{
									"mode": "fast|slow",
								},
							}},
						},
					},
				},
			},
			header:      "Bearer " + makeToken("read"),
			backendName: "backend1",
			toolName:    "tool1",
			args: map[string]any{
				"mode": "other",
			},
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "missing argument denies when required",
			auth: &filterapi.MCPRouteAuthorization{
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: filterapi.MCPAuthorizationSource{
							JWTSource: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								BackendName: "backend1",
								ToolName:    "tool1",
								Arguments: map[string]string{
									"mode": "fast",
								},
							}},
						},
					},
				},
			},
			header:        "Bearer " + makeToken("read"),
			backendName:   "backend1",
			toolName:      "tool1",
			args:          map[string]any{},
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "no matching rule falls back to default deny - tool mismatch",
			auth: &filterapi.MCPRouteAuthorization{
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: filterapi.MCPAuthorizationSource{
							JWTSource: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{BackendName: "backend1", ToolName: "tool1"}},
						},
					},
				},
			},
			header:        "Bearer " + makeToken("read", "write"),
			backendName:   "backend1",
			toolName:      "other-tool",
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "no matching rule falls back to default deny - scope mismatch",
			auth: &filterapi.MCPRouteAuthorization{
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: filterapi.MCPAuthorizationSource{
							JWTSource: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{BackendName: "backend1", ToolName: "tool1"}},
						},
					},
				},
			},
			header:        "Bearer " + makeToken("foo", "bar"),
			backendName:   "backend1",
			toolName:      "other-tool",
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name:          "no rules falls back to default deny",
			auth:          &filterapi.MCPRouteAuthorization{},
			header:        "",
			backendName:   "backend1",
			toolName:      "tool1",
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "no bearer token not allowed when rules exist",
			auth: &filterapi.MCPRouteAuthorization{
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: filterapi.MCPAuthorizationSource{
							JWTSource: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{BackendName: "backend1", ToolName: "tool1"}},
						},
					},
				},
			},
			header:        "",
			backendName:   "backend1",
			toolName:      "tool1",
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "invalid bearer token not allowed when rules exist",
			auth: &filterapi.MCPRouteAuthorization{
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: filterapi.MCPAuthorizationSource{
							JWTSource: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{BackendName: "backend1", ToolName: "tool1"}},
						},
					},
				},
			},
			header:        "Bearer invalid.token.here",
			backendName:   "backend1",
			toolName:      "tool1",
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "selects smallest required scope set when multiple rules match",
			auth: &filterapi.MCPRouteAuthorization{
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: filterapi.MCPAuthorizationSource{JWTSource: filterapi.JWTSource{Scopes: []string{"alpha", "beta", "gamma"}}},
						Target: filterapi.MCPAuthorizationTarget{Tools: []filterapi.ToolCall{{BackendName: "backend1", ToolName: "tool1"}}},
					},
					{
						Source: filterapi.MCPAuthorizationSource{JWTSource: filterapi.JWTSource{Scopes: []string{"alpha", "beta"}}},
						Target: filterapi.MCPAuthorizationTarget{Tools: []filterapi.ToolCall{{BackendName: "backend1", ToolName: "tool1"}}},
					},
				},
			},
			header:        "Bearer " + makeToken("alpha"),
			backendName:   "backend1",
			toolName:      "tool1",
			expectAllowed: false,
			expectScopes:  []string{"alpha", "beta"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			if tt.header != "" {
				headers.Set("Authorization", tt.header)
			}
			allowed, requiredScopes := proxy.authorizeRequest(tt.auth, headers, tt.backendName, tt.toolName, tt.args)
			if allowed != tt.expectAllowed {
				t.Fatalf("expected %v, got %v", tt.expectAllowed, allowed)
			}
			if !reflect.DeepEqual(requiredScopes, tt.expectScopes) {
				t.Fatalf("expected required scopes %v, got %v", tt.expectScopes, requiredScopes)
			}
		})
	}
}

func TestBuildInsufficientScopeHeader(t *testing.T) {
	const resourceMetadata = "https://api.example.com/.well-known/oauth-protected-resource/mcp"

	t.Run("with scopes and resource metadata", func(t *testing.T) {
		header := buildInsufficientScopeHeader([]string{"read", "write"}, resourceMetadata)
		expected := `Bearer error="insufficient_scope", scope="read write", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp", error_description="The token is missing required scopes"`
		if header != expected {
			t.Fatalf("expected %q, got %q", expected, header)
		}
	})
}
