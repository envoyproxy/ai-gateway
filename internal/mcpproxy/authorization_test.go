// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"io"
	"log/slog"
	"net/http"
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
		},
		{
			name:          "no rules falls back to default deny",
			auth:          &filterapi.MCPRouteAuthorization{},
			header:        "",
			backendName:   "backend1",
			toolName:      "tool1",
			expectAllowed: false,
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			if tt.header != "" {
				headers.Set("Authorization", tt.header)
			}
			allowed := proxy.authorizeRequest(tt.auth, headers, tt.backendName, tt.toolName, tt.args)
			if allowed != tt.expectAllowed {
				t.Fatalf("expected %v, got %v", tt.expectAllowed, allowed)
			}
		})
	}
}
