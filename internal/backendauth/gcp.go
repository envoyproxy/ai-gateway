// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"fmt"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

type gcpHandler struct {
	gcpAccessToken string // The GCP access token used for authentication.
	region         string // The GCP region to use for requests.
	projectName    string // The GCP project to use for requests.
}

func newGCPHandler(gcpAuth *filterapi.GCPAuth) (Handler, error) {
	if gcpAuth == nil {
		return nil, fmt.Errorf("GCP auth configuration cannot be nil")
	}

	if gcpAuth.AccessToken == "" {
		return nil, fmt.Errorf("GCP access token cannot be empty")
	}

	return &gcpHandler{
		gcpAccessToken: gcpAuth.AccessToken,
		region:         gcpAuth.Region,
		projectName:    gcpAuth.ProjectName,
	}, nil
}

// Do implements [Handler.Do].
//
// This method updates the request headers to:
//  1. Prepend the GCP API prefix to the ":path" header, constructing the full endpoint URL.
//  2. Add an "Authorization" header with the GCP access token.
//
// The ":path" header is expected to contain the API-specific suffix, which is injected by translator.requestBody.
// The suffix is combined with the generated prefix to form the complete path for the GCP API call.
func (g *gcpHandler) Do(_ context.Context, requestHeaders map[string]string, _ []byte) ([]internalapi.Header, error) {
	// Build the GCP URL prefix using the configured region and project name.
	prefixPath := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s", g.region, g.projectName, g.region)

	// Find and update the ":path" header by prepending the prefix.
	path := requestHeaders[":path"]
	// Update the raw byte value if present.
	newPath := fmt.Sprintf("%s/%s", prefixPath, path)

	if path == "" {
		return nil, fmt.Errorf("missing ':path' header in the request")
	}

	// Add the Authorization header with the GCP access token.
	requestHeaders[":path"] = newPath
	requestHeaders["Authorization"] = fmt.Sprintf("Bearer %s", g.gcpAccessToken)
	return []internalapi.Header{{":path", newPath}, {"Authorization", fmt.Sprintf("Bearer %s", g.gcpAccessToken)}}, nil
}
