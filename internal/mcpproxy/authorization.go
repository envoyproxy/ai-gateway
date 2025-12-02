// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

func (m *MCPProxy) authorizeRequest(authorization *filterapi.MCPRouteAuthorization, headers http.Header, backendName, toolName string, argments any) bool {
	defaultAction := authorization.DefaultAction == filterapi.AuthorizationActionAllow

	// If there are no rules, return the default action.
	if len(authorization.Rules) == 0 {
		return defaultAction
	}

	// If the rules are defined, a valid bearer token is required.
	token, err := bearerToken(headers.Get("Authorization"))
	if err != nil {
		m.l.Info("missing or invalid bearer token", slog.String("error", err.Error()))
		return false
	}

	claims := jwt.MapClaims{}
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	// JWT verification is performed by Envoy before reaching here. So we only need to parse the token without verification.
	// codeql[go/missing-jwt-signature-check]
	if _, _, err := parser.ParseUnverified(token, claims); err != nil {
		m.l.Info("failed to parse JWT token", slog.String("error", err.Error()))
		return false
	}

	scopeSet := make(map[string]struct{})
	for _, scope := range extractScopes(claims) {
		scopeSet[scope] = struct{}{}
	}

	for _, rule := range authorization.Rules {
		var args map[string]any
		if argments != nil {
			if cast, ok := argments.(map[string]any); ok {
				args = cast
			}
		}
		if !toolMatches(args, filterapi.ToolCall{BackendName: backendName, ToolName: toolName}, rule.Target.Tools) {
			continue
		}
		if scopesSatisfied(scopeSet, rule.Source.JWTSource.Scopes) {
			return rule.Action == filterapi.AuthorizationActionAllow
		}
	}

	return defaultAction
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

func toolMatches(args map[string]any, target filterapi.ToolCall, tools []filterapi.ToolCall) bool {
	if len(tools) == 0 {
		return true
	}

	for _, t := range tools {
		if t.BackendName != target.BackendName || t.ToolName != target.ToolName {
			continue
		}
		if len(t.Arguments) == 0 {
			return true
		}
		if args == nil {
			return false
		}
		allMatch := true
		for key, pattern := range t.Arguments {
			rawVal, ok := args[key]
			if !ok {
				allMatch = false
				break
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				allMatch = false
				break
			}
			var data []byte
			if s, ok := rawVal.(string); ok {
				data = []byte(s)
			} else {
				jsonVal, err := json.Marshal(rawVal)
				if err != nil {
					allMatch = false
					break
				}
				data = jsonVal
			}
			if !re.Match(data) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return true
		}
	}
	// If no matching tool entry or no arguments matched, fail.
	return false
}

func scopesSatisfied(have map[string]struct{}, required []string) bool {
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
