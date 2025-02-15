// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	stsTypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	oidcv3 "github.com/coreos/go-oidc/v3/oidc"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fake2 "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller/rotators"
)

func TestBackendSecurityController_Reconcile(t *testing.T) {
	ch := make(chan ConfigSinkEvent, 100)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := newBackendSecurityPolicyController(cl, &mockSTSOperations{}, fake2.NewClientset(), ctrl.Log, ch)
	backendSecurityPolicyName := "mybackendSecurityPolicy"
	namespace := "default"

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "clientSecret",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"client-secret": []byte("client-secret"),
		},
	}
	require.NoError(t, cl.Create(t.Context(), &secret, &client.CreateOptions{}))

	secret = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rotators.GetBSPSecretName(fmt.Sprintf("%s-OIDC", backendSecurityPolicyName)),
			Namespace: namespace,
			Annotations: map[string]string{
				rotators.ExpirationTimeAnnotationKey: "2024-01-01T01:01:00.000-00:00",
			},
		},
		Data: map[string][]byte{
			"credentials": []byte("credentials"),
		},
	}
	require.NoError(t, cl.Create(t.Context(), &secret, &client.CreateOptions{}))

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		b, err := json.Marshal(oauth2.Token{AccessToken: "some-access-token", TokenType: "Bearer", ExpiresIn: 60})
		require.NoError(t, err)
		_, err = w.Write(b)
		require.NoError(t, err)
	}))
	defer tokenServer.Close()

	discoveryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(`{"issuer": "issuer", "token_endpoint": "token_endpoint", "authorization_endpoint": "authorization_endpoint", "jwks_uri": "jwks_uri", "scopes_supported": []}`))
		require.NoError(t, err)
	}))
	defer discoveryServer.Close()

	err := cl.Create(t.Context(), &aigv1a1.BackendSecurityPolicy{ObjectMeta: metav1.ObjectMeta{Name: backendSecurityPolicyName, Namespace: namespace}})
	require.NoError(t, err)
	err = cl.Create(t.Context(), &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-OIDC", backendSecurityPolicyName), Namespace: namespace},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
			AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
				Region: "us-east-1",
				OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{
					OIDC: egv1a1.OIDC{
						Provider: egv1a1.OIDCProvider{
							Issuer:        discoveryServer.URL,
							TokenEndpoint: &tokenServer.URL,
						},
						ClientID: "some-client-id",
						ClientSecret: gwapiv1.SecretObjectReference{
							Name:      "clientSecret",
							Namespace: (*gwapiv1.Namespace)(&namespace),
						},
					},
					GrantType:  "placeholder",
					Aud:        "placeholder",
					AwsRoleArn: "placeholder",
				},
			},
		},
	})
	require.NoError(t, err)
	res, err := c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: backendSecurityPolicyName}})
	require.NoError(t, err)
	require.False(t, res.Requeue)
	item, ok := <-ch
	require.True(t, ok)
	require.IsType(t, &aigv1a1.BackendSecurityPolicy{}, item)
	require.Equal(t, backendSecurityPolicyName, item.(*aigv1a1.BackendSecurityPolicy).Name)
	require.Equal(t, namespace, item.(*aigv1a1.BackendSecurityPolicy).Namespace)

	ctx := oidcv3.InsecureIssuerURLContext(t.Context(), discoveryServer.URL)
	res, err = c.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: fmt.Sprintf("%s-OIDC", backendSecurityPolicyName)}})
	require.NoError(t, err)
	require.WithinRange(t, time.Now().Add(res.RequeueAfter), time.Now().Add(50*time.Minute), time.Now().Add(time.Hour))

	require.Len(t, c.oidcTokenCache, 1)
	token, ok := c.oidcTokenCache[fmt.Sprintf("%s-OIDC.%s", backendSecurityPolicyName, namespace)]
	require.True(t, ok)
	require.Equal(t, "some-access-token", token.AccessToken)

	updatedSecret, err := rotators.LookupSecret(t.Context(), cl, namespace, rotators.GetBSPSecretName(fmt.Sprintf("%s-OIDC", backendSecurityPolicyName)))
	require.NoError(t, err)
	require.NotEqualf(t, secret.Annotations[rotators.ExpirationTimeAnnotationKey], updatedSecret.Annotations[rotators.ExpirationTimeAnnotationKey], "expected updated expiration time annotation")

	// Test the case where the BackendSecurityPolicy is being deleted.
	err = cl.Delete(t.Context(), &aigv1a1.BackendSecurityPolicy{ObjectMeta: metav1.ObjectMeta{Name: backendSecurityPolicyName, Namespace: namespace}})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: backendSecurityPolicyName}})
	require.NoError(t, err)
}

// mockSTSOperations implements the STSOperations interface for testing
type mockSTSOperations struct{}

// AssumeRoleWithWebIdentity will return placeholder of type aws credentials.
//
// This implements [STSClient.AssumeRoleWithWebIdentity].
func (m *mockSTSOperations) AssumeRoleWithWebIdentity(_ context.Context, _ *sts.AssumeRoleWithWebIdentityInput, _ ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	return &sts.AssumeRoleWithWebIdentityOutput{
		Credentials: &stsTypes.Credentials{
			AccessKeyId:     aws.String("NEWKEY"),
			SecretAccessKey: aws.String("NEWSECRET"),
			SessionToken:    aws.String("NEWTOKEN"),
			Expiration:      aws.Time(time.Now().Add(1 * time.Hour)),
		},
	}, nil
}
