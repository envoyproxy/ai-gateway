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
	"slices"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

type compiledAuthorization struct {
	ResourceMetadataURL string
	Rules               []compiledAuthorizationRule
	DefaultAction       filterapi.AuthorizationAction
}

type compiledAuthorizationRule struct {
	Source *filterapi.MCPAuthorizationSource
	Target []filterapi.ToolCall
	Action filterapi.AuthorizationAction
	// CEL expression compiled for request-level evaluation.
	celExpression string
	celProgram    cel.Program
}

// authorizationRequest captures the parts of an MCP request needed for authorization.
type authorizationRequest struct {
	Headers    http.Header
	HTTPMethod string
	Host       string
	HTTPPath   string
	MCPMethod  string
	Backend    string
	Tool       string
	Params     mcp.Params
}

// compileAuthorization compiles the MCPRouteAuthorization into a compiledAuthorization for efficient CEL evaluation.
func compileAuthorization(auth *filterapi.MCPRouteAuthorization) (*compiledAuthorization, error) {
	if auth == nil {
		return nil, nil
	}

	env, err := cel.NewEnv(
		cel.Variable("request", cel.DynType),
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
			cr.Target = append(cr.Target, rule.Target.Tools...)
		}
		if rule.CEL != nil && strings.TrimSpace(*rule.CEL) != "" {
			expr := strings.TrimSpace(*rule.CEL)
			ast, issues := env.Compile(expr)
			if issues != nil && issues.Err() != nil {
				return nil, fmt.Errorf("failed to compile rule CEL: %w", issues.Err())
			}
			program, err := env.Program(ast, cel.CostLimit(10000), cel.EvalOptions(cel.OptOptimize))
			if err != nil {
				return nil, fmt.Errorf("failed to build rule CEL program: %w", err)
			}
			cr.celExpression = expr
			cr.celProgram = program
		}
		compiled.Rules = append(compiled.Rules, cr)
	}

	return compiled, nil
}

// authorizeRequest authorizes the request based on the given MCPRouteAuthorization configuration.
func (m *MCPProxy) authorizeRequest(authorization *compiledAuthorization, req *authorizationRequest) (bool, []string) {
	if authorization == nil {
		return true, nil
	}

	defaultAction := authorization.DefaultAction == filterapi.AuthorizationActionAllow

	// If no rules are defined, return the default action.
	if len(authorization.Rules) == 0 {
		return defaultAction, nil
	}

	scopeSet := sets.New[string]()
	claims := jwt.MapClaims{}

	token, err := bearerToken(req.Headers.Get("Authorization"))
	// This is just a sanity check. The actual JWT verification is performed by Envoy before reaching here, and the token
	// should always be present and valid.
	if err != nil {
		m.l.Info("missing or invalid bearer token", slog.String("error", err.Error()))
	} else {
		// JWT verification is performed by Envoy before reaching here. So we only need to parse the token without verification.
		if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
			m.l.Info("failed to parse JWT token", slog.String("error", err.Error()))
		} else {
			scopeSet = sets.New(extractScopes(claims)...)
			// Scopes are handled separately, remove them from the claims map to avoid interference.
			// "scp" is also removed as it is a common alias for "scope" (e.g. Azure AD, Okta).
			delete(claims, "scope")
			delete(claims, "scp")
		}
	}

	var requiredScopesForChallenge []string
	var celActivation map[string]any

	for i := range authorization.Rules {
		rule := &authorization.Rules[i]
		action := rule.Action == filterapi.AuthorizationActionAllow

		// Evaluate CEL expression if present.
		if rule.celProgram != nil {
			if celActivation == nil {
				celActivation = buildCELActivation(req, claims, scopeSet)
			}
			match, err := m.evalRuleCEL(rule, celActivation)
			if err != nil {
				m.l.Error("failed to evaluate authorization CEL", slog.String("error", err.Error()), slog.String("expression", rule.celExpression))
				continue
			}
			if !match {
				continue
			}
		}

		// If no target is specified, the rule matches all targets.
		if rule.Target != nil && !m.toolMatches(req.Backend, req.Tool, rule.Target) {
			continue
		}

		// If no source is specified, the rule matches all sources.
		if rule.Source == nil {
			return action, nil
		}

		// Check source if specified.
		if !claimsSatisfied(claims, rule.Source.JWT.Claims) {
			continue
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

func buildCELActivation(req *authorizationRequest, claims jwt.MapClaims, scopes sets.Set[string]) map[string]any {
	// Normalize headers to lowercased keys to align with Envoy's behavior.
	// Expose both single-value and multi-value header views for CEL.
	// - request.headers: lowercased keys, first value only.
	// - request.headers_all: lowercased keys, []string of all values.
	headers := map[string]string{}
	headersAll := map[string][]string{}
	for k, v := range req.Headers {
		if len(v) == 0 {
			continue
		}
		lk := strings.ToLower(k)
		headers[lk] = v[0]
		headersAll[lk] = append([]string(nil), v...)
	}

	request := map[string]any{
		"method":      req.HTTPMethod,
		"host":        req.Host,
		"headers":     headers,
		"headers_all": headersAll,
		"path":        req.HTTPPath,
		"auth": map[string]any{
			"jwt": map[string]any{
				"claims": claims,
				"scopes": sets.List(scopes),
			},
		},
		"mcp": map[string]any{
			"method":  req.MCPMethod,
			"backend": req.Backend,
			"tool":    req.Tool,
			"params":  normalizeParams(req.Params),
		},
	}
	// Only request is supported for now. Future expansions may include more context.
	return map[string]any{
		"request": request,
	}
}

// CEL sees the Go value as it is and we need to normalize it to a map[string]any so that CEL can refer to fields by their
// JSON tags (e.g. "arguments").
func normalizeParams(params mcp.Params) any {
	if params == nil {
		return nil
	}

	data, err := json.Marshal(params)
	if err != nil {
		return params
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return params
	}

	return parsed
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

// extractScopes extracts scopes from the "scope" claim (standard) or "scp" claim (common in Microsoft/Okta).
func extractScopes(claims jwt.MapClaims) []string {
	var scopes []string
	for _, key := range []string{"scope", "scp"} {
		raw, ok := claims[key]
		if !ok {
			continue
		}

		switch v := raw.(type) {
		case string:
			scopes = append(scopes, strings.Fields(v)...)
		case []string:
			scopes = append(scopes, v...)
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					scopes = append(scopes, s)
				}
			}
		}
	}
	return scopes
}

func (m *MCPProxy) evalRuleCEL(rule *compiledAuthorizationRule, activation map[string]any) (bool, error) {
	result, _, err := rule.celProgram.Eval(activation)
	if err != nil {
		m.l.Error("failed to evaluate authorization CEL", slog.String("error", err.Error()), slog.String("expression", rule.celExpression))
		return false, err
	}

	switch v := result.Value().(type) {
	case bool:
		return v, nil
	case types.Bool:
		return bool(v), nil
	default:
		m.l.Error("authorization CEL did not return a boolean", slog.String("expression", rule.celExpression))
		return false, errors.New("authorization CEL did not return a boolean")
	}
}

func (m *MCPProxy) toolMatches(backend, tool string, tools []filterapi.ToolCall) bool {
	// Empty tools means all tools match.
	if len(tools) == 0 {
		return true
	}

	for _, t := range tools {
		if t.Backend != backend || t.Tool != tool {
			continue
		}
		return true
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

func claimsSatisfied(claims jwt.MapClaims, required []filterapi.JWTClaim) bool {
	if len(required) == 0 {
		return true
	}

	for _, claim := range required {
		value, ok := lookupClaim(claims, claim.Name)
		if !ok {
			return false
		}

		switch claim.ValueType {
		case filterapi.JWTClaimValueTypeString:
			strVal, ok := value.(string)
			if !ok || !slices.Contains(claim.Values, strVal) {
				return false
			}
		case filterapi.JWTClaimValueTypeStringArray:
			if !claimHasAllowedString(value, claim.Values) {
				return false
			}
		default:
			return false
		}
	}

	return true
}

func lookupClaim(claims map[string]any, path string) (any, bool) {
	current := any(claims)
	for _, part := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := m[part]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

// When the claim is an array, check if any of the values is in the allowed list.
func claimHasAllowedString(value any, allowed []string) bool {
	switch v := value.(type) {
	case []string:
		for _, item := range v {
			if slices.Contains(allowed, item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if str, ok := item.(string); ok && slices.Contains(allowed, str) {
				return true
			}
		}
	// Handle the case where the claim is a single string instead of an array.
	// This avoids authorization failures when the claim matches but is not in an array.
	case string:
		return slices.Contains(allowed, v)
	}
	return false
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
