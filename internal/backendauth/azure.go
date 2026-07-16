// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// azureCognitiveServicesScope is the OAuth 2.0 scope used to request Entra tokens for Azure OpenAI /
// Cognitive Services when authenticating with ambient Azure Workload Identity.
const azureCognitiveServicesScope = "https://cognitiveservices.azure.com/.default"

type azureHandler struct {
	// azureAccessToken is a static, pre-rotated access token. Used when non-empty.
	azureAccessToken string
	// cred is the ambient Azure Workload Identity credential. Used when azureAccessToken is empty.
	cred azcore.TokenCredential
}

func newAzureHandler(auth *filterapi.AzureAuth) (filterapi.BackendAuthHandler, error) {
	if auth == nil {
		return nil, fmt.Errorf("azure auth configuration cannot be nil")
	}
	if token := strings.TrimSpace(auth.AccessToken); token != "" {
		// Static token path: the controller rotates the token into a secret.
		return &azureHandler{azureAccessToken: token}, nil
	}

	// Ambient Azure Workload Identity path: the SDK mints an Entra token from the projected
	// service-account token. ClientID/TenantID pin the identity when provided, otherwise the SDK
	// reads AZURE_CLIENT_ID/AZURE_TENANT_ID from the environment.
	opts := &azidentity.WorkloadIdentityCredentialOptions{}
	if clientID := strings.TrimSpace(auth.ClientID); clientID != "" {
		opts.ClientID = clientID
	}
	if tenantID := strings.TrimSpace(auth.TenantID); tenantID != "" {
		opts.TenantID = tenantID
	}
	// Honor the optional proxy used for Azure token operations, mirroring the controller's
	// GetClientAssertionCredentialOptions.
	if azureProxyURL := os.Getenv("AI_GATEWAY_AZURE_PROXY_URL"); azureProxyURL != "" {
		if proxyURL, err := url.Parse(azureProxyURL); err == nil {
			opts.ClientOptions = azcore.ClientOptions{
				Transport: &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}},
			}
		}
	}

	cred, err := azidentity.NewWorkloadIdentityCredential(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Workload Identity credential: %w", err)
	}
	return newAzureHandlerWithCredential(cred), nil
}

// newAzureHandlerWithCredential builds an ambient-mode azureHandler around the given credential.
// It exists as a seam so tests can inject a fake azcore.TokenCredential.
func newAzureHandlerWithCredential(cred azcore.TokenCredential) *azureHandler {
	return &azureHandler{cred: cred}
}

// Do implements [Handler.Do].
//
// It sets the "Authorization" header to a bearer token. When a static access token is configured it is
// used directly; otherwise an Entra token is minted from the ambient Azure Workload Identity credential.
func (a *azureHandler) Do(ctx context.Context, requestHeaders map[string]string, _ []byte) ([]internalapi.Header, error) {
	accessToken := a.azureAccessToken
	if a.cred != nil {
		token, err := a.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{azureCognitiveServicesScope}})
		if err != nil {
			return nil, fmt.Errorf("failed to get Azure access token: %w", err)
		}
		accessToken = token.Token
	}
	requestHeaders["Authorization"] = fmt.Sprintf("Bearer %s", accessToken)
	return []internalapi.Header{{"Authorization", fmt.Sprintf("Bearer %s", accessToken)}}, nil
}
