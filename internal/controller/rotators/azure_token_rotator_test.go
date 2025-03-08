// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package rotators

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/envoyproxy/ai-gateway/constants"
)

type MockTokenProvider struct {
	mock.Mock
}

func (m *MockTokenProvider) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	args := m.Called(ctx, opts)
	return args.Get(0).(azcore.AccessToken), args.Error(1)
}

func TestAzureTokenRotator_Rotate(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	t.Run("failed to get azure token", func(t *testing.T) {
		now := time.Now()
		oneHourBeforeNow := now.Add(-1 * time.Hour)
		mockProvider := new(MockTokenProvider)
		mockProvider.Mock.On("GetToken", mock.Anything, mock.Anything).Return(azcore.AccessToken{}, fmt.Errorf("failure to get azure access token error"))

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      GetBSPSecretName("test-policy"),
				Namespace: "default",
				Annotations: map[string]string{
					ExpirationTimeAnnotationKey: oneHourBeforeNow.Format(time.RFC3339),
				},
			},
			Data: map[string][]byte{
				constants.AzureAccessTokenKey: []byte("some-azure-access-token"),
			},
		}
		err := client.Create(context.Background(), secret)
		require.NoError(t, err)

		rotator := &AzureTokenRotator{
			client:                         client,
			tokenProvider:                  mockProvider,
			backendSecurityPolicyNamespace: "default",
			backendSecurityPolicyName:      "test-policy",
			preRotationWindow:              5 * time.Minute,
		}

		_, err = rotator.Rotate(context.Background(), "test-policy")
		require.Error(t, err)
		err = client.Delete(context.Background(), secret)
		require.NoError(t, err)
	})

	t.Run("secret does not exist", func(t *testing.T) {
		now := time.Now()
		twoHourAfterNow := now.Add(2 * time.Hour)
		mockProvider := new(MockTokenProvider)
		mockProvider.Mock.On("GetToken", mock.Anything, mock.Anything).Return(azcore.AccessToken{Token: "fake-token", ExpiresOn: twoHourAfterNow}, nil)

		rotator := &AzureTokenRotator{
			client:                         client,
			tokenProvider:                  mockProvider,
			backendSecurityPolicyNamespace: "default",
			backendSecurityPolicyName:      "test-policy",
			preRotationWindow:              5 * time.Minute,
		}
		expiration, err := rotator.Rotate(context.Background(), "test-policy")
		require.NoError(t, err)
		secret, err := LookupSecret(context.Background(), client, "default", GetBSPSecretName("test-policy"))
		require.NoError(t, err)
		err = client.Delete(context.Background(), secret)
		require.NoError(t, err)
		require.Equal(t, twoHourAfterNow, expiration)
	})

	t.Run("secret exist", func(t *testing.T) {
		now := time.Now()
		twoHourAfterNow := now.Add(2 * time.Hour)
		oneHourBeforeNow := now.Add(-1 * time.Hour)
		mockProvider := new(MockTokenProvider)
		mockProvider.Mock.On("GetToken", mock.Anything, mock.Anything).Return(azcore.AccessToken{Token: "fake-token", ExpiresOn: twoHourAfterNow}, nil)

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      GetBSPSecretName("test-policy"),
				Namespace: "default",
				Annotations: map[string]string{
					ExpirationTimeAnnotationKey: oneHourBeforeNow.Format(time.RFC3339),
				},
			},
			Data: map[string][]byte{
				constants.AzureAccessTokenKey: []byte("some-azure-access-token"),
			},
		}
		err := client.Create(context.Background(), secret)
		require.NoError(t, err)

		rotator := &AzureTokenRotator{
			client:                         client,
			tokenProvider:                  mockProvider,
			backendSecurityPolicyNamespace: "default",
			backendSecurityPolicyName:      "test-policy",
			preRotationWindow:              5 * time.Minute,
		}

		expiration, err := rotator.Rotate(context.Background(), "test-policy")
		require.NoError(t, err)
		require.Equal(t, twoHourAfterNow, expiration)

		err = client.Delete(context.Background(), secret)
		require.NoError(t, err)
	})
}

func TestAzureTokenRotator_GetPreRotationTime(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	rotator := &AzureTokenRotator{
		client:                         client,
		preRotationWindow:              5 * time.Minute,
		backendSecurityPolicyNamespace: "default",
		backendSecurityPolicyName:      "test-policy",
	}

	now := time.Now()

	tests := []struct {
		name          string
		secret        *corev1.Secret
		expectedTime  time.Time
		expectedError bool
	}{
		{
			name: "secret annotation missing",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      GetBSPSecretName("test-policy"),
					Namespace: "default",
				},
				Data: map[string][]byte{
					constants.AzureAccessTokenKey: []byte("some-azure-access-token"),
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
					constants.AzureAccessTokenKey: []byte("some-azure-access-token"),
				},
			},
			expectedTime:  now.Add(2 * time.Hour),
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.Create(context.Background(), tt.secret)
			require.NoError(t, err)

			got, err := rotator.GetPreRotationTime(context.Background())
			if (err != nil) != tt.expectedError {
				t.Errorf("AzureTokenRotator.GetPreRotationTime() error = %v, expectedError %v", err, tt.expectedError)
				return
			}
			if !tt.expectedTime.IsZero() && got.Compare(tt.expectedTime) >= 0 {
				t.Errorf("AzureTokenRotator.GetPreRotationTime() = %v, expected %v", got, tt.expectedTime)
			}
			err = client.Delete(context.Background(), tt.secret)
			require.NoError(t, err)
		})
	}
}

func TestAzureTokenRotator_IsExpired(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	rotator := &AzureTokenRotator{
		client: client,
	}
	tests := []struct {
		name       string
		expiration time.Time
		expect     bool
	}{
		{
			name:       "not expired",
			expiration: time.Now().Add(1 * time.Hour),
			expect:     false,
		},
		{
			name:       "expired",
			expiration: time.Now().Add(-1 * time.Hour),
			expect:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rotator.IsExpired(tt.expiration); got != tt.expect {
				t.Errorf("AzureTokenRotator.IsExpired() = %v, expect %v", got, tt.expect)
			}
		})
	}
}
