package controller

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

const testNamespace = "test-namespace"
const mockOIDCIssuer = "https://test-oidc-server"

// var scheme = runtime.NewScheme()

func init() {
	// Initialize the logger for all tests
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
}

// mockHTTPClient implements http.RoundTripper for testing
type mockHTTPClient struct {
	response *http.Response
	err      error
}

func (m *mockHTTPClient) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.err != nil {
		return nil, m.err
	}

	if m.response != nil {
		// Read the original body
		body, err := io.ReadAll(m.response.Body)
		if err != nil {
			return nil, err
		}
		m.response.Body.Close()

		// Create a new response with the same data
		resp := &http.Response{
			StatusCode: m.response.StatusCode,
			Header:     m.response.Header.Clone(),
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    req,
		}

		// Reset the original response body
		m.response.Body = io.NopCloser(bytes.NewReader(body))

		return resp, nil
	}

	return &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader("not found")),
		Request:    req,
	}, nil
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return m.RoundTrip(req)
}

// assertDurationWithin checks if a duration is within an acceptable range of the expected value
func assertDurationWithin(t *testing.T, expected, actual time.Duration, margin time.Duration) {
	t.Helper()
	diff := expected - actual
	if diff < 0 {
		diff = -diff
	}
	if diff > margin {
		t.Errorf("Duration %v not within %v of expected %v", actual, margin, expected)
	}
}

// mockSTSClient implements STSClient for testing
type mockSTSClient struct {
	assumeRoleOutput *sts.AssumeRoleWithWebIdentityOutput
	assumeRoleError  error
	region           string
}

func (m *mockSTSClient) AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	if m.assumeRoleError != nil {
		return nil, m.assumeRoleError
	}
	if m.assumeRoleOutput != nil {
		return m.assumeRoleOutput, nil
	}
	// Default successful response if none provided
	expiration := time.Now().Add(1 * time.Hour)
	return &sts.AssumeRoleWithWebIdentityOutput{
		Credentials: &ststypes.Credentials{
			AccessKeyId:     aws.String("AKIATEST"),
			SecretAccessKey: aws.String("test-secret"),
			SessionToken:    aws.String("test-session"),
			Expiration:      &expiration,
		},
	}, nil
}

// mockSTSClientFactory creates mock STS clients for testing
type mockSTSClientFactory struct {
	clients map[string]*mockSTSClient
}

func newMockSTSClientFactory() *mockSTSClientFactory {
	return &mockSTSClientFactory{
		clients: make(map[string]*mockSTSClient),
	}
}

func (f *mockSTSClientFactory) getClient(region string) *mockSTSClient {
	if client, ok := f.clients[region]; ok {
		return client
	}
	client := &mockSTSClient{region: region}
	f.clients[region] = client
	return client
}

func TestAWSCredentialsRotator(t *testing.T) {
	logger := zap.New()
	require.NoError(t, aigv1a1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	// Mock successful OIDC token response
	successfulOIDCResponse := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(`{
			"access_token": "test-access-token",
			"id_token": "eyJ0eXAiOiJKV1QiLCJhbGciOiJSUzI1NiIsImtpZCI6InRlc3Qta2V5In0.eyJpc3MiOiJodHRwczovL3Rlc3Qtb2lkYy1zZXJ2ZXIiLCJzdWIiOiJ0ZXN0LXVzZXIiLCJhdWQiOiJjbGllbnQtaWQiLCJleHAiOjE3MzgzNzUzNjEsImlhdCI6MTczODM3MTc2MX0.test-signature",
			"token_type": "Bearer",
			"expires_in": 3600
		}`)),
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
	}

	// Mock failed OIDC token response
	failedOIDCResponse := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Body: io.NopCloser(strings.NewReader(`{
			"error": "invalid_client",
			"error_description": "Client authentication failed"
		}`)),
		Header: http.Header{
			"Content-Type":     []string{"application/json"},
			"WWW-Authenticate": []string{`Bearer error="invalid_client", error_description="Client authentication failed"`},
		},
	}

	tests := []struct {
		name                string
		existingCredentials string
		profile             string
		rotationConfig      *aigv1a1.AWSCredentialsRotationConfig
		oidcConfig          *aigv1a1.AWSOIDCExchangeToken
		clientSecret        *corev1.Secret
		createKeyOutput     *iam.CreateAccessKeyOutput
		createKeyError      error
		deleteKeyError      error
		assumeRoleOutput    *sts.AssumeRoleWithWebIdentityOutput
		assumeRoleError     error
		httpResponse        *http.Response
		httpError           error
		expectError         bool
		expectRequeue       bool
		expectedRequeue     time.Duration
		expectedErrorMsg    string
		setupPolicy         func(*aigv1a1.BackendSecurityPolicy)
		modifySecret        func(*corev1.Secret)
	}{
		{
			name: "successful rotation with OIDC",
			oidcConfig: &aigv1a1.AWSOIDCExchangeToken{
				OIDC: egv1a1.OIDC{
					Provider: egv1a1.OIDCProvider{
						Issuer: mockOIDCIssuer,
					},
					ClientID: "client-id",
					ClientSecret: gwapiv1.SecretObjectReference{
						Name: "client-secret",
					},
				},
				AwsRoleArn: "arn:aws:iam::123456789012:role/test-role",
			},
			rotationConfig: &aigv1a1.AWSCredentialsRotationConfig{
				RotationInterval:  "1h",
				PreRotationWindow: "30m",
			},
			clientSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "client-secret",
					Namespace: testNamespace,
				},
				Data: map[string][]byte{
					"client-secret": []byte("test-secret"),
				},
			},
			httpResponse: successfulOIDCResponse,
			assumeRoleOutput: &sts.AssumeRoleWithWebIdentityOutput{
				Credentials: &ststypes.Credentials{
					AccessKeyId:     aws.String("AKIAOIDC"),
					SecretAccessKey: aws.String("oidcSecret"),
					SessionToken:    aws.String("oidcSession"),
					Expiration:      aws.Time(time.Now().Add(1 * time.Hour)),
				},
			},
			expectRequeue:   true,
			expectedRequeue: 30 * time.Minute,
		},
		{
			name: "failed OIDC token exchange",
			oidcConfig: &aigv1a1.AWSOIDCExchangeToken{
				OIDC: egv1a1.OIDC{
					Provider: egv1a1.OIDCProvider{
						Issuer: mockOIDCIssuer,
					},
					ClientID: "client-id",
					ClientSecret: gwapiv1.SecretObjectReference{
						Name: "client-secret",
					},
				},
				AwsRoleArn: "arn:aws:iam::123456789012:role/test-role",
			},
			clientSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "client-secret",
					Namespace: testNamespace,
				},
				Data: map[string][]byte{
					"client-secret": []byte("test-secret"),
				},
			},
			httpResponse:     failedOIDCResponse,
			expectError:      true,
			expectedErrorMsg: "oauth2: \"invalid_client\" \"Client authentication failed\"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create fake clients
			k8sClient := clientfake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(
					&aigv1a1.BackendSecurityPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-policy",
							Namespace: testNamespace,
						},
						Spec: aigv1a1.BackendSecurityPolicySpec{
							Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
							AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
								Region:            "us-west-2",
								OIDCExchangeToken: tc.oidcConfig,
								RotationConfig:    tc.rotationConfig,
							},
						},
					},
				).
				Build()

			kubeClient := kubefake.NewSimpleClientset()
			if tc.clientSecret != nil {
				_, err := kubeClient.CoreV1().Secrets(testNamespace).Create(context.Background(), tc.clientSecret, metav1.CreateOptions{})
				require.NoError(t, err)
			}

			// Create mock STS client factory
			stsFactory := newMockSTSClientFactory()
			mockSTS := stsFactory.getClient("us-west-2")
			mockSTS.assumeRoleOutput = tc.assumeRoleOutput
			mockSTS.assumeRoleError = tc.assumeRoleError

			// Create the rotator with mock clients
			mockClient := &mockHTTPClient{
				response: tc.httpResponse,
				err:      tc.httpError,
			}
			rotator := &awsCredentialsRotator{
				client:     k8sClient,
				kubeClient: kubeClient,
				logger:     logger,
				iamClient: &mockIAMClient{
					createKeyOutput: tc.createKeyOutput,
					createKeyError:  tc.createKeyError,
					deleteKeyError:  tc.deleteKeyError,
				},
				httpClient: &http.Client{
					Transport: mockClient,
				},
				stsClientCache: map[string]STSClient{
					"us-west-2": mockSTS,
				},
			}

			// Run reconciliation
			result, err := rotator.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-policy",
					Namespace: testNamespace,
				},
			})

			// Verify results
			if tc.expectError {
				assert.Error(t, err)
				if tc.expectedErrorMsg != "" {
					assert.Contains(t, err.Error(), tc.expectedErrorMsg)
				}
			} else {
				assert.NoError(t, err)
				// Verify the secret was created
				var secret corev1.Secret
				err := k8sClient.Get(context.Background(), types.NamespacedName{
					Name:      fmt.Sprintf("%s-oidc-creds", "test-policy"),
					Namespace: testNamespace,
				}, &secret)
				assert.NoError(t, err)
				assert.NotEmpty(t, secret.Data[credentialsKey])
			}

			if tc.expectRequeue {
				// Allow for duration differences up to 2 seconds
				assertDurationWithin(t, tc.expectedRequeue, result.RequeueAfter, 2*time.Second)
			} else {
				assert.Zero(t, result.RequeueAfter)
			}
		})
	}
}

// mockIAMClient implements IAMClient for testing
type mockIAMClient struct {
	createKeyOutput *iam.CreateAccessKeyOutput
	createKeyError  error
	deleteKeyError  error
}

func (m *mockIAMClient) CreateAccessKey(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
	return m.createKeyOutput, m.createKeyError
}

func (m *mockIAMClient) DeleteAccessKey(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
	return &iam.DeleteAccessKeyOutput{}, m.deleteKeyError
}
