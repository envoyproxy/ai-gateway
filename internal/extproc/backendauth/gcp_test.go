// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestNewGCPHandler(t *testing.T) {
	testCases := []struct {
		name        string
		gcpAuth     *filterapi.GCPAuth
		wantHandler *gcpHandler
		wantError   bool
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
			wantError: false,
		},
		{
			name:        "nil config",
			gcpAuth:     nil,
			wantHandler: nil,
			wantError:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := newGCPHandler(tc.gcpAuth)
			if tc.wantError {
				require.Error(t, err)
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
		headerMut        *extprocv3.HeaderMutation
		bodyMut          *extprocv3.BodyMutation
		wantPathValue    string
		wantPathRawValue []byte
		wantErrorMsg     string
	}{
		{
			name:    "basic headers update with string value",
			handler: handler,
			headerMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:   ":path",
							Value: "publishers/google/models/gemini-pro:generateContent",
						},
					},
				},
			},
			bodyMut:       &extprocv3.BodyMutation{},
			wantPathValue: "https://us-central1-aiplatform.googleapis.com/v1/projects/test-project/locations/us-central1/publishers/google/models/gemini-pro:generateContent",
		},
		{
			name:    "basic headers update with raw value",
			handler: handler,
			headerMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:      ":path",
							RawValue: []byte("publishers/google/models/gemini-pro:generateContent"),
						},
					},
				},
			},
			bodyMut:          &extprocv3.BodyMutation{},
			wantPathRawValue: []byte("https://us-central1-aiplatform.googleapis.com/v1/projects/test-project/locations/us-central1/publishers/google/models/gemini-pro:generateContent"),
		},
		{
			name:    "no path header",
			handler: handler,
			headerMut: &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:   "Content-Type",
							Value: "application/json",
						},
					},
				},
			},
			bodyMut:      &extprocv3.BodyMutation{},
			wantErrorMsg: "missing ':path' header in the request",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			err := tc.handler.Do(ctx, nil, tc.headerMut, tc.bodyMut)

			if tc.wantErrorMsg != "" {
				require.ErrorContains(t, err, tc.wantErrorMsg, "Expected error message not found")
			} else {
				require.NoError(t, err)

				// Check Authorization header
				authHeaderFound := false
				expectedAuthHeader := fmt.Sprintf("Bearer %s", tc.handler.gcpAccessToken)

				// Check path header if expected
				pathHeaderUpdated := false

				for _, header := range tc.headerMut.SetHeaders {
					if header.Header.Key == "Authorization" {
						authHeaderFound = true
						require.Equal(t, []byte(expectedAuthHeader), header.Header.RawValue)
					}

					if header.Header.Key == ":path" {
						pathHeaderUpdated = true
						if len(tc.wantPathValue) > 0 {
							require.Equal(t, tc.wantPathValue, header.Header.Value)
						}
						if len(tc.wantPathRawValue) > 0 {
							require.True(t, bytes.Equal(tc.wantPathRawValue, header.Header.RawValue))
						}
					}
				}

				// Authorization header should always be added
				require.True(t, authHeaderFound, "Authorization header not found")

				// Only check path header if we had expectations for it
				if len(tc.wantPathValue) > 0 || len(tc.wantPathRawValue) > 0 {
					require.True(t, pathHeaderUpdated, "Path header not updated as expected")
				}
			}
		})
	}
}
