// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

type azureHandler struct {
	azureAccessToken string
}

func newAzureHandler(azureAuth *filterapi.AzureAuth) (Handler, error) {
	if azureAuth != nil {
		content, err := os.ReadFile(azureAuth.Filename)
		if err != nil {
			return nil, fmt.Errorf("failed to read azure access token file: %w", err)
		}
		// Extract access token from secret which content a key-value string
		// such as `azure_access_token: secret_value`.
		lines := strings.Split(string(content), "\n")
		var azureAccessToken string
		for _, line := range lines {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[0]) == "azure_access_token" {
				azureAccessToken = strings.TrimSpace(parts[1])
				break
			}
		}
		if azureAccessToken == "" {
			return nil, fmt.Errorf("azure_access_token not found in the secret file")
		}
		return &azureHandler{azureAccessToken: azureAccessToken}, nil
	}
	return nil, fmt.Errorf("azure auth configuration is required")
}

func (a *azureHandler) Do(_ context.Context, requestHeaders map[string]string, headerMut *extprocv3.HeaderMutation, bodyMut *extprocv3.BodyMutation) error {
	requestHeaders["Authorization"] = fmt.Sprintf("Bearer %s", a.azureAccessToken)
	headerMut.SetHeaders = append(headerMut.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: "Authorization", RawValue: []byte(requestHeaders["Authorization"])},
	})

	// lookup model name from request body
	model, err := extractModel(bodyMut.GetBody())
	if err != nil {
		return fmt.Errorf("cannot extract model from request: %w", err)
	}

	err = mutateHeader(requestHeaders, headerMut, model)
	if err != nil {
		return fmt.Errorf("failed to mutate header: %w", err)
	}
	return nil
}

func extractModel(body []byte) (string, error) {
	type requestBody struct {
		Model string `json:"model"`
	}
	var reqBody requestBody
	if err := json.Unmarshal(body, &reqBody); err != nil {
		return "", err
	}
	return reqBody.Model, nil
}

func mutateHeader(requestHeaders map[string]string, headerMut *extprocv3.HeaderMutation, model string) error {
	// assume deployment_id equal to model value in request body
	// hardcoded Azure api-version for now, see https://learn.microsoft.com/en-us/azure/ai-services/openai/reference-preview#data-plane-inference
	azurePath := fmt.Sprintf("/openai/deployments/%s/chat/completion?api-version=2025-02-01-preview", model)
	// only support Azure chat completion endpoint for now
	if requestHeaders[":path"] != "/v1/chat/completions" {
		return fmt.Errorf("unsupported request path for Azure OpenAI: %s", requestHeaders[":path"])
	}
	requestHeaders[":path"] = azurePath
	if headerMut.SetHeaders != nil {
		for _, h := range headerMut.SetHeaders {
			if h.Header.Key == ":path" {
				if len(h.Header.Value) > 0 {
					h.Header.Value = azurePath
				} else {
					h.Header.RawValue = []byte(azurePath)
				}
				break
			}
		}
	}
	return nil
}
