// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

func TestNewGCPHandler(t *testing.T) {
	testCases := []struct {
		name         string
		gcpAuth      *filterapi.GCPAuth
		wantHandler  *gcpHandler
		wantErrorMsg string
	}{
		{
			name: "valid config",
			gcpAuth: &filterapi.GCPAuth{
				AccessToken: "test-token",
				Region:      "us-central1",
				ProjectName: "test-project",
			},
			wantHandler: &gcpHandler{
				gcpAccessToken: "test-token",
				region:         "us-central1",
				projectName:    "test-project",
			},
		},
		{
			name: "missing auth token",
			gcpAuth: &filterapi.GCPAuth{
				AccessToken: "",
				Region:      "us-central1",
				ProjectName: "test-project",
			},
			wantHandler:  nil,
			wantErrorMsg: "GCP access token cannot be empty",
		},
		{
			name:         "nil config",
			gcpAuth:      nil,
			wantHandler:  nil,
			wantErrorMsg: "GCP auth configuration cannot be nil",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := newGCPHandler(tc.gcpAuth)
			if tc.wantErrorMsg != "" {
				require.ErrorContains(t, err, tc.wantErrorMsg)
			} else {
				require.NoError(t, err)
				require.NotNil(t, handler)

				if d := cmp.Diff(tc.wantHandler, handler, cmp.AllowUnexported(gcpHandler{})); d != "" {
					t.Errorf("Handler mismatch (-want +got):\n%s", d)
				}
			}
		})
	}
}

func TestGCPHandler_Do(t *testing.T) {
	handler := &gcpHandler{
		gcpAccessToken: "test-token",
		region:         "us-central1",
		projectName:    "test-project",
	}
	testCases := []struct {
		name             string
		handler          *gcpHandler
		requestHeaders   map[string]string
		wantPathValue    string
		wantPathRawValue []byte
	}{
		{
			name:    "basic headers update",
			handler: handler,
			requestHeaders: map[string]string{
				":path": "publishers/google/models/gemini-pro:generateContent",
			},
			wantPathValue: "https://us-central1-aiplatform.googleapis.com/v1/projects/test-project/locations/us-central1/publishers/google/models/gemini-pro:generateContent",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			hdrs, err := tc.handler.Do(ctx, tc.requestHeaders, nil)
			require.NoError(t, err)

			expectedAuthHeader := fmt.Sprintf("Bearer %s", tc.handler.gcpAccessToken)

			hdrsMap := stringPairsToMap(hdrs)
			authValue, ok := hdrsMap["Authorization"]
			require.True(t, ok, "Authorization header not found in returned headers")
			require.Equal(t, expectedAuthHeader, authValue, "Authorization header value mismatch")

			pathValue, ok := hdrsMap[":path"]
			require.True(t, ok, ":path header not found in returned headers")
			require.Equal(t, tc.wantPathValue, pathValue, ":path header value mismatch")
		})
	}
}
