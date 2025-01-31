package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/envoyproxy/ai-gateway/internal/version"
	"github.com/envoyproxy/ai-gateway/tests/internal/mockoidclib"
)

var logger = log.New(os.Stdout, "[mockoidc] ", 0)

func main() {
	logger.Println("Version: ", version.Version)
	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()
	doMain(l)
}

func doMain(l net.Listener) {
	defer l.Close()
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	http.HandleFunc("/", handler)
	if err := http.Serve(l, nil); err != nil {
		logger.Printf("failed to serve: %v", err)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	for k, v := range r.Header {
		logger.Printf("header %q: %s\n", k, v)
	}

	// Get the expected response for this path
	responseBody := r.Header.Get(mockoidclib.ResponseBodyHeaderKey)
	if responseBody == "" {
		// If no response body is specified, use the default response for well-known paths
		var err error
		responseBody, err = getDefaultResponse(r.URL.Path)
		if err != nil {
			logger.Println("failed to get default response:", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		// Decode the base64 encoded response body
		decoded, err := base64.StdEncoding.DecodeString(responseBody)
		if err != nil {
			logger.Println("failed to decode response body:", err)
			http.Error(w, "failed to decode response body", http.StatusBadRequest)
			return
		}
		responseBody = string(decoded)
	}

	// Get the response status code
	status := http.StatusOK
	if statusStr := r.Header.Get(mockoidclib.ResponseStatusHeaderKey); statusStr != "" {
		var err error
		status, err = strconv.Atoi(statusStr)
		if err != nil {
			logger.Println("failed to parse status code:", err)
			http.Error(w, "failed to parse status code", http.StatusBadRequest)
			return
		}
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	if headersStr := r.Header.Get(mockoidclib.ResponseHeadersKey); headersStr != "" {
		headers, err := base64.StdEncoding.DecodeString(headersStr)
		if err != nil {
			logger.Println("failed to decode headers:", err)
			http.Error(w, "failed to decode headers", http.StatusBadRequest)
			return
		}
		for _, header := range headers {
			parts := strings.SplitN(string(header), ":", 2)
			if len(parts) != 2 {
				continue
			}
			w.Header().Set(parts[0], parts[1])
		}
	}

	w.WriteHeader(status)
	fmt.Fprintln(w, responseBody)
}

func getDefaultResponse(path string) (string, error) {
	issuer := os.Getenv("ISSUER")
	if issuer == "" {
		return "", fmt.Errorf("ISSUER environment variable not set")
	}

	switch path {
	case "/.well-known/openid-configuration":
		discovery := map[string]interface{}{
			"issuer":                                issuer,
			"token_endpoint":                        fmt.Sprintf("%s/token", issuer),
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"scopes_supported":                      []string{"openid"},
			"token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
		}
		resp, err := json.Marshal(discovery)
		if err != nil {
			return "", fmt.Errorf("failed to marshal discovery response: %v", err)
		}
		return string(resp), nil

	case "/token":
		// Verify client credentials
		clientID := os.Getenv("CLIENT_ID")
		clientSecret := os.Getenv("CLIENT_SECRET")
		if clientID == "" || clientSecret == "" {
			return "", fmt.Errorf("CLIENT_ID or CLIENT_SECRET environment variable not set")
		}

		// Generate a mock ID token
		now := time.Now()
		exp := now.Add(1 * time.Hour)
		claims := map[string]interface{}{
			"iss": issuer,
			"sub": "test-subject",
			"aud": clientID,
			"exp": exp.Unix(),
			"iat": now.Unix(),
		}

		claimsJSON, err := json.Marshal(claims)
		if err != nil {
			return "", fmt.Errorf("failed to marshal claims: %v", err)
		}

		idToken := fmt.Sprintf("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.%s.test",
			base64.RawURLEncoding.EncodeToString(claimsJSON))

		response := map[string]interface{}{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"id_token":     idToken,
			"expires_in":   3600,
		}

		resp, err := json.Marshal(response)
		if err != nil {
			return "", fmt.Errorf("failed to marshal token response: %v", err)
		}
		return string(resp), nil

	default:
		return "", fmt.Errorf("unknown path: %s", path)
	}
}
