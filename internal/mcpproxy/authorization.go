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
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

// authorizeRequest authorizes the request based on the given MCPRouteAuthorization configuration.
func (m *MCPProxy) authorizeRequest(authorization *filterapi.MCPRouteAuthorization, headers http.Header, backendName, toolName string, argments any) bool {
	if authorization == nil {
		return true
	}

	// If no rules are defined, deny all requests.
	if len(authorization.Rules) == 0 {
		return false
	}

	// If the rules are defined, a valid bearer token is required.
	token, err := bearerToken(headers.Get("Authorization"))
	// This is just a sanity check. The actual JWT verification is performed by Envoy before reaching here, and the token
	// should always be present and valid.
	if err != nil {
		m.l.Info("missing or invalid bearer token", slog.String("error", err.Error()))
		return false
	}

	claims := jwt.MapClaims{}
	// JWT verification is performed by Envoy before reaching here. So we only need to parse the token without verification.
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		m.l.Info("failed to parse JWT token", slog.String("error", err.Error()))
		return false
	}

	scopeSet := sets.New[string](extractScopes(claims)...)

	for _, rule := range authorization.Rules {
		var args map[string]any
		if argments != nil {
			if cast, ok := argments.(map[string]any); ok {
				args = cast
			}
		}
		if !m.toolMatches(filterapi.ToolCall{BackendName: backendName, ToolName: toolName}, rule.Target.Tools, args) {
			continue
		}
		if scopesSatisfied(scopeSet, rule.Source.JWTSource.Scopes) {
			return true
		}
	}

	return false
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

func (m *MCPProxy) toolMatches(target filterapi.ToolCall, tools []filterapi.ToolCall, args map[string]any) bool {
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
				m.l.Error("invalid argument regex pattern", slog.String("pattern", pattern), slog.String("error", err.Error()))
				allMatch = false
				break
			}
			var data []byte
			if s, ok := rawVal.(string); ok {
				data = []byte(s)
			} else {
				jsonVal, err := json.Marshal(rawVal)
				if err != nil {
					m.l.Error("failed to marshal argument value to json", slog.String("key", key), slog.String("error", err.Error()))
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
