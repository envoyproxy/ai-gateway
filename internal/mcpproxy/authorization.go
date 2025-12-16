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
	DefaultAction       filterapi.AuthorizationAction
}

type compiledAuthorizationRule struct {
	Source *filterapi.MCPAuthorizationSource
	Target []compiledToolCall
	Action filterapi.AuthorizationAction
}

type compiledToolCall struct {
	Backend    string
	Tool       string
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
		DefaultAction:       auth.DefaultAction,
	}

	for _, rule := range auth.Rules {
		cr := compiledAuthorizationRule{
			Source: rule.Source,
			Action: rule.Action,
		}
		if rule.Target != nil {
			for _, tool := range rule.Target.Tools {
				ct := compiledToolCall{
					Backend: tool.Backend,
					Tool:    tool.Tool,
				}
				if tool.When != nil && strings.TrimSpace(*tool.When) != "" {
					expr := strings.TrimSpace(*tool.When)
					ast, issues := env.Compile(expr)
					if issues != nil && issues.Err() != nil {
						return nil, fmt.Errorf("failed to compile when CEL for tool %s/%s: %w", tool.Backend, tool.Tool, issues.Err())
					}
					program, err := env.Program(ast, cel.CostLimit(10000), cel.EvalOptions(cel.OptOptimize))
					if err != nil {
						return nil, fmt.Errorf("failed to build when CEL program for tool %s/%s: %w", tool.Backend, tool.Tool, err)
					}
					ct.Expression = expr
					ct.program = program
				}
				cr.Target = append(cr.Target, ct)
			}
		}
		compiled.Rules = append(compiled.Rules, cr)
	}

	return compiled, nil
}

// authorizeRequest authorizes the request based on the given MCPRouteAuthorization configuration.
func (m *MCPProxy) authorizeRequest(authorization *compiledAuthorization, headers http.Header, backend, tool string, arguments any) (bool, []string) {
	if authorization == nil {
		return true, nil
	}

	defaultAction := authorization.DefaultAction == filterapi.AuthorizationActionAllow

	// If no rules are defined, return the default action.
	if len(authorization.Rules) == 0 {
		return defaultAction, nil
	}

	scopeSet := sets.New[string]()
	token, err := bearerToken(headers.Get("Authorization"))
	// This is just a sanity check. The actual JWT verification is performed by Envoy before reaching here, and the token
	// should always be present and valid.
	if err != nil {
		m.l.Info("missing or invalid bearer token", slog.String("error", err.Error()))
	} else {
		claims := jwt.MapClaims{}
		// JWT verification is performed by Envoy before reaching here. So we only need to parse the token without verification.
		if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
			m.l.Info("failed to parse JWT token", slog.String("error", err.Error()))
		}
		scopeSet = sets.New(extractScopes(claims)...)
	}

	var requiredScopesForChallenge []string

	for _, rule := range authorization.Rules {
		action := rule.Action == filterapi.AuthorizationActionAllow

		if rule.Target != nil && !m.toolMatches(backend, tool, rule.Target, arguments) {
			continue
		}

		// If no source is specified, the rule matches all sources.
		if rule.Source == nil {
			return action, nil
		}

		// Scopes check doesn't make much sense if action is deny, we check it anyway.
		requiredScopes := rule.Source.JWT.Scopes
		if scopesSatisfied(scopeSet, requiredScopes) {
			return action, nil
		}

		// Keep track of the smallest set of required scopes for challenge when the action is allow and the request is denied.
		if action {
			if len(requiredScopesForChallenge) == 0 || len(requiredScopes) < len(requiredScopesForChallenge) {
				requiredScopesForChallenge = requiredScopes
			}
		}
	}

	return defaultAction, requiredScopesForChallenge
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

func (m *MCPProxy) toolMatches(backend, tool string, tools []compiledToolCall, args any) bool {
	// Empty tools means all tools match.
	if len(tools) == 0 {
		return true
	}

	for _, t := range tools {
		if t.Backend != backend || t.Tool != tool {
			continue
		}
		if t.program == nil {
			return true
		}

		result, _, err := t.program.Eval(map[string]any{"args": args})
		if err != nil {
			m.l.Error("failed to evaluate when CEL", slog.String("backend", t.Backend), slog.String("tool", t.Tool), slog.String("error", err.Error()))
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
			m.l.Error("when CEL did not return a boolean", slog.String("backend", t.Backend), slog.String("tool", t.Tool), slog.String("expression", t.Expression))
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
