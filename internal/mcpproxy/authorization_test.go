// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"cmp"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"k8s.io/utils/ptr"

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
		backend       string
		tool          string
		args          map[string]any
		host          string
		headers       http.Header
		mcpMethod     string
		expectError   bool
		expectAllowed bool
		expectScopes  []string
	}{
		{
			name: "rule CEL matches all conditions",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						CEL:    ptr.To(`request.host.startsWith("api.") && request.mcp.backend == "backend1" && request.mcp.params.mode == "fast" && request.headers["x-tenant"] == "t-123"`),
					},
				},
			},
			backend:       "backend1",
			tool:          "tool1",
			args:          map[string]any{"mode": "fast"},
			host:          "api.example.com",
			headers:       http.Header{"X-Tenant": []string{"t-123"}},
			expectAllowed: true,
		},
		{
			name: "rule CEL non match falls back to default deny",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						CEL:    ptr.To(`request.host.startsWith("api.") && request.mcp.backend == "backend1" && request.mcp.params.mode == "fast" && request.headers["x-tenant"] == "t-123"`),
					},
				},
			},
			backend:       "backend1",
			tool:          "tool1",
			args:          map[string]any{"mode": "fast"},
			host:          "api.example.com",
			headers:       http.Header{"X-Tenant": []string{"t-234"}},
			expectAllowed: false,
		},
		{
			name: "rule CEL returns non boolean treated as non match",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						CEL:    ptr.To(`10`),
					},
				},
			},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
		},
		{
			name: "invalid CEL denies",
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
							}},
						},
						CEL: ptr.To(`invalid syntax here`),
					},
				},
			},
			headers: http.Header{"Authorization": []string{"Bearer " + makeToken("read")}},
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
			name: "rule with source target and CEL all match",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"read", "write"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend1",
								Tool:    "tool1",
							}},
						},
						CEL: ptr.To(`request.method == "POST" && request.mcp.backend == "backend1" && request.mcp.tool == "tool1" && request.headers["x-tenant"] == "t-123" && request.mcp.params.flag == true`),
					},
				},
			},
			headers:       http.Header{"Authorization": []string{"Bearer " + makeToken("read", "write")}, "X-Tenant": []string{"t-123"}},
			backend:       "backend1",
			tool:          "tool1",
			args:          map[string]any{"flag": true},
			expectAllowed: true,
		},
		{
			name: "source target match but CEL does not",
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
							}},
						},
						CEL: ptr.To(`request.method == "GET"`),
					},
				},
			},
			headers:       http.Header{"Authorization": []string{"Bearer " + makeToken("read")}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
		},
		{
			name: "CEL match but source target do not",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{Scopes: []string{"write"}},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{
								Backend: "backend2",
								Tool:    "tool2",
							}},
						},
						CEL: ptr.To(`request.method == "POST"`),
					},
				},
			},
			headers:       http.Header{"Authorization": []string{"Bearer " + makeToken("write")}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
		},
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
			headers:       http.Header{"Authorization": []string{"Bearer " + makeToken("read", "write")}},
			backend:       "backend1",
			tool:          "tool1",
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
							}},
						},
						CEL: ptr.To(`int(request.mcp.params.count) >= 40 && int(request.mcp.params.count) < 50`),
					},
				},
			},
			headers:       http.Header{"Authorization": []string{"Bearer " + makeToken("read")}},
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
							}},
						},
						CEL: ptr.To(`request.mcp.params["payload"] != null && request.mcp.params["payload"]["kind"] == "test" && request.mcp.params["payload"]["value"] == 123`),
					},
				},
			},
			headers: http.Header{"Authorization": []string{"Bearer " + makeToken("read")}},
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
			headers:       http.Header{"Authorization": []string{"Bearer " + makeToken("read")}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
			expectScopes:  []string{"read", "write"},
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
							}},
						},
						CEL: ptr.To(`request.mcp.params["mode"] == "fast"`),
					},
				},
			},
			headers:       http.Header{"Authorization": []string{"Bearer " + makeToken("read")}},
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
			headers:       http.Header{"Authorization": []string{"Bearer " + makeToken("read", "write")}},
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
			headers:       http.Header{"Authorization": []string{"Bearer " + makeToken("foo", "bar")}},
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
			headers:       http.Header{"Authorization": []string{"Bearer invalid.token.here"}},
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
			headers:       http.Header{"Authorization": []string{"Bearer " + makeToken("alpha")}},
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
							}},
						},
						CEL: ptr.To(`request.mcp.params.folder == "restricted"`),
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
			headers: http.Header{"Authorization": []string{"Bearer " + makeToken("read")}},
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
							}},
						},
						CEL: ptr.To(`request.mcp.params.folder == "restricted"`),
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
			headers: http.Header{"Authorization": []string{"Bearer " + makeToken("read")}},
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
			headers:       http.Header{"Authorization": []string{"Bearer " + makeToken("read")}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
			expectScopes:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := tt.headers
			if headers == nil {
				headers = http.Header{}
			}
			compiled, err := compileAuthorization(tt.auth)
			if (err != nil) != tt.expectError {
				t.Fatalf("expected error: %v, got: %v", tt.expectError, err)
			}
			if err != nil {
				return
			}
			allowed, requiredScopes := proxy.authorizeRequest(compiled, authorizationRequest{
				Headers:    headers,
				HTTPMethod: cmp.Or(tt.mcpMethod, http.MethodPost),
				Host:       tt.host,
				HTTPPath:   "/mcp",
				MCPMethod:  cmp.Or(tt.mcpMethod, "tools/call"),
				Backend:    tt.backend,
				Tool:       tt.tool,
				Params:     tt.args,
			})
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

func TestCompileAuthorizationInvalidRuleCEL(t *testing.T) {
	_, err := compileAuthorization(&filterapi.MCPRouteAuthorization{
		Rules: []filterapi.MCPRouteAuthorizationRule{
			{
				CEL: ptr.To("request."),
			},
		},
	})
	if err == nil {
		t.Fatalf("expected compile error for invalid rule CEL expression")
	}
}
