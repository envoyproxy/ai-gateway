// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package rotators

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller/tokenprovider"
)

const (
	dummyProjectName   = "dummy-project-name"   // #nosec G101
	dummyProjectRegion = "dummy-project-region" // #nosec G101
	dummyJWTToken      = "dummy-oidc-token"     // #nosec G101
	dummySTSToken      = "dummy-sts-token"      // #nosec G101
	oldGCPAccessToken  = "old-gcp-access-token" // #nosec G101
	newGCPAccessToken  = "new-gcp-access-token" // #nosec G101
)

func TestGCPTokenRotator_Rotate(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})

	// now := time.Now()
	now := time.Date(2021, 1, 1, 12, 0, 0, 0, time.UTC) // Fixed time for testing
	oneHourBeforeNow := now.Add(-1 * time.Hour)
	twoHourAfterNow := now.Add(2 * time.Hour)

	oldSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GetBSPSecretName("test-policy"),
			Namespace: "default",
			Annotations: map[string]string{
				ExpirationTimeAnnotationKey: oneHourBeforeNow.Format(time.RFC3339),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			GCPProjectNameKey: []byte(dummyProjectName),
			GCPRegionKey:      []byte(dummyProjectRegion),
			GCPAccessTokenKey: []byte(oldGCPAccessToken),
		},
	}

	renewedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GetBSPSecretName("test-policy"),
			Namespace: "default",
			Annotations: map[string]string{
				ExpirationTimeAnnotationKey: twoHourAfterNow.Format(time.RFC3339),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			GCPProjectNameKey: []byte(dummyProjectName),
			GCPRegionKey:      []byte(dummyProjectRegion),
			GCPAccessTokenKey: []byte(newGCPAccessToken),
		},
	}

	renewedSecretWithoutSAImpersonation := renewedSecret.DeepCopy()
	renewedSecretWithoutSAImpersonation.Data[GCPAccessTokenKey] = []byte(dummySTSToken)

	tests := []struct {
		name                            string
		kubeInitObjects                 []runtime.Object
		saTokenFunc                     serviceAccountTokenGenerator
		stsTokenFunc                    stsTokenGenerator
		skipServiceAccountImpersonation bool
		expectedSecret                  *corev1.Secret
		expectErrorMsg                  string
		clientCreateFn                  func(t *testing.T) client.Client
	}{
		{
			name:            "failed to get sts token",
			kubeInitObjects: []runtime.Object{oldSecret},
			stsTokenFunc: func(_ context.Context, _ string, _ aigv1a1.GCPWorkLoadIdentityFederationConfig, _ ...option.ClientOption) (*tokenprovider.TokenExpiry, error) {
				return nil, fmt.Errorf("fake network failure")
			},
			expectErrorMsg: "failed to exchange JWT for STS token (project: test-project-id, pool: test-pool-name): fake network failure",
		},
		{
			name:            "failed to get OIDC token",
			kubeInitObjects: []runtime.Object{oldSecret},
			expectErrorMsg:  "failed to obtain OIDC token: oidc provider error",
		},
		{
			name:            "failed to impersonate service account",
			kubeInitObjects: []runtime.Object{oldSecret},
			saTokenFunc: func(_ context.Context, _ string, _ aigv1a1.GCPServiceAccountImpersonationConfig, _ ...option.ClientOption) (*tokenprovider.TokenExpiry, error) {
				return nil, fmt.Errorf("fake network failure")
			},
			expectErrorMsg: "failed to impersonate service account test-service-account@test-service-account-project-name.iam.gserviceaccount.com: fake network failure",
		},
		{
			name:            "secret with old does not exist",
			kubeInitObjects: nil,
		},
		{
			name:            "secret with old token exists",
			kubeInitObjects: []runtime.Object{oldSecret},
			expectErrorMsg:  "",
		},
		{
			name:                            "without service account impersonation",
			kubeInitObjects:                 []runtime.Object{oldSecret},
			skipServiceAccountImpersonation: true,
			expectedSecret:                  renewedSecretWithoutSAImpersonation,
			expectErrorMsg:                  "",
		},
		{
			name:            "create error",
			kubeInitObjects: nil,
			clientCreateFn: func(t *testing.T) client.Client {
				// Create a fake client that returns an error on Create
				fc := fake.NewFakeClient()
				// Wrap the fake client to return an error on Create
				return &errorOnCreateClient{
					Client: fc,
					t:      t,
				}
			},
			expectErrorMsg: "create error",
		},
		{
			name:            "update error",
			kubeInitObjects: []runtime.Object{oldSecret},
			clientCreateFn: func(t *testing.T) client.Client {
				// Create a fake client that returns an error on Update
				fc := fake.NewFakeClient()
				// Wrap the fake client to return an error on Update
				return &errorOnUpdateClient{
					Client: fc,
					t:      t,
				}
			},
			expectErrorMsg: "update error",
		},
		{
			name:            "secret lookup error (non-NotFound)",
			kubeInitObjects: []runtime.Object{},
			clientCreateFn: func(t *testing.T) client.Client {
				// Create a fake client that returns an error on Get operations
				fc := fake.NewFakeClient()
				// Wrap the fake client to return an error on Get
				return &errorOnGetClient{
					Client: fc,
					t:      t,
				}
			},
			expectErrorMsg: "failed to get secret: lookup error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fakeClient client.Client

			// Use custom client if provided, otherwise use default fake client
			if tt.clientCreateFn != nil {
				fakeClient = tt.clientCreateFn(t)
				// Add initial objects to the custom client if needed
				for _, obj := range tt.kubeInitObjects {
					if clientObj, ok := obj.(client.Object); ok {
						err := fakeClient.Create(context.Background(), clientObj)
						require.NoError(t, err)
					}
				}
			} else {
				fakeClient = fake.NewFakeClient(tt.kubeInitObjects...)
			}

			// If no saTokenFunc or stsTokenFunc is provided, use the default mock functions.
			if tt.saTokenFunc == nil {
				tt.saTokenFunc = func(_ context.Context, _ string, _ aigv1a1.GCPServiceAccountImpersonationConfig, _ ...option.ClientOption) (*tokenprovider.TokenExpiry, error) {
					return &tokenprovider.TokenExpiry{Token: newGCPAccessToken, ExpiresAt: twoHourAfterNow}, nil
				}
			}
			if tt.stsTokenFunc == nil {
				tt.stsTokenFunc = func(_ context.Context, _ string, _ aigv1a1.GCPWorkLoadIdentityFederationConfig, _ ...option.ClientOption) (*tokenprovider.TokenExpiry, error) {
					return &tokenprovider.TokenExpiry{Token: dummySTSToken, ExpiresAt: twoHourAfterNow}, nil
				}
			}
			gcpCredentials := aigv1a1.BackendSecurityPolicyGCPCredentials{
				ProjectName: dummyProjectName,
				Region:      dummyProjectRegion,
				WorkLoadIdentityFederationConfig: aigv1a1.GCPWorkLoadIdentityFederationConfig{
					ProjectID:                "test-project-id",
					WorkloadIdentityProvider: aigv1a1.GCPWorkloadIdentityProvider{},
					WorkloadIdentityPoolName: "test-pool-name",
				},
			}
			if !tt.skipServiceAccountImpersonation {
				gcpCredentials.WorkLoadIdentityFederationConfig.ServiceAccountImpersonation = &aigv1a1.GCPServiceAccountImpersonationConfig{
					ServiceAccountName:        "test-service-account",
					ServiceAccountProjectName: "test-service-account-project-name",
				}
			}

			rotator := &gcpOIDCTokenRotator{
				client:                         fakeClient,
				logger:                         logr.Logger{},
				gcpCredentials:                 gcpCredentials,
				backendSecurityPolicyName:      "test-policy",
				backendSecurityPolicyNamespace: "default",
				preRotationWindow:              5 * time.Minute,
				saTokenFunc:                    tt.saTokenFunc,
				stsTokenFunc:                   tt.stsTokenFunc,
			}

			// Set up OIDC provider based on test case
			if tt.name == "failed to get OIDC token" {
				rotator.oidcProvider = tokenprovider.NewMockTokenProvider("", time.Time{}, fmt.Errorf("oidc provider error"))
			} else {
				rotator.oidcProvider = tokenprovider.NewMockTokenProvider(dummyJWTToken, twoHourAfterNow, nil)
			}

			expiration, err := rotator.Rotate(context.Background())
			switch {
			case tt.expectErrorMsg != "" && err == nil:
				t.Errorf("expected error %q, got nil", tt.expectErrorMsg)
			case tt.expectErrorMsg != "" && err != nil:
				if d := cmp.Diff(tt.expectErrorMsg, err.Error()); d != "" {
					t.Errorf("GCPTokenRotator.Rotate() returned unexpected error (-want +got):\n%s", d)
				}
			case tt.expectErrorMsg == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			default:
				if d := cmp.Diff(twoHourAfterNow, expiration); d != "" {
					t.Errorf("GCPTokenRotator.Rotate() returned unexpected expiration time (-want +got):\n%s", d)
				}

				var actualSec corev1.Secret
				if err = fakeClient.Get(context.Background(), client.ObjectKey{
					Namespace: renewedSecret.Namespace,
					Name:      renewedSecret.Name,
				}, &actualSec); err != nil {
					t.Errorf("Failed to get expected secret from client: %v", err)
				}

				if tt.expectedSecret == nil {
					tt.expectedSecret = renewedSecret
				}
				if d := cmp.Diff(tt.expectedSecret, &actualSec, cmpopts.IgnoreFields(corev1.Secret{}, "ResourceVersion")); d != "" {
					t.Errorf("GCPTokenRotator.Rotate() returned unexpected secret (-want +got):\n%s", d)
				}
			}
		})
	}
}

func TestGCPTokenRotator_GetPreRotationTime(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})

	now := time.Now()

	tests := []struct {
		name           string
		secret         *corev1.Secret
		expectedTime   time.Time
		expectedError  bool
		clientCreateFn func(t *testing.T) client.Client
	}{
		{
			name: "secret annotation missing",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      GetBSPSecretName("test-policy"),
					Namespace: "default",
				},
				Data: map[string][]byte{
					GCPProjectNameKey: []byte(dummyProjectName),
					GCPRegionKey:      []byte(dummyProjectRegion),
					GCPAccessTokenKey: []byte(oldGCPAccessToken),
				},
			},
			expectedTime:  time.Time{},
			expectedError: true,
		},
		{
			name: "rotation time before expiration time",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      GetBSPSecretName("test-policy"),
					Namespace: "default",
					Annotations: map[string]string{
						ExpirationTimeAnnotationKey: now.Add(2 * time.Hour).Format(time.RFC3339),
					},
				},
				Data: map[string][]byte{
					GCPProjectNameKey: []byte(dummyProjectName),
					GCPRegionKey:      []byte(dummyProjectRegion),
					GCPAccessTokenKey: []byte(oldGCPAccessToken),
				},
			},
			expectedTime:  now.Add(2 * time.Hour),
			expectedError: false,
		},
		{
			name:          "lookup secret error (non-NotFound)",
			expectedTime:  time.Time{},
			expectedError: true,
			clientCreateFn: func(t *testing.T) client.Client {
				return &errorOnGetClient{
					Client: fake.NewFakeClient(),
					t:      t,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var testClient client.Client

			// Use custom client if provided, otherwise use default fake client
			if tt.clientCreateFn != nil {
				testClient = tt.clientCreateFn(t)
			} else {
				testClient = fake.NewClientBuilder().WithScheme(scheme).Build()
				err := testClient.Create(context.Background(), tt.secret)
				require.NoError(t, err)
			}

			// Create rotator with the test client
			testRotator := &gcpOIDCTokenRotator{
				client:                         testClient,
				preRotationWindow:              5 * time.Minute,
				backendSecurityPolicyName:      "test-policy",
				backendSecurityPolicyNamespace: "default",
				gcpCredentials:                 aigv1a1.BackendSecurityPolicyGCPCredentials{},
			}

			got, err := testRotator.GetPreRotationTime(context.Background())
			if (err != nil) != tt.expectedError {
				t.Errorf("GCPTokenRotator.GetPreRotationTime() error = %v, expectedError %v", err, tt.expectedError)
				return
			}
			if !tt.expectedTime.IsZero() && got.Compare(tt.expectedTime) >= 0 {
				t.Errorf("GCPTokenRotator.GetPreRotationTime() = %v, expected %v", got, tt.expectedTime)
			}
		})
	}
}

func TestGCPTokenRotator_IsExpired(t *testing.T) {
	fakeKubeClient := fake.NewFakeClient()
	rotator := &gcpOIDCTokenRotator{
		client: fakeKubeClient,
	}
	now := time.Now()
	tests := []struct {
		name       string
		expiration time.Time
		expect     bool
	}{
		{
			name:       "not expired",
			expiration: now.Add(1 * time.Hour),
			expect:     false,
		},
		{
			name:       "expired",
			expiration: now.Add(-1 * time.Hour),
			expect:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rotator.IsExpired(tt.expiration); got != tt.expect {
				t.Errorf("GCPTokenRotator.IsExpired() = %v, expect %v", got, tt.expect)
			}
		})
	}
}

// TestExchangeJWTForSTSToken tests the exchangeJWTForSTSToken function.
func TestExchangeJWTForSTSToken(t *testing.T) {
	// Test cases
	tests := []struct {
		name            string
		jwtToken        string
		wifConfig       aigv1a1.GCPWorkLoadIdentityFederationConfig
		mockServer      func() *httptest.Server
		expectedError   bool
		expectedToken   string
		expectedExpires time.Duration
	}{
		{
			name:     "successful token exchange",
			jwtToken: "test-jwt-token",
			wifConfig: aigv1a1.GCPWorkLoadIdentityFederationConfig{
				ProjectID:                "test-project",
				WorkloadIdentityPoolName: "test-pool",
				WorkloadIdentityProvider: aigv1a1.GCPWorkloadIdentityProvider{
					Name: "test-provider",
				},
			},
			mockServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Verify request is to token endpoint
					if r.URL.Path != "/v1/token" {
						http.Error(w, "Not found", http.StatusNotFound)
						return
					}

					// Verify it's a POST request
					if r.Method != http.MethodPost {
						http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
						return
					}

					// Return successful token response
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{
						"access_token": "test-sts-token",
						"expires_in": 3600,
						"token_type": "Bearer"
					}`)
				}))
			},
			expectedError:   false,
			expectedToken:   "test-sts-token",
			expectedExpires: time.Hour,
		},
		{
			name:     "token exchange error",
			jwtToken: "invalid-jwt-token",
			wifConfig: aigv1a1.GCPWorkLoadIdentityFederationConfig{
				ProjectID:                "test-project",
				WorkloadIdentityPoolName: "test-pool",
				WorkloadIdentityProvider: aigv1a1.GCPWorkloadIdentityProvider{
					Name: "test-provider",
				},
			},
			mockServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					// Return error response
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, err := fmt.Fprintf(w, `{
											"error": "invalid_token",
											"error_description": "The provided JWT is invalid"
										}`)
					if err != nil {
						return
					}
				}))
			},
			expectedError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock server
			server := tc.mockServer()
			defer server.Close()

			// Create custom HTTP client option that points to our test server
			ctx := context.Background()
			// Call the function being tested
			tokenExpiry, err := exchangeJWTForSTSToken(ctx, tc.jwtToken, tc.wifConfig, option.WithEndpoint(server.URL))

			// Check error conditions
			if tc.expectedError {
				require.Error(t, err)
				require.Nil(t, tokenExpiry)
				return
			}

			// Validate successful results
			require.NoError(t, err)
			require.NotNil(t, tokenExpiry)
			require.Equal(t, tc.expectedToken, tokenExpiry.Token)

			// Check expiration time is in the expected range
			// Since the function uses time.Now(), we can't assert the exact time
			// but we can check that it's within an acceptable range
			expectedExpiryTime := time.Now().Add(tc.expectedExpires)
			timeDiff := tokenExpiry.ExpiresAt.Sub(expectedExpiryTime)
			require.Less(t, timeDiff.Abs(), time.Second*5, "Expiry time should be close to expected value")
		})
	}
}

func TestExchangeJWTForSTSToken_WithoutAuthOption(t *testing.T) {
	// Create a mock server that validates the request has no authentication
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for absence of authentication headers to validate WithoutAuthentication is working
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			http.Error(w, "Authorization header should not be present", http.StatusBadRequest)
			return
		}

		// Return a successful response
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"access_token": "test-sts-token",
			"expires_in": 3600,
			"token_type": "Bearer"
		}`)
	}))
	defer server.Close()

	// Define test configuration
	jwtToken := "test-jwt-token" // #nosec G101
	wifConfig := aigv1a1.GCPWorkLoadIdentityFederationConfig{
		ProjectID:                "test-project",
		WorkloadIdentityPoolName: "test-pool",
		WorkloadIdentityProvider: aigv1a1.GCPWorkloadIdentityProvider{
			Name: "test-provider",
		},
	}

	// Call the function with the server URL as the endpoint
	ctx := context.Background()
	tokenExpiry, err := exchangeJWTForSTSToken(ctx, jwtToken, wifConfig, option.WithEndpoint(server.URL))

	// Verify the results
	require.NoError(t, err)
	require.NotNil(t, tokenExpiry)
	require.Equal(t, "test-sts-token", tokenExpiry.Token)

	// Verify the expiration time is about an hour from now
	expectedExpiryTime := time.Now().Add(time.Hour)
	timeDiff := tokenExpiry.ExpiresAt.Sub(expectedExpiryTime)
	require.Less(t, timeDiff.Abs(), time.Second*5, "Expiry time should be close to expected value")
}

// roundTripperFunc implements http.RoundTripper interface for custom response handling
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestImpersonateServiceAccount tests the impersonateServiceAccount function.
func TestImpersonateServiceAccount(t *testing.T) {
	// Test cases
	tests := []struct {
		name     string
		stsToken string
		saConfig aigv1a1.GCPServiceAccountImpersonationConfig
		// impersonateServiceAccount is hardcoded to call google api endpoint and ignore mockEndpoints set via opts.
		// thus we mock the underlying HTTPRoundTripper to simulate mock responses.
		mockResponse  func(req *http.Request) (*http.Response, error)
		expectedError bool
		expectedToken string
	}{
		{
			name:     "successful service account impersonation",
			stsToken: "test-sts-token",
			saConfig: aigv1a1.GCPServiceAccountImpersonationConfig{
				ServiceAccountName:        "test-service-account",
				ServiceAccountProjectName: "test-project",
			},
			mockResponse: func(req *http.Request) (*http.Response, error) {
				// Verify it's a POST request
				if req.Method != http.MethodPost {
					return &http.Response{
						StatusCode: http.StatusMethodNotAllowed,
						Body:       http.NoBody,
					}, nil
				}

				// Verify request is to the IAM credentials API and is asking to generate access token
				if !strings.Contains(req.URL.String(), "iamcredentials.googleapis.com") ||
					!strings.Contains(req.URL.Path, "generateAccessToken") {
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Body:       http.NoBody,
					}, nil
				}

				// Return successful token response
				expiryTime := time.Now().Add(time.Hour).Format(time.RFC3339)
				respBody := fmt.Sprintf(`{
					"accessToken": "impersonated-sa-token",
					"expireTime": "%s"
				}`, expiryTime)

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(respBody)),
					Header:     map[string][]string{"Content-Type": {"application/json"}},
				}, nil
			},
			expectedError: false,
			expectedToken: "impersonated-sa-token",
		},
		{
			name:     "impersonation error",
			stsToken: "invalid-sts-token",
			saConfig: aigv1a1.GCPServiceAccountImpersonationConfig{
				ServiceAccountName:        "test-service-account",
				ServiceAccountProjectName: "test-project",
			},
			mockResponse: func(_ *http.Request) (*http.Response, error) {
				// Return error response
				respBody := `{
					"error": {
						"code": 401,
						"message": "Request had invalid authentication credentials",
						"status": "UNAUTHENTICATED"
					}
				}`

				return &http.Response{
					StatusCode: http.StatusUnauthorized,
					Body:       io.NopCloser(strings.NewReader(respBody)),
					Header:     map[string][]string{"Content-Type": {"application/json"}},
				}, nil
			},
			expectedError: true,
		},
		{
			name:     "credentials creation error",
			stsToken: "test-sts-token",
			saConfig: aigv1a1.GCPServiceAccountImpersonationConfig{
				ServiceAccountName:        "test-service-account",
				ServiceAccountProjectName: "test-project",
			},
			mockResponse: func(_ *http.Request) (*http.Response, error) {
				// Simulate network error during credential creation
				return nil, fmt.Errorf("network error during credential creation")
			},
			expectedError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			// Create a mock HTTP client that intercepts requests
			mockTransport := roundTripperFunc(tc.mockResponse)
			mockHTTPClient := &http.Client{Transport: mockTransport}

			// Call the function being tested with our mock HTTP client
			tokenExpiry, err := impersonateServiceAccount(ctx, tc.stsToken, tc.saConfig, option.WithHTTPClient(mockHTTPClient))

			// Check error conditions
			if tc.expectedError {
				require.Error(t, err)
				require.Nil(t, tokenExpiry)
				return
			}

			// Validate successful results
			require.NoError(t, err)
			require.NotNil(t, tokenExpiry)
			require.Equal(t, tc.expectedToken, tokenExpiry.Token)

			// Check that expiration time is reasonably set (should be around 1 hour from now)
			expectedExpiryTimeApprox := time.Now().Add(time.Hour)
			timeDiff := tokenExpiry.ExpiresAt.Sub(expectedExpiryTimeApprox)
			require.Less(t, timeDiff.Abs(), time.Minute*5, "Expiry time should be close to expected value")
		})
	}
}

// TestNewGCPOIDCTokenRotator tests the NewGCPOIDCTokenRotator constructor function.
func TestNewGCPOIDCTokenRotator(t *testing.T) {
	logger := logr.Logger{}
	preRotationWindow := 30 * time.Minute

	// Mock token provider creation by directly creating test cases with valid/invalid params
	// without monkey patching the NewOidcTokenProvider function

	// Define OIDC values based on the real Envoy Gateway API types
	validOIDCConfig := aigv1a1.BackendSecurityPolicyOIDC{
		OIDC: egv1a1.OIDC{
			ClientID: "client-id",
			Scopes:   []string{"scope1", "scope2"},
		},
	}

	tests := []struct {
		name          string
		bsp           aigv1a1.BackendSecurityPolicy
		expectedError string
	}{
		{
			name: "nil GCP credentials",
			bsp: aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-policy",
					Namespace: "default",
				},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					GCPCredentials: nil,
				},
			},
			expectedError: "GCP credentials are not configured in BackendSecurityPolicy default/test-policy",
		},
		{
			name: "success",
			bsp: aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-success-policy",
					Namespace: "default",
				},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					GCPCredentials: &aigv1a1.BackendSecurityPolicyGCPCredentials{
						ProjectName: "test-project",
						Region:      "us-central1",
						WorkLoadIdentityFederationConfig: aigv1a1.GCPWorkLoadIdentityFederationConfig{
							ProjectID:                "test-project-id",
							WorkloadIdentityPoolName: "test-pool-name",
							WorkloadIdentityProvider: aigv1a1.GCPWorkloadIdentityProvider{
								Name:         "test-provider",
								OIDCProvider: validOIDCConfig,
							},
							ServiceAccountImpersonation: &aigv1a1.GCPServiceAccountImpersonationConfig{
								ServiceAccountName:        "test-service-account",
								ServiceAccountProjectName: "test-project",
							},
						},
					},
				},
			},
			expectedError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client for each test
			scheme := runtime.NewScheme()
			scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
			fakeClient := fake.NewFakeClient()

			var rotator Rotator
			var err error

			mockTokenProvider := tokenprovider.NewMockTokenProvider("mock-jwt-token", time.Now().Add(time.Hour), nil)
			rotator, err = NewGCPOIDCTokenRotator(fakeClient, logger, tt.bsp, preRotationWindow, mockTokenProvider)

			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
				require.Nil(t, rotator)
			} else {
				require.NotNil(t, rotator)

				// Verify rotator is properly initialized
				gcpRotator, ok := rotator.(*gcpOIDCTokenRotator)
				require.True(t, ok, "Expected a gcpOIDCTokenRotator instance")

				// Instead of comparing the entire struct with cmp.Diff, which has issues with unexported fields,
				// verify individual fields that we care about
				require.Equal(t, tt.bsp.Name, gcpRotator.backendSecurityPolicyName)
				require.Equal(t, tt.bsp.Namespace, gcpRotator.backendSecurityPolicyNamespace)
				require.Equal(t, preRotationWindow, gcpRotator.preRotationWindow)
				require.NotNil(t, gcpRotator.oidcProvider)
				require.NotNil(t, gcpRotator.client)
				require.NotNil(t, gcpRotator.saTokenFunc)
				require.NotNil(t, gcpRotator.stsTokenFunc)

				// Verify that the GCP credentials were properly copied
				if tt.bsp.Spec.GCPCredentials != nil {
					require.Equal(t, *tt.bsp.Spec.GCPCredentials, gcpRotator.gcpCredentials)
				}
			}
		})
	}
}

// errorOnCreateClient is a client that returns an error on Create
type errorOnCreateClient struct {
	client.Client
	t *testing.T
}

func (c *errorOnCreateClient) Create(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
	return fmt.Errorf("create error")
}

// errorOnUpdateClient is a client that returns an error on Update
type errorOnUpdateClient struct {
	client.Client
	t *testing.T
}

func (c *errorOnUpdateClient) Create(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
	return nil // Allow create to succeed
}

func (c *errorOnUpdateClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	// Cast to Secret
	if secret, ok := obj.(*corev1.Secret); ok {
		secret.Name = key.Name
		secret.Namespace = key.Namespace
		secret.Data = map[string][]byte{
			GCPProjectNameKey: []byte(dummyProjectName),
			GCPRegionKey:      []byte(dummyProjectRegion),
			GCPAccessTokenKey: []byte(oldGCPAccessToken),
		}
		secret.Annotations = map[string]string{
			ExpirationTimeAnnotationKey: time.Now().Format(time.RFC3339),
		}
		return nil
	}
	return nil
}

func (c *errorOnUpdateClient) Update(_ context.Context, _ client.Object, _ ...client.UpdateOption) error {
	return fmt.Errorf("update error")
}

// errorOnGetClient is a client that returns an error on Get (for testing lookup failures)
type errorOnGetClient struct {
	client.Client
	t *testing.T
}

func (c *errorOnGetClient) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	return fmt.Errorf("lookup error")
}
