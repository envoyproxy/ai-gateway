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
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

func TestAuthorizeRequest(t *testing.T) {
	makeTokenWithClaims := func(extraClaims jwt.MapClaims, scopes ...string) string {
		claims := jwt.MapClaims{}
		for k, v := range extraClaims {
			claims[k] = v
		}
		if len(scopes) > 0 {
			claims["scope"] = scopes
		}
		token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
		signed, _ := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		return signed
	}

	makeToken := func(scopes ...string) string {
		return makeTokenWithClaims(jwt.MapClaims{}, scopes...)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := &mcpRequestContext{ProxyConfig: &ProxyConfig{l: logger}}

	tests := []struct {
		name          string
		auth          *filterapi.MCPRouteAuthorization
		backend       string
		tool          string
		args          mcp.Params
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
						CEL:    ptr.To(`request.host.startsWith("api.") && request.mcp.backend == "backend1" && request.mcp.params.arguments.mode == "fast" && request.headers["x-tenant-id"] == "t-123"`),
					},
				},
			},
			backend:       "backend1",
			tool:          "tool1",
			args:          &mcp.CallToolParams{Arguments: map[string]any{"mode": "fast"}},
			host:          "api.example.com",
			headers:       http.Header{"X-Tenant-Id": []string{"t-123"}},
			expectAllowed: true,
		},
		{
			name: "rule CEL non match falls back to default deny",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						CEL:    ptr.To(`request.host.startsWith("api.") && request.mcp.backend == "backend1" && request.mcp.params.arguments.mode == "fast" && request.headers["x-tenant-id"] == "t-123"`),
					},
				},
			},
			backend:       "backend1",
			tool:          "tool1",
			args:          &mcp.CallToolParams{Name: "p1", Arguments: map[string]any{"mode": "fast"}},
			host:          "api.example.com",
			headers:       http.Header{"X-Tenant-Id": []string{"t-234"}},
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
			args: &mcp.CallToolParams{
				Name: "p1",
				Arguments: map[string]any{
					"mode": "other",
				},
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
						CEL: ptr.To(`request.method == "POST" && request.mcp.backend == "backend1" && request.mcp.tool == "tool1" && request.headers["x-tenant-id"] == "t-123" && request.mcp.params.arguments["flag"] == true`),
					},
				},
			},
			headers: http.Header{"Authorization": []string{"Bearer " + makeToken("read", "write")}, "X-Tenant-Id": []string{"t-123"}},
			backend: "backend1",
			tool:    "tool1",
			args: &mcp.CallToolParams{
				Name: "p1",
				Arguments: map[string]any{
					"flag": true,
				},
			},
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
						CEL: ptr.To(`int(request.mcp.params.arguments["count"]) >= 40 && int(request.mcp.params.arguments["count"]) < 50`),
					},
				},
			},
			headers: http.Header{"Authorization": []string{"Bearer " + makeToken("read")}},
			backend: "backend1",
			tool:    "tool1",
			args: &mcp.CallToolParams{
				Name: "p1",
				Arguments: map[string]any{
					"count": 42,
				},
			},
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
						CEL: ptr.To(`request.mcp.params.arguments["payload"] != null && request.mcp.params.arguments["payload"]["kind"] == "test" && request.mcp.params.arguments["payload"]["value"] == 123`),
					},
				},
			},
			headers: http.Header{"Authorization": []string{"Bearer " + makeToken("read")}},
			backend: "backend1",
			tool:    "tool1",
			args: &mcp.CallToolParams{
				Name: "p1",
				Arguments: map[string]any{
					"payload": map[string]any{
						"kind":  "test",
						"value": 123,
					},
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
						CEL: ptr.To(`request.mcp.params.arguments["mode"] == "fast"`),
					},
				},
			},
			headers:       http.Header{"Authorization": []string{"Bearer " + makeToken("read")}},
			backend:       "backend1",
			tool:          "tool1",
			args:          &mcp.CallToolParams{Name: "p1", Arguments: map[string]any{}},
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
						CEL: ptr.To(`request.mcp.params.arguments["folder"] == "restricted"`),
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
			args: &mcp.CallToolParams{
				Name: "p1",
				Arguments: map[string]any{
					"folder": "restricted",
				},
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
						CEL: ptr.To(`request.mcp.params.arguments["folder"] == "restricted"`),
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
			args: &mcp.CallToolParams{
				Name: "p1",
				Arguments: map[string]any{
					"folder": "allowed",
				},
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
		{
			name: "claims mismatch denies request",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{
								Scopes: []string{"read"},
								Claims: []filterapi.JWTClaim{{
									Name:      "tenant",
									ValueType: filterapi.JWTClaimValueTypeString,
									Values:    []string{"acme", "globex"},
								}},
							},
						},
					},
				},
			},
			headers:       http.Header{"Authorization": []string{"Bearer " + makeTokenWithClaims(jwt.MapClaims{"tenant": "other"}, "read")}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "claims match allows request - first value",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{
								Scopes: []string{"read"},
								Claims: []filterapi.JWTClaim{{
									Name:      "tenant",
									ValueType: filterapi.JWTClaimValueTypeString,
									Values:    []string{"acme", "globex"},
								}},
							},
						},
					},
				},
			},
			headers:       http.Header{"Authorization": []string{"Bearer " + makeTokenWithClaims(jwt.MapClaims{"tenant": "acme"}, "read")}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "claims match allows request - second value",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{
								Scopes: []string{"read"},
								Claims: []filterapi.JWTClaim{{
									Name:      "tenant",
									ValueType: filterapi.JWTClaimValueTypeString,
									Values:    []string{"acme", "globex"},
								}},
							},
						},
					},
				},
			},
			headers:       http.Header{"Authorization": []string{"Bearer " + makeTokenWithClaims(jwt.MapClaims{"tenant": "globex"}, "read")}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "opaque token denies with required scope challenge",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{
								Scopes: []string{"read"},
							},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}},
						},
					},
				},
			},
			// Opaque tokens are not JWTs; parsing fails so no scopes are extracted.
			headers:       http.Header{"Authorization": []string{"Bearer opaque-token"}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
			expectScopes:  []string{"read"},
		},
		{
			name: "scope mismatch denies request even if claims match",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{
								Scopes: []string{"admin"},
								Claims: []filterapi.JWTClaim{{
									Name:      "tenant",
									ValueType: filterapi.JWTClaimValueTypeString,
									Values:    []string{"acme", "globex"},
								}},
							},
						},
					},
				},
			},
			headers:       http.Header{"Authorization": []string{"Bearer " + makeTokenWithClaims(jwt.MapClaims{"tenant": "acme"}, "read")}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
			expectScopes:  []string{"admin"},
		},
		{
			name: "scope and claims match allows request",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{
								Scopes: []string{"admin"},
								Claims: []filterapi.JWTClaim{
									{
										Name:      "tenant",
										ValueType: filterapi.JWTClaimValueTypeString,
										Values:    []string{"acme", "globex"},
									},
								},
							},
						},
					},
				},
			},
			headers:       http.Header{"Authorization": []string{"Bearer " + makeTokenWithClaims(jwt.MapClaims{"tenant": "acme"}, "admin")}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
		},
		{
			name: "matching nested jwt claim string array value",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{
								Claims: []filterapi.JWTClaim{{
									Name:      "org.departments",
									ValueType: filterapi.JWTClaimValueTypeStringArray,
									Values:    []string{"security", "hr"},
								}},
							},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}},
						},
					},
				},
			},
			headers: http.Header{"Authorization": []string{"Bearer " + makeTokenWithClaims(jwt.MapClaims{
				"org": map[string]any{"departments": []any{"engineering", "security"}},
			})}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "matching nested jwt claim string array value via CEL",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						CEL:    ptr.To(`request.auth.jwt.claims["org"]["departments"].exists(d, d == "security")`),
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}},
						},
					},
				},
			},
			headers: http.Header{"Authorization": []string{"Bearer " + makeTokenWithClaims(jwt.MapClaims{
				"org": map[string]any{"departments": []any{"engineering", "security"}},
			})}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "non-matching nested jwt claim string array value",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{
								Claims: []filterapi.JWTClaim{{
									Name:      "org.departments",
									ValueType: filterapi.JWTClaimValueTypeStringArray,
									Values:    []string{"operations", "hr"},
								}},
							},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}},
						},
					},
				},
			},
			headers: http.Header{"Authorization": []string{"Bearer " + makeTokenWithClaims(jwt.MapClaims{
				"org": map[string]any{"departments": []any{"engineering", "security"}},
			})}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: false,
			expectScopes:  nil,
		},
		{
			name: "matching nested jwt claim string array single value",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{
								Claims: []filterapi.JWTClaim{{
									Name:      "org.departments",
									ValueType: filterapi.JWTClaimValueTypeStringArray,
									Values:    []string{"operations", "hr", "engineering"},
								}},
							},
						},
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "tool1"}},
						},
					},
				},
			},
			headers: http.Header{"Authorization": []string{"Bearer " + makeTokenWithClaims(jwt.MapClaims{
				"org": map[string]any{"departments": "engineering"},
			})}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
			expectScopes:  nil,
		},
		{
			name: "complex matching nested jwt claims",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Source: &filterapi.MCPAuthorizationSource{
							JWT: filterapi.JWTSource{
								Scopes: []string{"admin"},
								Claims: []filterapi.JWTClaim{
									{
										Name:      "tenant",
										ValueType: filterapi.JWTClaimValueTypeString,
										Values:    []string{"acme", "globex"},
									},
									{
										Name:      "org.departments",
										ValueType: filterapi.JWTClaimValueTypeStringArray,
										Values:    []string{"operations", "engineering"},
									},
								},
							},
						},
					},
				},
			},
			headers:       http.Header{"Authorization": []string{"Bearer " + makeTokenWithClaims(jwt.MapClaims{"tenant": "acme", "org": map[string]any{"departments": []any{"engineering", "hr"}}}, "admin")}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
		},
		{
			name: "scp claim used as scopes",
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
			headers:       http.Header{"Authorization": []string{"Bearer " + makeTokenWithClaims(jwt.MapClaims{"scp": "read"})}},
			backend:       "backend1",
			tool:          "tool1",
			expectAllowed: true,
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
			allowed, requiredScopes := proxy.authorizeRequest(compiled, &authorizationRequest{
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

// TestAuthorizeBackendOnly covers the initialize-phase, backend-level pre-check
// (authorizeBackendOnly), used by newSession to decide whether to even attempt
// connecting to a backend before any specific tool is known.
//
// The key invariant under test: a Deny rule with a tool-specific target and no
// CEL is ambiguous at this phase (backendMatches ignores the Tool field, since
// no tool is known yet), so it must never cause this pre-check to reject a
// backend outright — doing so would incorrectly block every OTHER tool on that
// backend too, not just the one the rule actually names. It's fine for the
// pre-check to be overly permissive (attempt a session that authorizeRequest
// later denies per-tool); it must never be overly restrictive.
func TestAuthorizeBackendOnly(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := &mcpRequestContext{ProxyConfig: &ProxyConfig{l: logger}}

	tests := []struct {
		name          string
		auth          *filterapi.MCPRouteAuthorization
		backend       string
		headers       http.Header
		expectAllowed bool
	}{
		{
			name:          "nil authorization always allows",
			auth:          nil,
			backend:       "backend1",
			expectAllowed: true,
		},
		{
			name:          "no rules returns DefaultAction",
			auth:          &filterapi.MCPRouteAuthorization{DefaultAction: "Deny"},
			backend:       "backend1",
			expectAllowed: false,
		},
		{
			// The bug: this rule only ever meant to deny "denyme", but
			// backendMatches can't see the tool, so before the fix this
			// returned Deny outright for the whole backend.
			name: "tool-scoped deny with no CEL does not block backend when DefaultAction is Allow",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Allow",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Deny",
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "denyme"}},
						},
					},
				},
			},
			backend:       "backend1",
			expectAllowed: true,
		},
		{
			// Skipping the ambiguous rule still correctly falls back to
			// DefaultAction when nothing else grants access — the fix must
			// not force an attempt when nothing actually allows one.
			name: "tool-scoped deny with no CEL falls back to DefaultAction Deny when nothing else allows",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Deny",
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "denyme"}},
						},
					},
				},
			},
			backend:       "backend1",
			expectAllowed: false,
		},
		{
			// A later Allow rule for a DIFFERENT tool on the same backend
			// must still be reachable once the ambiguous Deny is skipped —
			// this is the collateral-damage case: before the fix, the Deny
			// rule matching first would have blocked "othertool" too, even
			// though nothing was ever meant to touch it.
			name: "tool-scoped deny with no CEL lets a later allow rule for a different tool through",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Deny",
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "denyme"}},
						},
					},
					{
						Action: "Allow",
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "othertool"}},
						},
					},
				},
			},
			backend:       "backend1",
			expectAllowed: true,
		},
		{
			// Regression guard: a target-less Deny rule is unambiguous (it
			// really does apply to the whole backend/all tools) and must
			// keep blocking the backend at this phase exactly as before.
			name: "deny rule with no target still blocks the whole backend",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Allow",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{Action: "Deny"},
				},
			},
			backend:       "backend1",
			expectAllowed: false,
		},
		{
			// Regression guard: a Deny rule with CEL that doesn't reference
			// the tool is fully decidable at this phase (same result
			// regardless of which tool ends up being called) and must keep
			// being enforced here — this is the useful part of the
			// optimization (reject early, no wasted connection) and the fix
			// must not weaken it.
			name: "deny rule with tool-independent CEL still blocks backend when CEL matches",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Allow",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Deny",
						CEL:    ptr.To(`request.headers["x-scope"] != "admin"`),
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "denyme"}},
						},
					},
				},
			},
			backend:       "backend1",
			headers:       http.Header{"X-Scope": []string{"viewer"}},
			expectAllowed: false,
		},
		{
			// Regression guard: a Deny rule whose CEL explicitly references
			// request.mcp.tool is designed to evaluate false when Tool is
			// blanked out during this phase — it must keep NOT matching
			// here (existing mechanism, unaffected by the fix).
			name: "deny rule with tool-referencing CEL does not match during backend-only pre-check",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Allow",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Deny",
						CEL:    ptr.To(`request.mcp.tool == "denyme"`),
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "denyme"}},
						},
					},
				},
			},
			backend:       "backend1",
			expectAllowed: true,
		},
		{
			// Regression guard: an Allow rule with a tool-specific target
			// and no CEL is unaffected by the fix (it already returned
			// action=Allow immediately; over-attempting on Allow was always
			// considered acceptable, unlike over-rejecting on Deny).
			name: "tool-scoped allow with no CEL still returns true",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Deny",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Allow",
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "backend1", Tool: "sometool"}},
						},
					},
				},
			},
			backend:       "backend1",
			expectAllowed: true,
		},
		{
			// A rule targeting a DIFFERENT backend must not affect this one.
			name: "rule targeting a different backend does not apply",
			auth: &filterapi.MCPRouteAuthorization{
				DefaultAction: "Allow",
				Rules: []filterapi.MCPRouteAuthorizationRule{
					{
						Action: "Deny",
						Target: &filterapi.MCPAuthorizationTarget{
							Tools: []filterapi.ToolCall{{Backend: "other-backend", Tool: "denyme"}},
						},
					},
				},
			},
			backend:       "backend1",
			expectAllowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := tt.headers
			if headers == nil {
				headers = http.Header{}
			}
			compiled, err := compileAuthorization(tt.auth)
			if err != nil {
				t.Fatalf("unexpected compile error: %v", err)
			}
			allowed := proxy.authorizeBackendOnly(compiled, tt.backend, headers)
			if allowed != tt.expectAllowed {
				t.Fatalf("expected %v, got %v", tt.expectAllowed, allowed)
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
