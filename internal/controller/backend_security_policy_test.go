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
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller/rotators"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestBackendSecurityController_Reconcile(t *testing.T) {
	syncFn := internaltesting.NewSyncFnImpl[aigv1a1.AIServiceBackend]()
	fakeClient := requireNewFakeClientWithIndexes(t)
	c := NewBackendSecurityPolicyController(fakeClient, fake2.NewClientset(), ctrl.Log, syncFn.Sync)
	backendSecurityPolicyName := "mybackendSecurityPolicy"
	namespace := "default"

	// Create AIServiceBackend that references the BackendSecurityPolicy.
	asb := &aigv1a1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{
				Name: gwapiv1.ObjectName("mybackend"),
				Port: ptr.To[gwapiv1.PortNumber](8080),
			},
			BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{
				Name: gwapiv1.ObjectName(backendSecurityPolicyName),
			},
		},
	}
	require.NoError(t, fakeClient.Create(t.Context(), asb))

	err := fakeClient.Create(t.Context(), &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: backendSecurityPolicyName, Namespace: namespace},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
			APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
				SecretRef: &gwapiv1.SecretObjectReference{Name: "mysecret"},
			},
		},
	})
	require.NoError(t, err)
	res, err := c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: backendSecurityPolicyName}})
	require.NoError(t, err)
	require.False(t, res.Requeue)
	items := syncFn.GetItems()
	require.Len(t, items, 1)
	require.Equal(t, asb, items[0])

	// Test the case where the BackendSecurityPolicy is being deleted.
	err = fakeClient.Delete(t.Context(), &aigv1a1.BackendSecurityPolicy{ObjectMeta: metav1.ObjectMeta{Name: backendSecurityPolicyName, Namespace: namespace}})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: backendSecurityPolicyName}})
	require.NoError(t, err)
}

// mockSTSClient implements the STSOperations interface for testing
type mockSTSClient struct {
	expTime time.Time
}

// AssumeRoleWithWebIdentity will return placeholder of type aws credentials.
//
// This implements [rotators.STSClient.AssumeRoleWithWebIdentity].
func (m *mockSTSClient) AssumeRoleWithWebIdentity(_ context.Context, _ *sts.AssumeRoleWithWebIdentityInput, _ ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	return &sts.AssumeRoleWithWebIdentityOutput{
		Credentials: &stsTypes.Credentials{
			AccessKeyId:     aws.String("NEWKEY"),
			SecretAccessKey: aws.String("NEWSECRET"),
			SessionToken:    aws.String("NEWTOKEN"),
			Expiration:      &m.expTime,
		},
	}, nil
}

func TestBackendSecurityPolicyController_ReconcileOIDC(t *testing.T) {
	syncFn := internaltesting.NewSyncFnImpl[aigv1a1.AIServiceBackend]()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := NewBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, syncFn.Sync)
	backendSecurityPolicyName := "mybackendSecurityPolicy"
	namespace := "default"

	bsp := &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-OIDC", backendSecurityPolicyName), Namespace: namespace},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
			AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
				OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{
					OIDC: egv1a1.OIDC{},
				},
			},
		},
	}
	err := cl.Create(t.Context(), bsp)
	require.NoError(t, err)

	// Expects rotate credentials to fail due to missing OIDC details.
	res, err := c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: fmt.Sprintf("%s-OIDC", backendSecurityPolicyName)}})
	require.Error(t, err)
	require.Equal(t, time.Minute, res.RequeueAfter)
}

func TestBackendSecurityController_RotateCredentials(t *testing.T) {
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

	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := NewBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, internaltesting.NewSyncFnImpl[aigv1a1.AIServiceBackend]().Sync)
	bspName := "mybackendSecurityPolicy"
	bspNamespace := "default"

	// initial secret lookup failure as no secret exist
	_, err := rotators.LookupSecret(t.Context(), cl, bspNamespace, rotators.GetBSPSecretName(fmt.Sprintf("%s-OIDC", bspName)))
	require.Error(t, err)

	oidcSecretName := "oidcClientSecret"
	awsSecretName := rotators.GetBSPSecretName(fmt.Sprintf("%s-OIDC", bspName))

	// create oidc secret
	oidcSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      oidcSecretName,
			Namespace: bspNamespace,
		},
		Data: map[string][]byte{
			"client-secret": []byte("client-secret"),
		},
	}
	require.NoError(t, cl.Create(t.Context(), &oidcSecret, &client.CreateOptions{}))

	// create backend security policy
	oidc := egv1a1.OIDC{
		Provider: egv1a1.OIDCProvider{
			Issuer:        discoveryServer.URL,
			TokenEndpoint: &tokenServer.URL,
		},
		ClientID: "some-client-id",
		ClientSecret: gwapiv1.SecretObjectReference{
			Name:      gwapiv1.ObjectName(oidcSecretName),
			Namespace: (*gwapiv1.Namespace)(&bspNamespace),
		},
	}
	bsp := &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-OIDC", bspName), Namespace: bspNamespace},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
			AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
				OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{
					OIDC: oidc,
				},
			},
		},
	}
	err = cl.Create(t.Context(), bsp)
	require.NoError(t, err)

	// new aws oidc rotator
	ctx := oidcv3.InsecureIssuerURLContext(t.Context(), discoveryServer.URL)
	rotator, err := rotators.NewAWSOIDCRotator(ctx, cl, &mockSTSClient{time.Now().Add(time.Hour)}, fake2.NewClientset(), ctrl.Log, bspNamespace, bsp.Name, preRotationWindow, "placeholder", "us-east-1")
	require.NoError(t, err)

	// ensure aws credentials secret do not exist
	_, err = rotators.LookupSecret(t.Context(), cl, bspNamespace, awsSecretName)
	require.Error(t, err)

	// first credential rotation should create aws credentials secret
	res, err := c.rotateCredential(ctx, bsp, oidc, rotator)
	require.NoError(t, err)
	require.WithinRange(t, time.Now().Add(res), time.Now().Add(50*time.Minute), time.Now().Add(time.Hour))

	// ensure oidc token cache has oidc token
	require.Len(t, c.oidcTokenCache, 1)
	token, ok := c.oidcTokenCache[fmt.Sprintf("%s-OIDC.%s", bspName, bspNamespace)]
	require.True(t, ok)
	require.Equal(t, "some-access-token", token.AccessToken)

	// ensure both oidc secret and aws credential secret are created
	returnOidcSecret, err := rotators.LookupSecret(t.Context(), cl, bspNamespace, oidcSecretName)
	require.NoError(t, err)
	require.Equal(t, "client-secret", string(returnOidcSecret.Data["client-secret"]))

	awsSecret1, err := rotators.LookupSecret(t.Context(), cl, bspNamespace, awsSecretName)
	require.NoError(t, err)

	// second credential rotation should update expiration time
	t0 := awsSecret1.Annotations[rotators.ExpirationTimeAnnotationKey]

	// set secret time expired
	parsedTime, err := time.Parse(time.RFC3339, t0)
	require.NoError(t, err)
	t1 := parsedTime.Add(-preRotationWindow - time.Minute).String()
	awsSecret1.Annotations[rotators.ExpirationTimeAnnotationKey] = t1
	require.NoError(t, cl.Update(t.Context(), awsSecret1))

	// rotate credential
	_, err = c.rotateCredential(ctx, bsp, oidc, rotator)
	require.NoError(t, err)
	awsSecret2, err := rotators.LookupSecret(t.Context(), cl, bspNamespace, awsSecretName)
	require.NoError(t, err)
	t2 := awsSecret2.Annotations[rotators.ExpirationTimeAnnotationKey]
	require.NotEqual(t, t1, t2)
}

func TestBackendSecurityPolicyController_RotateExpiredCredential(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := NewBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, internaltesting.NewSyncFnImpl[aigv1a1.AIServiceBackend]().Sync)
	bspName := "mybackendSecurityPolicy"
	bspNamespace := "default"

	c.oidcTokenCache[backendSecurityPolicyKey(bspNamespace, bspName)] = &oauth2.Token{AccessToken: "some-access-token", Expiry: time.Now().Add(time.Hour)}
	rotator, err := rotators.NewAWSOIDCRotator(t.Context(), cl, &mockSTSClient{time.Now().Add(-time.Hour)}, fake2.NewClientset(), ctrl.Log, bspNamespace, bspName, preRotationWindow, "placeholder", "us-east-1")
	require.NoError(t, err)

	oidcCreds := egv1a1.OIDC{}
	policy := &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bspName,
			Namespace: bspNamespace,
		},
	}

	// Expiration time will be before current time, so the requeue will be changed to one minute.
	requeue, err := c.rotateCredential(t.Context(), policy, oidcCreds, rotator)
	require.Error(t, err)
	require.Contains(t, err.Error(), "newly rotate credentials is already expired")
	require.Equal(t, time.Minute, requeue)
}

func TestBackendSecurityController_GetBackendSecurityPolicyAuthOIDC(t *testing.T) {
	// API Key type does not support OIDC.
	require.Nil(t, getBackendSecurityPolicyAuthOIDC(aigv1a1.BackendSecurityPolicySpec{Type: aigv1a1.BackendSecurityPolicyTypeAPIKey}))

	// AWS type supports OIDC type but OIDC needs to be defined.
	require.Nil(t, getBackendSecurityPolicyAuthOIDC(aigv1a1.BackendSecurityPolicySpec{
		Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
		AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
			CredentialsFile: &aigv1a1.AWSCredentialsFile{},
		},
	}))

	// AWS type with OIDC defined.
	oidc := getBackendSecurityPolicyAuthOIDC(aigv1a1.BackendSecurityPolicySpec{
		Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
		AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
			OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{
				OIDC: egv1a1.OIDC{
					ClientID: "some-client-id",
				},
			},
		},
	})
	require.NotNil(t, oidc)
	require.Equal(t, "some-client-id", oidc.ClientID)
}
