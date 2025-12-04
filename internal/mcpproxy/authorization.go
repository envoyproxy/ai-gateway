// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

type compiledAuthorization struct {
	ResourceMetadataURL string
	Rules               []compiledAuthorizationRule
}

type compiledAuthorizationRule struct {
	Source filterapi.MCPAuthorizationSource
	Target []compiledToolCall
}

type compiledToolCall struct {
	Backend    string
	ToolName   string
	Expression string
	program    cel.Program
}

// compileAuthorization compiles the MCPRouteAuthorization into a compiledAuthorization for efficient CEL evaluation.
func compileAuthorization(auth *filterapi.MCPRouteAuthorization) (*compiledAuthorization, error) {
	if auth == nil {
		return nil, nil
	}

	env, err := cel.NewEnv(
		cel.Variable("args", cel.DynType),
		cel.OptionalTypes(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	compiled := &compiledAuthorization{
		ResourceMetadataURL: auth.ResourceMetadataURL,
	}

	for _, rule := range auth.Rules {
		cr := compiledAuthorizationRule{
			Source: rule.Source,
		}
		for _, tool := range rule.Target.Tools {
			ct := compiledToolCall{
				Backend:  tool.Backend,
				ToolName: tool.ToolName,
			}
			if tool.Arguments != nil && strings.TrimSpace(*tool.Arguments) != "" {
				expr := strings.TrimSpace(*tool.Arguments)
				ast, issues := env.Compile(expr)
				if issues != nil && issues.Err() != nil {
					return nil, fmt.Errorf("failed to compile arguments CEL for tool %s/%s: %w", tool.Backend, tool.ToolName, issues.Err())
				}
				program, err := env.Program(ast, cel.CostLimit(10000), cel.EvalOptions(cel.OptOptimize))
				if err != nil {
					return nil, fmt.Errorf("failed to build arguments CEL program for tool %s/%s: %w", tool.Backend, tool.ToolName, err)
				}
				ct.Expression = expr
				ct.program = program
			}
			cr.Target = append(cr.Target, ct)
		}
		compiled.Rules = append(compiled.Rules, cr)
	}

	return compiled, nil
}

// authorizeRequest authorizes the request based on the given MCPRouteAuthorization configuration.

func (m *MCPProxy) authorizeRequest(authorization *compiledAuthorization, headers http.Header, backendName, toolName string, arguments any) (bool, []string) {
	if authorization == nil {
		return true, nil
	}

	// If no rules are defined, deny all requests.
	if len(authorization.Rules) == 0 {
		return false, nil
	}

	// If the rules are defined, a valid bearer token is required.
	token, err := bearerToken(headers.Get("Authorization"))
	// This is just a sanity check. The actual JWT verification is performed by Envoy before reaching here, and the token
	// should always be present and valid.
	if err != nil {
		m.l.Info("missing or invalid bearer token", slog.String("error", err.Error()))
		return false, nil
	}

	claims := jwt.MapClaims{}
	// JWT verification is performed by Envoy before reaching here. So we only need to parse the token without verification.
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		m.l.Info("failed to parse JWT token", slog.String("error", err.Error()))
		return false, nil
	}

	scopeSet := sets.New(extractScopes(claims)...)
	var requiredScopesForChallenge []string

	for _, rule := range authorization.Rules {
		if !m.toolMatches(backendName, toolName, rule.Target, arguments) {
			continue
		}

		requiredScopes := rule.Source.JWT.Scopes
		if scopesSatisfied(scopeSet, requiredScopes) {
			return true, nil
		}

		// Keep track of the smallest set of required scopes for challenge.
		if len(requiredScopesForChallenge) == 0 || len(requiredScopes) < len(requiredScopesForChallenge) {
			requiredScopesForChallenge = requiredScopes
		}
	}

	return false, requiredScopesForChallenge
}

func bearerToken(header string) (string, error) {
	if header == "" {
		return "", errors.New("missing Authorization header")
	}

	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", errors.New("invalid Authorization header")
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", errors.New("missing bearer token")
	}
	return token, nil
}

func extractScopes(claims jwt.MapClaims) []string {
	raw, ok := claims["scope"]
	if !ok {
		return nil
	}

	switch v := raw.(type) {
	case string:
		return strings.Fields(v)
	case []string:
		return v
	case []interface{}:
		scopes := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				scopes = append(scopes, s)
			}
		}
		return scopes
	default:
		return nil
	}
}

func (m *MCPProxy) toolMatches(backendName, toolName string, tools []compiledToolCall, args any) bool {
	if len(tools) == 0 {
		return true
	}

	for _, t := range tools {
		if t.Backend != backendName || t.ToolName != toolName {
			continue
		}
		if t.program == nil {
			return true
		}

		result, _, err := t.program.Eval(map[string]any{"args": args})
		if err != nil {
			m.l.Error("failed to evaluate arguments CEL", slog.String("backend", t.Backend), slog.String("tool", t.ToolName), slog.String("error", err.Error()))
			continue
		}

		switch v := result.Value().(type) {
		case bool:
			if v {
				return true
			}
		case types.Bool:
			if bool(v) {
				return true
			}
		default:
			m.l.Error("arguments CEL did not return a boolean", slog.String("backend", t.Backend), slog.String("tool", t.ToolName), slog.String("expression", t.Expression))
		}
	}
	// If no matching tool entry or no arguments matched, fail.
	return false
}

func scopesSatisfied(have sets.Set[string], required []string) bool {
	if len(required) == 0 {
		return true
	}
	// All required scopes must be present for authorization to succeed.
	for _, scope := range required {
		if _, ok := have[scope]; !ok {
			return false
		}
	}
	return true
}

// buildInsufficientScopeHeader builds the WWW-Authenticate header value for insufficient scope errors.
// Reference: https://mcp.mintlify.app/specification/2025-11-25/basic/authorization#runtime-insufficient-scope-errors
func buildInsufficientScopeHeader(scopes []string, resourceMetadata string) string {
	parts := []string{`Bearer error="insufficient_scope"`}
	parts = append(parts, fmt.Sprintf(`scope="%s"`, strings.Join(scopes, " ")))
	if resourceMetadata != "" {
		parts = append(parts, fmt.Sprintf(`resource_metadata="%s"`, resourceMetadata))
	}
	parts = append(parts, `error_description="The token is missing required scopes"`)

	return strings.Join(parts, ", ")
}
