// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package testtokenexchange provides a minimal OAuth 2.0 Token Exchange (RFC-8693)
// server for use in tests. It records the last received exchange request so tests
// can assert on which parameters were sent (including whether delegation vs.
// impersonation semantics were used) without running a real STS.
//
// The server issues real HS256-signed JWTs. The subject claim is taken from the
// incoming subject_token; for delegation mode the issued JWT includes an "act"
// claim whose sub is taken from the actor_token. Tests can verify the issued
// token with [ParseIssuedToken] using the well-known [TestSigningKey].
package testtokenexchangelib

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/envoyproxy/ai-gateway/internal/json"
)

const (
	// ListenerPortEnvVar is the environment variable that configures the server's listener port.
	ListenerPortEnvVar = "LISTENER_PORT"

	// GrantTypeTokenExchange is the grant_type value defined by RFC-8693.
	GrantTypeTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange" // #nosec G101

	// DefaultTokenType is the default token type URI for subject_token and actor_token.
	DefaultTokenType = "urn:ietf:params:oauth:token-type:access_token" // #nosec G101

	// TestSigningKey is the HMAC-SHA256 key used to sign JWTs issued by the test server.
	// Tests can use this key together with [ParseIssuedToken] to verify issued tokens.
	TestSigningKey = "test-token-exchange-signing-key-for-testing-only" // #nosec G101
)

// ExchangeRequest holds all RFC-8693 form parameters received in a token exchange POST.
// Fields are populated from the raw form values; unset parameters are empty strings.
type ExchangeRequest struct {
	// GrantType is the grant_type parameter. Should equal GrantTypeTokenExchange.
	GrantType string `json:"grant_type"`
	// SubjectToken is the incoming user token being exchanged.
	SubjectToken string `json:"subject_token"`
	// SubjectTokenType is the token type URI for SubjectToken.
	SubjectTokenType string `json:"subject_token_type"`
	// Audience is the intended recipient of the issued token (RFC-8693 §2.1).
	Audience string `json:"audience,omitempty"`
	// Resource is the URI of the target resource (RFC-8693 §2.1, RFC-8707).
	Resource string `json:"resource,omitempty"`
	// Scope is the space-separated list of requested scopes.
	Scope string `json:"scope,omitempty"`
	// RequestedTokenType is the desired type of the issued token.
	RequestedTokenType string `json:"requested_token_type,omitempty"`
	// ActorToken is the token representing the acting party (gateway). Non-empty only
	// when delegation semantics are used (RFC-8693 §1.1).
	ActorToken string `json:"actor_token,omitempty"`
	// ActorTokenType is the token type URI for ActorToken.
	ActorTokenType string `json:"actor_token_type,omitempty"`
	// ClientID is the OAuth 2.0 client_id sent to authenticate the exchange request.
	ClientID string `json:"client_id,omitempty"`
	// ClientSecret is the OAuth 2.0 client_secret sent to authenticate the exchange request.
	ClientSecret string `json:"client_secret,omitempty"`
}

// IsDelegation reports whether the exchange request used delegation semantics
// (RFC-8693 §1.1) by checking whether an actor_token was included.
func (r *ExchangeRequest) IsDelegation() bool {
	return r.ActorToken != ""
}

// ActClaim represents the RFC-8693 "act" (authorized actor) claim embedded in the issued JWT.
// It identifies the party that is acting on behalf of the subject.
type ActClaim struct {
	Sub string `json:"sub"`
}

// IssuedClaims holds the full claim set of a JWT issued by the test token exchange server.
// Tests should use [ParseIssuedToken] to decode and verify issued tokens.
type IssuedClaims struct {
	jwt.RegisteredClaims
	// Act is the RFC-8693 "act" claim identifying the delegated actor (gateway).
	// It is present only when delegation semantics are used (actor_token was provided).
	Act *ActClaim `json:"act,omitempty"`
}

// ParseIssuedToken parses and verifies a JWT issued by the test token exchange server
// using the well-known [TestSigningKey]. Returns the decoded claims or an error.
func ParseIssuedToken(tokenString string) (*IssuedClaims, error) {
	claims := &IssuedClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(_ *jwt.Token) (interface{}, error) {
		return []byte(TestSigningKey), nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// tokenExchangeResponse is the JSON body returned by the test server for successful exchanges.
type tokenExchangeResponse struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int    `json:"expires_in"`
}

// Server is a test STS that implements the minimal RFC-8693 token exchange flow.
// It records the last received exchange request for test assertions.
type Server struct {
	mu          sync.RWMutex
	LastRequest *ExchangeRequest
	logger      *log.Logger
}

// NewServer creates a Server and starts an HTTP listener on the given port.
// Returns the Server (for in-process assertions) and the *http.Server (for lifecycle management).
func NewServer(port int) (*Server, *http.Server) {
	s := &Server{
		logger: log.New(os.Stdout, "[testtokenexchange] ", 0),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/token", s.handleTokenExchange)
	mux.HandleFunc("/last-request", s.handleLastRequest)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		s.logger.Fatalf("failed to listen on port %d: %v", port, err)
	}

	srv := &http.Server{Handler: mux} //nolint:gosec
	s.logger.Printf("starting token exchange test server on port %d", port)
	go func() {
		if err := srv.Serve(lis); err != nil && err != http.ErrServerClosed {
			s.logger.Fatalf("failed to serve: %v", err)
		}
	}()

	return s, srv
}

// handleTokenExchange handles POST /token. It parses the RFC-8693 form parameters,
// stores the request, and returns a minimal token exchange response.
func (s *Server) handleTokenExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}

	clientID, clientSecret, _ := r.BasicAuth()

	req := &ExchangeRequest{
		GrantType:          r.FormValue("grant_type"),
		SubjectToken:       r.FormValue("subject_token"),
		SubjectTokenType:   r.FormValue("subject_token_type"),
		Audience:           r.FormValue("audience"),
		Resource:           r.FormValue("resource"),
		Scope:              r.FormValue("scope"),
		RequestedTokenType: r.FormValue("requested_token_type"),
		ActorToken:         r.FormValue("actor_token"),
		ActorTokenType:     r.FormValue("actor_token_type"),
		ClientID:           clientID,
		ClientSecret:       clientSecret,
	}

	s.logger.Printf("token exchange request: grant_type=%q subject_token=%q audience=%q actor_token=%q client_id=%q",
		req.GrantType, req.SubjectToken, req.Audience, req.ActorToken, req.ClientID)

	s.mu.Lock()
	s.LastRequest = req
	s.mu.Unlock()

	issuedToken, err := s.issueToken(req)
	if err != nil {
		s.logger.Printf("failed to issue token: %v", err)
		http.Error(w, "failed to issue token", http.StatusInternalServerError)
		return
	}

	resp := tokenExchangeResponse{
		AccessToken:     issuedToken,
		IssuedTokenType: DefaultTokenType,
		TokenType:       "Bearer",
		ExpiresIn:       3600,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Printf("failed to encode response: %v", err)
	}
}

// issueToken generates a signed HS256 JWT for a token exchange request.
//
// The issued JWT claims are derived from the incoming subject_token and actor_token:
//   - sub: copied from the subject_token's "sub" claim (fallback: raw token string).
//   - aud: set from the exchange request's audience parameter when present.
//   - act: set to {"sub": <actor_token.sub>} when an actor_token is provided (delegation mode).
//     The actor's sub is taken from the actor_token's "sub" claim (fallback: raw token string).
//
// The token is signed with HMAC-SHA256 using [TestSigningKey].
func (s *Server) issueToken(req *ExchangeRequest) (string, error) {
	p := jwt.NewParser()
	now := time.Now()

	// Extract sub from subject_token. It may or may not be a JWT.
	subjectClaims := jwt.MapClaims{}
	_, _, _ = p.ParseUnverified(req.SubjectToken, subjectClaims)
	sub, _ := subjectClaims["sub"].(string)
	if sub == "" {
		sub = req.SubjectToken // fallback for opaque subject tokens.
	}

	claims := &IssuedClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "testtokenexchange",
			Subject:   sub,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	}
	if req.Audience != "" {
		claims.Audience = jwt.ClaimStrings{req.Audience}
	}

	// For delegation mode, embed the actor's identity in the "act" claim.
	if req.ActorToken != "" {
		actorClaims := jwt.MapClaims{}
		_, _, _ = p.ParseUnverified(req.ActorToken, actorClaims)
		actorSub, _ := actorClaims["sub"].(string)
		if actorSub == "" {
			actorSub = req.ActorToken // fallback for opaque actor tokens.
		}
		claims.Act = &ActClaim{Sub: actorSub}
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(TestSigningKey))
}

// handleLastRequest handles GET /last-request. It returns the last received
// ExchangeRequest as JSON, or 404 if no exchange has been performed yet.
func (s *Server) handleLastRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	req := s.GetLastRequest()

	if req == nil {
		http.Error(w, "no exchange request received yet", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(req); err != nil {
		s.logger.Printf("failed to encode last request: %v", err)
	}
}

// GetLastRequest returns the last received ExchangeRequest, or nil if no exchange has been performed yet.
func (s *Server) GetLastRequest() *ExchangeRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LastRequest
}
