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

func strPtr(s string) *string { return &s }

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
		backend       string
		tool          string
		args          map[string]any
		expectError   bool
		expectAllowed bool
		expectScopes  []string
	}{
		{
			name: "matching tool and scope",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read", "write"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}},
						},
					},
				},
			},
			header:        "Bearer " + makeToken("read", "write"),
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "matching tool scope and arguments CEL",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "tool1",
								When:    strPtr(`args.mode in ["fast", "slow"] && args.user.matches("u-[0-9]+") && args.debug == true`),
							}},
						},
					},
				},
			},
			header:  "Bearer " + makeToken("read"),
			backend: "backend1",
			tool:    "tool1",
			args: map[string]any{
				"mode":  "fast",
				"user":  "u-123",
				"debug": true,
			},
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "numeric argument matches via CEL",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "tool1",
								When:    strPtr(`int(args.count) >= 40 && int(args.count) < 50`),
							}},
						},
					},
				},
			},
			header:        "Bearer " + makeToken("read"),
			backend:       "backend1",
			tool:          "tool1",
			args:          map[string]any{"count": 42},
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "object argument can be matched via CEL safe navigation",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "tool1",
								When:    strPtr(`args["payload"] != null && args["payload"]["kind"] == "test" && args["payload"]["value"] == 123`),
							}},
						},
					},
				},
			},
			header:  "Bearer " + makeToken("read"),
			backend: "backend1",
			tool:    "tool1",
			args: map[string]any{
				"payload": map[string]any{
					"kind":  "test",
					"value": 123,
				},
			},
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "matching tool but insufficient scopes not allowed",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read", "write"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}},
						},
					},
				},
			},
			header:        "Bearer " + makeToken("read"),
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
			expectScopes:  []string{"read", "write"},
		},
		{
			name: "arguments CEL mismatch denied",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "tool1",
								When:    strPtr(`args.mode in ["fast", "slow"]`),
							}},
						},
					},
				},
			},
			header:  "Bearer " + makeToken("read"),
			backend: "backend1",
			tool:    "tool1",
			args: map[string]any{
				"mode": "other",
			},
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "arguments CEL failed evaluation denies",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "tool1",
								When:    strPtr(`args.nonExistingField in ["fast", "slow"]`),
							}},
						},
					},
				},
			},
			header:  "Bearer " + makeToken("read"),
			backend: "backend1",
			tool:    "tool1",
			args: map[string]any{
				"mode": "other",
			},
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "arguments CEL returns non-boolean denies",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "tool1",
								When:    strPtr(`args.mode`),
							}},
						},
					},
				},
			},
			header:  "Bearer " + makeToken("read"),
			backend: "backend1",
			tool:    "tool1",
			args: map[string]any{
				"mode": "other",
			},
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "arguments invalid CEL denies",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "tool1",
								When:    strPtr(`invalid syntax here`),
							}},
						},
					},
				},
			},
			header:  "Bearer " + makeToken("read"),
			backend: "backend1",
			tool:    "tool1",
			args: map[string]any{
				"mode": "other",
			},
			expectError:   true,
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "missing argument denies when required",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "tool1",
								When:    strPtr(`args["mode"] == "fast"`),
							}},
						},
					},
				},
			},
			header:        "Bearer " + makeToken("read"),
			backend:       "backend1",
			tool:          "tool1",
			args:          map[string]any{},
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "no matching rule falls back to default deny - tool mismatch",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}},
						},
					},
				},
			},
			header:        "Bearer " + makeToken("read", "write"),
			backend:       "backend1",
			tool:          "other-tool",
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "no matching rule falls back to default deny - scope mismatch",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}},
						},
					},
				},
			},
			header:        "Bearer " + makeToken("foo", "bar"),
			backend:       "backend1",
			tool:          "other-tool",
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "no bearer token not allowed when rules exist",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}},
						},
					},
				},
			},
			header:        "",
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
			expectScopes:  []string{"read"},
		},
		{
			name: "invalid bearer token not allowed when rules exist",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}},
						},
					},
				},
			},
			header:        "Bearer invalid.token.here",
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
			expectScopes:  []string{"read"},
		},
		{
			name: "selects smallest required scope set when multiple rules match",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{JWT: filterapi.JWTSource{Scopes: []string{"alpha", "beta", "gamma"}}},
						Target: &filterapi.MCPAuthorizationTarget{Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}}},
					},
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{JWT: filterapi.JWTSource{Scopes: []string{"alpha", "beta"}}},
						Target: &filterapi.MCPAuthorizationTarget{Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}}},
					},
				},
			},
			header:        "Bearer " + makeToken("alpha"),
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
			expectScopes:  []string{"alpha", "beta"},
		},
		{
			name: "allow requests with required scopes except those matching CEL deny rule - deny request",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Deny",
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "listFiles",
								When:    strPtr(`args.folder == "restricted"`),
							}},
						},
					},
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "listFiles",
							}},
						},
					},
				},
			},
			header:  "Bearer " + makeToken("read"),
			backend: "backend1",
			tool:    "listFiles",
			args: map[string]any{
				"folder": "restricted",
			},
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "allow requests with required scopes except those matching CEL deny rule - allow request",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Deny",
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "listFiles",
								When:    strPtr(`args.folder == "restricted"`),
							}},
						},
					},
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "listFiles",
							}},
						},
					},
				},
			},
			header:  "Bearer " + makeToken("read"),
			backend: "backend1",
			tool:    "listFiles",
			args: map[string]any{
				"folder": "allowed",
			},
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "no rules default deny",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
			},
			header:        "",
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "no rules default allow",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Allow",
			},
			header:        "",
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "empty rule default deny",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
					},
				},
			},
			header:        "",
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "empty rule default allow",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Allow",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Deny",
					},
				},
			},
			header:        "",
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "rule with no source allows all requests for matching tool",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "tool1",
							}},
						},
						Action: "Allow",
					},
				},
			},
			header:        "",
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "rule with no target allows all requests with matching source",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read"}},
						},
						Action: "Allow",
					},
				},
			},
			header:        "Bearer " + makeToken("read"),
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
			expectScopes:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			if tt.header != "" {
				headers.Set("Authorization", tt.header)
			}
			compiled, err := compileAuthorization(tt.auth)
			if (err != nil) != tt.expectError {
				t.Fatalf("expected error: %v, got: %v", tt.expectError, err)
			}
			if err != nil {
				return
			}
			allowed, requiredScopes := proxy.authorizeRequest(compiled, headers, tt.backend, tt.tool, tt.args)
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

func TestCompileAuthorizationInvalidExpression(t *testing.T) {
	_, err := compileAuthorization(&filterapi.MCPRouteAuthorization{
		Rules: []filterapi.MCPRouteAuthorizationRule{
			{
				Source: &filterapi.MCPAuthorizationSource{
					JWT: filterapi.JWTSource{Scopes: []string{"read"}},
				},
				Target: &filterapi.MCPAuthorizationTarget{
					Tools: []filterapi.ToolCall{{
						Backend: "backend1",
						Tool:    "tool1",
						When:    strPtr("args."),
					}},
				},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected compile error for invalid CEL expression")
	}
}
