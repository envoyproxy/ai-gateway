package backendauth

import (
	"fmt"
	"os"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/filterconfig"
)

// apiKeyHandler implements [Handler] for api key authz.
type apiKeyHandler struct {
	apiKey string
}

func newAPIKeyHandler(auth *filterconfig.APIKeyAuth) (Handler, error) {
	secret, err := os.ReadFile(auth.Filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read api key file: %w", err)
	}
	return &apiKeyHandler{apiKey: strings.TrimSpace(string(secret))}, nil
}

// Do implements [Handler.Do].
//
// Extracts the api key from the local file and set it as an authorization header.
func (a *apiKeyHandler) Do(requestHeaders map[string]string, headerMut *extprocv3.HeaderMutation, _ *extprocv3.BodyMutation) error {
	requestHeaders["Authorization"] = fmt.Sprintf("Bearer %s", a.apiKey)
	headerMut.SetHeaders = append(headerMut.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: "Authorization", RawValue: []byte(requestHeaders["Authorization"])},
	})

	return nil
}
