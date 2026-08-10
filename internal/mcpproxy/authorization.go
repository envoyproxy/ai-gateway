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
	"reflect"
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
	// CEL expression compiled for request-level evaluation (with tool constraints).
	celExpression string
	celProgram    cel.Program
	// Backend-only CEL program: same expression as celProgram, but compiled with
	// cel.OptPartialEval so authorizeBackendOnly can mark request.mcp.method/tool/params
	// as unknown attributes during the initialize-phase pre-check (see
	// backendOnlyUnknownAttrs). This lets CEL correctly decide any part of the
	// expression that doesn't depend on the future call's specifics (e.g. a backend,
	// HTTP-level, or header check) while reporting Unknown only for the part that
	// genuinely does.
	// Pre-compiled to avoid repeated compilation during request handling.
	backendOnlyProgram cel.Program
}

// same reports whether two compiledAuthorization values are semantically equivalent.
// celProgram and backendOnlyProgram are excluded because they are derived from
// celExpression and are not comparable.
func (a *compiledAuthorization) same(other *compiledAuthorization) bool {
	if a == nil || other == nil {
		return a == other
	}
	if a.ResourceMetadataURL != other.ResourceMetadataURL || a.DefaultAction != other.DefaultAction {
		return false
	}
	return slices.EqualFunc(a.Rules, other.Rules, func(ra, rb compiledAuthorizationRule) bool {
		return ra.Action == rb.Action &&
			ra.celExpression == rb.celExpression &&
			reflect.DeepEqual(ra.Source, rb.Source) &&
			reflect.DeepEqual(ra.Target, rb.Target)
	})
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

// authorizationCELCostLimit is the per-evaluation cost budget for compiled
// authorization CEL expressions. It applies to both request-time and
// backend-only evaluations.
const authorizationCELCostLimit = 10000

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
			program, err := env.Program(ast, cel.CostLimit(authorizationCELCostLimit), cel.EvalOptions(cel.OptOptimize))
			if err != nil {
				return nil, fmt.Errorf("failed to build rule CEL program: %w", err)
			}
			// Requires cel.OptPartialEval, which authorizeBackendOnly relies on to mark
			// request.mcp.tool as unknown during the initialize-phase pre-check.
			// See the field doc on backendOnlyProgram.
			backendOnlyProgram, err := env.Program(ast, cel.CostLimit(authorizationCELCostLimit), cel.EvalOptions(cel.OptOptimize, cel.OptPartialEval))
			if err != nil {
				return nil, fmt.Errorf("failed to build backend-only CEL program: %w", err)
			}
			cr.celExpression = expr
			cr.celProgram = program
			cr.backendOnlyProgram = backendOnlyProgram
		}
		compiled.Rules = append(compiled.Rules, cr)
	}

	return compiled, nil
}

// extractClaimsAndScopes parses JWT from headers and extracts claims and scopes.
// Used by both authorizeRequest and authorizeBackendOnly to avoid duplication.
func (m *mcpRequestContext) extractClaimsAndScopes(headers http.Header, logContext string) (jwt.MapClaims, sets.Set[string]) {
	scopeSet := sets.New[string]()
	claims := jwt.MapClaims{}

	token, err := bearerToken(headers.Get("Authorization"))
	if err != nil {
		m.l.Info("missing or invalid bearer token", slog.String("context", logContext), slog.String("error", err.Error()))
	} else {
		// JWT verification is performed by Envoy before reaching here. So we only need to parse the token without verification.
		if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
			m.l.Info("failed to parse JWT token", slog.String("context", logContext), slog.String("error", err.Error()))
		} else {
			scopeSet = sets.New(extractScopes(claims)...)
			// Scopes are handled separately, remove them from the claims map to avoid interference.
			// "scp" is also removed as it is a common alias for "scope" (e.g. Azure AD, Okta).
			delete(claims, "scope")
			delete(claims, "scp")
		}
	}
	return claims, scopeSet
}

// authorizeRequest authorizes the request based on the given MCPRouteAuthorization configuration.
func (m *mcpRequestContext) authorizeRequest(authorization *compiledAuthorization, req *authorizationRequest) (bool, []string) {
	if authorization == nil {
		return true, nil
	}

	defaultAction := authorization.DefaultAction == filterapi.AuthorizationActionAllow

	// If no rules are defined, return the default action.
	if len(authorization.Rules) == 0 {
		return defaultAction, nil
	}

	claims, scopeSet := m.extractClaimsAndScopes(req.Headers, "authorizeRequest")

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
			requiredScopesForChallenge = preferSmallerScopeChallenge(requiredScopesForChallenge, requiredScopes)
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

func (m *mcpRequestContext) evalRuleCEL(rule *compiledAuthorizationRule, activation map[string]any) (bool, error) {
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

func (m *mcpRequestContext) toolMatches(backend, tool string, tools []filterapi.ToolCall) bool {
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

// backendMatches checks if the backend is in the target tools list (ignoring tool constraints).
// Used for backend-only authorization pre-checks during the initialize phase.
func (m *mcpRequestContext) backendMatches(backend string, tools []filterapi.ToolCall) bool {
	// Empty tools means all backends match.
	if len(tools) == 0 {
		return true
	}

	for _, t := range tools {
		if t.Backend == backend {
			return true
		}
	}
	// If no matching backend entry, fail.
	return false
}

// backendOnlyUnknownAttrs marks the parts of request.mcp that describe the
// specific future call (which JSON-RPC method, which tool, with what params)
// as unknown CEL attributes for the initialize-phase backend-only pre-check
// (see evalBackendOnlyCEL). None of these are known yet at initialize time -
// unlike request.method/request.host/request.path, which belong to the
// current HTTP request and are the same for every request on this session,
// request.mcp.method/tool/params describe whatever future tools/call or
// tools/list request eventually happens. This lets cel-go's partial
// evaluation correctly decide any part of a rule's expression that doesn't
// depend on them (e.g. a backend, HTTP-level, or header check), while
// reporting the dependent part as undecidable rather than resolving it to a
// zero value like "".
var backendOnlyUnknownAttrs = []*cel.AttributePatternType{
	cel.AttributePattern("request").QualString("mcp").QualString("method"),
	cel.AttributePattern("request").QualString("mcp").QualString("tool"),
	cel.AttributePattern("request").QualString("mcp").QualString("params"),
}

// evalBackendOnlyCEL evaluates a rule's backend-only CEL program against a partial
// activation with request.mcp.method/tool/params marked unknown via
// backendOnlyUnknownAttrs. ambiguous is true whenever the result can't be resolved to
// a concrete boolean without knowing the future call's specifics - this includes
// cel-go reporting Unknown, but also a program error or a non-boolean result, which
// are treated the same as ambiguous (never as a definite non-match) so that any CEL
// construct fails safe. See authorizeBackendOnly for how Allow vs Deny rules use the
// ambiguous flag to stay on the permissive side of "never skip a backend that should
// have been attempted."
func (m *mcpRequestContext) evalBackendOnlyCEL(rule *compiledAuthorizationRule, partial cel.PartialActivation) (match, ambiguous bool) {
	result, _, err := rule.backendOnlyProgram.Eval(partial)
	if err != nil {
		m.l.Debug("backend-only CEL evaluation error, treating as ambiguous", slog.String("error", err.Error()), slog.String("expression", rule.celExpression))
		return false, true
	}
	if types.IsUnknown(result) {
		return false, true
	}

	b, ok := result.Value().(bool)
	if !ok {
		m.l.Debug("backend-only CEL did not return a boolean, treating as ambiguous", slog.String("expression", rule.celExpression))
		return false, true
	}
	return b, false
}

// authorizeBackendOnly evaluates whether the client has potential access to a backend,
// ignoring specific tool constraints. Evaluates the same authorization rules as authorizeRequest,
// but with request.mcp.method/tool/params marked unknown for CEL evaluation (see
// evalBackendOnlyCEL and backendOnlyUnknownAttrs), so CEL rules that reference the backend,
// HTTP method/host/path, headers, or JWT claims are still decided correctly while
// call-specific constraints come back ambiguous.
// Used during initialize phase to avoid unnecessary connections to denied backends.
// The returned scopes mirror authorizeRequest's requiredScopesForChallenge: the smallest
// set of scopes that would have granted access via an Allow rule, for building a
// WWW-Authenticate challenge if every backend in the route ends up denied.
func (m *mcpRequestContext) authorizeBackendOnly(authorization *compiledAuthorization, backend string, headers http.Header, httpMethod, host, httpPath string, claims jwt.MapClaims, scopeSet sets.Set[string]) (bool, []string) {
	if authorization == nil {
		return true, nil
	}

	defaultAction := authorization.DefaultAction == filterapi.AuthorizationActionAllow

	// If no rules are defined, return the default action.
	if len(authorization.Rules) == 0 {
		return defaultAction, nil
	}

	// celActivation and partialActivation are built once per backend (not per rule)
	// the first time a CEL-bearing rule needs them, and reused across the remaining
	// rules for this backend.
	var celActivation map[string]any
	var partialActivation cel.PartialActivation
	var requiredScopesForChallenge []string

	for i := range authorization.Rules {
		rule := &authorization.Rules[i]
		action := rule.Action == filterapi.AuthorizationActionAllow

		// Evaluate backend-only CEL program if present. Build full activation structure
		// (same as authorizeRequest) but with request.mcp.method/tool/params marked
		// unknown for CEL (see evalBackendOnlyCEL and backendOnlyUnknownAttrs). This
		// ensures all other CEL field references work (request.mcp.backend,
		// auth.jwt.claims, request.headers, request.method/host/path, etc.) while
		// call-specific conditions come back ambiguous.
		if rule.backendOnlyProgram != nil {
			if celActivation == nil {
				// HTTPMethod, Host, and HTTPPath belong to the current HTTP request and
				// are the same for every request on this session, so they're populated
				// with real values here, same as authorizeRequest. MCPMethod, Tool, and
				// Params describe whichever future tools/call or tools/list request
				// eventually happens - those are genuinely unknown at this phase and left
				// unset; evalBackendOnlyCEL marks them unknown via backendOnlyUnknownAttrs,
				// which intercepts attribute resolution before it would reach this map.
				req := &authorizationRequest{
					Headers:    headers,
					Backend:    backend,
					HTTPMethod: httpMethod,
					Host:       host,
					HTTPPath:   httpPath,
				}

				// Build full activation structure using existing function.
				celActivation = buildCELActivation(req, claims, scopeSet)

				var err error
				partialActivation, err = cel.PartialVars(celActivation, backendOnlyUnknownAttrs...)
				if err != nil {
					m.l.Debug("failed to build partial CEL activation for backend-only pre-check", slog.String("error", err.Error()), slog.String("backend", backend))
				}
			}

			// If the partial activation couldn't be built, there's nothing to evaluate
			// against - treat it the same as evalBackendOnlyCEL treats an evaluation
			// error: ambiguous, never a definite non-match.
			var match, ambiguous bool
			if partialActivation == nil {
				ambiguous = true
			} else {
				match, ambiguous = m.evalBackendOnlyCEL(rule, partialActivation)
			}
			if ambiguous {
				// The expression's truth value depends on request.mcp.tool, which isn't
				// known yet at this phase. A Deny rule can't be proven to fire for
				// whichever tool is eventually requested, so it must not block the
				// backend here - defer to the real per-tool check in authorizeRequest.
				// An Allow rule can't be ruled out either: don't treat the CEL gate as
				// failed, fall through and let Target/Source decide instead.
				if !action {
					continue
				}
			} else if !match {
				continue
			}
		}

		// Check if backend is in the target list (without tool constraints).
		if rule.Target != nil {
			if !m.backendMatches(backend, rule.Target) {
				continue
			}

			// A Deny rule with a specific (tool-scoped) target can't be evaluated at
			// the backend level at all: backendMatches ignores the Tool field entirely
			// (it can't know which tool will be called yet), so a rule meant to deny
			// ONE tool on this backend would otherwise be indistinguishable here from
			// a rule meant to deny the WHOLE backend, and this pre-check would
			// incorrectly reject every other tool on the backend along with it. This
			// holds regardless of whether the rule also has CEL: a CEL condition can
			// be fully decided (e.g. it never references the tool) while the Target's
			// tool-scoping is still a completely separate, still-unresolved source of
			// ambiguity - the two aren't the same thing, so a decided CEL result does
			// not make the rule as a whole decided.
			//
			// Skip it instead: defer to later rules / DefaultAction, and let the real
			// per-call authorizeRequest (which does check Tool, via toolMatches) make the
			// actual, correctly-scoped decision once a specific tool is known. This can
			// only ever make the pre-check MORE permissive for this rule (attempt a
			// session that authorizeRequest may still deny per-tool) — it can never make
			// it reject a backend that would otherwise have been let through, since
			// returning here was already the most restrictive possible outcome.
			if !action {
				continue
			}
		}

		// If no source is specified, the rule matches.
		if rule.Source == nil {
			return action, nil
		}

		// Check source if specified.
		if !claimsSatisfied(claims, rule.Source.JWT.Claims) {
			continue
		}

		// Scopes check.
		requiredScopes := rule.Source.JWT.Scopes
		if scopesSatisfied(scopeSet, requiredScopes) {
			return action, nil
		}

		// Keep track of the smallest set of required scopes for challenge when the action is allow and the request is denied.
		if action {
			requiredScopesForChallenge = preferSmallerScopeChallenge(requiredScopesForChallenge, requiredScopes)
		}
	}

	return defaultAction, requiredScopesForChallenge
}

// preferSmallerScopeChallenge returns whichever of current/candidate is the better
// WWW-Authenticate scope challenge to keep: the smallest non-empty scope set, on the
// theory that it's the easiest additional requirement for the client to satisfy.
// candidate may be nil/empty - e.g. when aggregating across backends denied for
// unrelated reasons (a Deny rule vs. an unmet Allow-rule scope) - so, unlike a single
// rule loop where an empty requiredScopes would already have returned via
// scopesSatisfied before reaching this comparison, nil/empty here must never be
// treated as "smaller" and win over a real candidate.
func preferSmallerScopeChallenge(current, candidate []string) []string {
	if len(current) == 0 {
		return candidate
	}
	if len(candidate) > 0 && len(candidate) < len(current) {
		return candidate
	}
	return current
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
