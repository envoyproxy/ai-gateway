package mockoidc

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/version"
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
	http.HandleFunc("/health", handleHealthCheck)
	http.HandleFunc("/oauth2/token", handleTokenRequest)
	if err := http.Serve(l, nil); err != nil {
		logger.Printf("failed to serve: %v", err)
	}
}

func handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func handleTokenRequest(w http.ResponseWriter, r *http.Request) {
	// Get expected values from headers
	testUpstreamID := r.Header.Get("x-expected-testupstream-id")
	expectedHost := r.Header.Get("x-expected-host")
	expectedHeadersB64 := r.Header.Get("x-expected-headers")
	expectedRequestBodyB64 := r.Header.Get("x-expected-request-body")
	responseBodyB64 := r.Header.Get("x-response-body")
	responseStatusStr := r.Header.Get("x-response-status")
	responseHeadersB64 := r.Header.Get("x-response-headers")

	// Log request details for debugging
	log.Printf("[testupstream] Received request to /oauth2/token")
	for name, values := range r.Header {
		log.Printf("[testupstream] header %q: %v", name, values)
	}

	// Validate testupstream ID
	if testUpstreamID == "" {
		log.Printf("[testupstream] no expected testupstream-id")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Validate host
	if expectedHost != "" && r.Host != expectedHost {
		log.Printf("[testupstream] host mismatch: expected %q, got %q", expectedHost, r.Host)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Validate headers if specified
	if expectedHeadersB64 != "" {
		expectedHeadersBytes, err := base64.StdEncoding.DecodeString(expectedHeadersB64)
		if err != nil {
			log.Printf("[testupstream] failed to decode expected headers: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		expectedHeaders := strings.Split(string(expectedHeadersBytes), ",")
		for _, headerPair := range expectedHeaders {
			parts := strings.SplitN(headerPair, ":", 2)
			if len(parts) != 2 {
				continue
			}
			headerName := parts[0]
			expectedValue := parts[1]
			actualValue := r.Header.Get(headerName)
			if actualValue != expectedValue {
				log.Printf("[testupstream] header mismatch for %q: expected %q, got %q", headerName, expectedValue, actualValue)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
	}

	// Validate request body if specified
	if expectedRequestBodyB64 != "" {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("[testupstream] failed to read request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		expectedBodyBytes, err := base64.StdEncoding.DecodeString(expectedRequestBodyB64)
		if err != nil {
			log.Printf("[testupstream] failed to decode expected request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Parse both expected and actual bodies as URL values for comparison
		expectedValues, err := url.ParseQuery(string(expectedBodyBytes))
		if err != nil {
			log.Printf("[testupstream] failed to parse expected request body as form data: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		actualValues, err := url.ParseQuery(string(body))
		if err != nil {
			log.Printf("[testupstream] failed to parse actual request body as form data: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Compare form values
		if !urlValuesEqual(expectedValues, actualValues) {
			log.Printf("[testupstream] request body mismatch: expected %q, got %q", expectedBodyBytes, body)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	// Set response status
	statusCode := http.StatusOK
	if responseStatusStr != "" {
		if code, err := parseStatusCode(responseStatusStr); err == nil {
			statusCode = code
		}
	}

	// Set response headers if specified
	if responseHeadersB64 != "" {
		headersBytes, err := base64.StdEncoding.DecodeString(responseHeadersB64)
		if err != nil {
			log.Printf("[testupstream] failed to decode response headers: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		headers := strings.Split(string(headersBytes), ",")
		for _, headerPair := range headers {
			parts := strings.SplitN(headerPair, ":", 2)
			if len(parts) != 2 {
				continue
			}
			w.Header().Set(parts[0], parts[1])
		}
	}

	// Write response body if specified
	if responseBodyB64 != "" {
		responseBody, err := base64.StdEncoding.DecodeString(responseBodyB64)
		if err != nil {
			log.Printf("[testupstream] failed to decode response body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(statusCode)
		w.Write(responseBody)
		return
	}

	// Default response if no specific response is configured
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	fmt.Fprintf(w, `{"error":"no response configured"}`)
}

func parseStatusCode(s string) (int, error) {
	var code int
	if _, err := fmt.Sscanf(s, "%d", &code); err != nil {
		return http.StatusOK, err
	}
	return code, nil
}

func urlValuesEqual(expected, actual url.Values) bool {
	if len(expected) != len(actual) {
		return false
	}
	for key, expectedValues := range expected {
		actualValues := actual[key]
		if len(expectedValues) != len(actualValues) {
			return false
		}
		// Sort both slices for consistent comparison
		expectedStr := strings.Join(expectedValues, ",")
		actualStr := strings.Join(actualValues, ",")
		if expectedStr != actualStr {
			return false
		}
	}
	return true
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
			"token_endpoint":                        fmt.Sprintf("%s/oauth2/token", issuer),
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

	case "/oauth2/token":
		// Token endpoint is handled directly in the handler
		return "", fmt.Errorf("token endpoint should be handled by the main handler")

	default:
		return "", fmt.Errorf("unknown path: %s", path)
	}
}
