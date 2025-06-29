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
	"strings"
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
	eventCh := internaltesting.NewControllerEventChan[*aigv1a1.AIServiceBackend]()
	fakeClient := requireNewFakeClientWithIndexes(t)
	c := NewBackendSecurityPolicyController(fakeClient, fake2.NewClientset(), ctrl.Log, eventCh.Ch)
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
	items := eventCh.RequireItemsEventually(t, 1)
	require.Len(t, items, 1)
	require.Equal(t, asb, items[0])

	// Check that the status was updated.
	var bsp aigv1a1.BackendSecurityPolicy
	require.NoError(t, fakeClient.Get(t.Context(), types.NamespacedName{Namespace: namespace, Name: backendSecurityPolicyName}, &bsp))
	require.Len(t, bsp.Status.Conditions, 1)
	require.Equal(t, aigv1a1.ConditionTypeAccepted, bsp.Status.Conditions[0].Type)
	require.Equal(t, "BackendSecurityPolicy reconciled successfully", bsp.Status.Conditions[0].Message)

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

func TestBackendSecurityPolicyController_ReconcileOIDC_Fail(t *testing.T) {
	eventCh := internaltesting.NewControllerEventChan[*aigv1a1.AIServiceBackend]()
	cl := fake.NewClientBuilder().WithScheme(Scheme).Build()
	c := NewBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, eventCh.Ch)
	bspName := "mybackendSecurityPolicy"
	bspNamespace := "default"

	bsp := &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-OIDC", bspName), Namespace: bspNamespace},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
			AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
				OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{
					BackendSecurityPolicyOIDC: aigv1a1.BackendSecurityPolicyOIDC{
						OIDC: egv1a1.OIDC{},
					},
				},
			},
		},
	}
	err := cl.Create(t.Context(), bsp)
	require.NoError(t, err)
	// Expects rotate credentials to fail due to missing OIDC details.
	res, err := c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: bspNamespace, Name: fmt.Sprintf("%s-OIDC", bspName)}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create oidc config")
	require.Equal(t, time.Minute, res.RequeueAfter)
}

func TestBackendSecurityPolicyController_RotateCredential(t *testing.T) {
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

	eventCh := internaltesting.NewControllerEventChan[*aigv1a1.AIServiceBackend]()
	cl := fake.NewClientBuilder().WithScheme(Scheme).Build()
	c := NewBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, eventCh.Ch)
	bspName := "mybackendSecurityPolicy"
	bspNamespace := "default"

	// initial secret lookup failure as no secret exist
	_, err := rotators.LookupSecret(t.Context(), cl, bspNamespace, rotators.GetBSPSecretName(fmt.Sprintf("%s-OIDC", bspName)))
	require.Error(t, err)
	require.Equal(t, "secrets \"ai-eg-bsp-mybackendSecurityPolicy-OIDC\" not found", err.Error())

	oidcSecretName := "oidcClientSecret"

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

	// create backend security policy with OIDC config
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
					BackendSecurityPolicyOIDC: aigv1a1.BackendSecurityPolicyOIDC{
						OIDC: oidc,
					},
				},
				Region: "us-east-1",
			},
		},
	}
	err = cl.Create(t.Context(), bsp)
	require.NoError(t, err)

	ctx := oidcv3.InsecureIssuerURLContext(t.Context(), discoveryServer.URL)

	data := map[string][]byte{
		"credentials": []byte(fmt.Sprintf("[%s]\naws_access_key_id = %s\naws_secret_access_key = %s\naws_session_token = %s\nregion = %s\n",
			"default", "accessKey", "secretKey", "sessionToken", "us-east-2")),
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("ai-eg-bsp-%s-OIDC", bspName),
			Namespace: bspNamespace,
			Annotations: map[string]string{
				rotators.ExpirationTimeAnnotationKey: time.Now().Add(60 * time.Minute).Format(time.RFC3339),
			},
		},
		Data: data,
	}
	err = cl.Create(ctx, secret)
	require.NoError(t, err)
	_, err = c.rotateCredential(ctx, bsp)
	require.NoError(t, err)
}

func TestBackendSecurityPolicyController_RotateExpiredCredential(t *testing.T) {
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

	cl := fake.NewClientBuilder().WithScheme(Scheme).Build()
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
					BackendSecurityPolicyOIDC: aigv1a1.BackendSecurityPolicyOIDC{
						OIDC: oidc,
					},
				},
			},
		},
	}
	err = cl.Create(t.Context(), bsp)
	require.NoError(t, err)

	// new aws oidc rotator
	ctx := oidcv3.InsecureIssuerURLContext(t.Context(), discoveryServer.URL)
	rotator, err := rotators.NewAWSOIDCRotator(ctx, cl, &mockSTSClient{time.Now().Add(time.Hour)}, fake2.NewClientset(), ctrl.Log, bspNamespace, bsp.Name, preRotationWindow,
		oidc, "placeholder", "us-east-1")
	require.NoError(t, err)

	// ensure aws credentials secret do not exist
	_, err = rotators.LookupSecret(t.Context(), cl, bspNamespace, awsSecretName)
	require.Error(t, err)

	// first credential rotation should create aws credentials secret
	res, err := rotator.Rotate(ctx)
	require.NoError(t, err)
	require.WithinRange(t, time.Now().Add(time.Until(res.Add(-preRotationWindow))), time.Now().Add(50*time.Minute), time.Now().Add(time.Hour))

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
	_, err = rotator.Rotate(ctx)
	require.NoError(t, err)
	awsSecret2, err := rotators.LookupSecret(t.Context(), cl, bspNamespace, awsSecretName)
	require.NoError(t, err)
	t2 := awsSecret2.Annotations[rotators.ExpirationTimeAnnotationKey]
	require.NotEqual(t, t1, t2)
}

func TestBackendSecurityPolicyController_GetBackendSecurityPolicyAuthOIDC(t *testing.T) {
	// API Key type does not support OIDC.
	require.Nil(t, getBackendSecurityPolicyAuthOIDC(aigv1a1.BackendSecurityPolicySpec{Type: aigv1a1.BackendSecurityPolicyTypeAPIKey}))

	// Azure type supports OIDC type but OIDC needs to be defined.
	require.Nil(t, getBackendSecurityPolicyAuthOIDC(aigv1a1.BackendSecurityPolicySpec{
		Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
		AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
			ClientID:        "client-id",
			TenantID:        "tenant-id",
			ClientSecretRef: nil,
		},
	}))

	// Azure type with OIDC defined.
	oidcAzure := getBackendSecurityPolicyAuthOIDC(aigv1a1.BackendSecurityPolicySpec{
		Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
		AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
			ClientID: "client-id",
			TenantID: "tenant-id",
			OIDCExchangeToken: &aigv1a1.AzureOIDCExchangeToken{
				BackendSecurityPolicyOIDC: aigv1a1.BackendSecurityPolicyOIDC{
					OIDC: egv1a1.OIDC{
						ClientID: "some-client-id",
					},
				},
			},
		},
	})

	require.NotNil(t, oidcAzure)
	require.Equal(t, "some-client-id", oidcAzure.ClientID)

	// AWS type supports OIDC type but OIDC needs to be defined.
	require.Nil(t, getBackendSecurityPolicyAuthOIDC(aigv1a1.BackendSecurityPolicySpec{
		Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
		AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
			CredentialsFile: &aigv1a1.AWSCredentialsFile{},
		},
	}))

	// AWS type with OIDC defined.
	oidcAWS := getBackendSecurityPolicyAuthOIDC(aigv1a1.BackendSecurityPolicySpec{
		Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
		AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
			OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{
				BackendSecurityPolicyOIDC: aigv1a1.BackendSecurityPolicyOIDC{
					OIDC: egv1a1.OIDC{
						ClientID: "some-client-id",
					},
				},
			},
		},
	})
	require.NotNil(t, oidcAWS)
	require.Equal(t, "some-client-id", oidcAWS.ClientID)

	// GCP type with OIDC defined.
	oidcGCP := getBackendSecurityPolicyAuthOIDC(aigv1a1.BackendSecurityPolicySpec{
		Type: aigv1a1.BackendSecurityPolicyTypeGCPCredentials,
		GCPCredentials: &aigv1a1.BackendSecurityPolicyGCPCredentials{
			ProjectName: "fake-project-name",
			Region:      "fake-region",
			WorkLoadIdentityFederationConfig: aigv1a1.GCPWorkLoadIdentityFederationConfig{
				ProjectID: "fake-project-id",
				WorkloadIdentityProvider: aigv1a1.GCPWorkloadIdentityProvider{
					Name: "fake-workload-identity-provider-name",
					OIDCProvider: aigv1a1.BackendSecurityPolicyOIDC{
						OIDC: egv1a1.OIDC{
							ClientID: "some-client-id",
						},
					},
				},
				WorkloadIdentityPoolName:    "fake-workload-identity-pool-name",
				ServiceAccountImpersonation: nil,
			},
		},
	})
	require.NotNil(t, oidcGCP)
	require.Equal(t, "some-client-id", oidcGCP.ClientID)
}

func TestNewBackendSecurityPolicyController_ReconcileAzureMissingSecret(t *testing.T) {
	eventCh := internaltesting.NewControllerEventChan[*aigv1a1.AIServiceBackend]()
	cl := fake.NewClientBuilder().WithScheme(Scheme).Build()
	c := NewBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, eventCh.Ch)
	bspName := "my-azure-backend-security-policy"
	tenantID := "some-tenant-id"
	clientID := "some-client-id"

	bsp := &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: bspName, Namespace: "default"},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
			AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
				ClientID:        clientID,
				TenantID:        tenantID,
				ClientSecretRef: &gwapiv1.SecretObjectReference{Name: "some-azure-secret", Namespace: ptr.To[gwapiv1.Namespace]("default")},
			},
		},
	}
	err := cl.Create(t.Context(), bsp)
	require.NoError(t, err)
	res, err := c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: bspName}})
	require.Error(t, err)
	require.Equal(t, "secrets \"some-azure-secret\" not found", err.Error())
	require.Equal(t, time.Duration(0), res.RequeueAfter)
}

func TestNewBackendSecurityPolicyController_ReconcileAzureMissingSecretData(t *testing.T) {
	eventCh := internaltesting.NewControllerEventChan[*aigv1a1.AIServiceBackend]()
	cl := fake.NewClientBuilder().WithScheme(Scheme).Build()
	c := NewBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, eventCh.Ch)
	bspName := "my-azure-backend-security-policy"
	tenantID := "some-tenant-id"
	clientID := "some-client-id"

	azureClientSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "some-azure-secret",
			Namespace: "default",
		},
	}
	require.NoError(t, cl.Create(t.Context(), &azureClientSecret, &client.CreateOptions{}))

	bsp := &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: bspName, Namespace: "default"},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
			AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
				ClientID: clientID,
				TenantID: tenantID,
				ClientSecretRef: &gwapiv1.SecretObjectReference{
					Name:      "some-azure-secret",
					Namespace: ptr.To[gwapiv1.Namespace]("default"),
				},
			},
		},
	}
	err := cl.Create(t.Context(), bsp)
	require.NoError(t, err)
	res, err := c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: bspName}})
	require.Error(t, err)
	require.Equal(t, "missing azure client secret key client-secret", err.Error())
	require.Equal(t, time.Duration(0), res.RequeueAfter)
}

func TestNewBackendSecurityPolicyController_RotateCredentialInvalidType(t *testing.T) {
	eventCh := internaltesting.NewControllerEventChan[*aigv1a1.AIServiceBackend]()
	cl := fake.NewClientBuilder().WithScheme(Scheme).Build()
	c := NewBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, eventCh.Ch)
	bspName := "some-backend-security-policy"
	bspNamespace := "default"

	bsp := &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-OIDC", bspName), Namespace: bspNamespace},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			Type: "Unknown",
			AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
				OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{
					BackendSecurityPolicyOIDC: aigv1a1.BackendSecurityPolicyOIDC{
						OIDC: egv1a1.OIDC{},
					},
				},
			},
		},
	}
	err := cl.Create(t.Context(), bsp)
	require.NoError(t, err)
	res, err := c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: bspNamespace, Name: fmt.Sprintf("%s-OIDC", bspName)}})
	require.Error(t, err)
	require.Equal(t, time.Duration(0), res.RequeueAfter)
}

func TestNewBackendSecurityPolicyController_RotateCredentialAwsCredentialFile(t *testing.T) {
	eventCh := internaltesting.NewControllerEventChan[*aigv1a1.AIServiceBackend]()
	cl := fake.NewClientBuilder().WithScheme(Scheme).Build()
	c := NewBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, eventCh.Ch)
	bspName := "some-backend-security-policy"
	bspNamespace := "default"

	bsp := &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-OIDC", bspName), Namespace: bspNamespace},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
			AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
				CredentialsFile: &aigv1a1.AWSCredentialsFile{},
			},
		},
	}
	err := cl.Create(t.Context(), bsp)
	require.NoError(t, err)
	res, err := c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: bspNamespace, Name: fmt.Sprintf("%s-OIDC", bspName)}})
	require.Error(t, err)
	require.Equal(t, time.Duration(0), res.RequeueAfter)
}

func TestNewBackendSecurityPolicyController_RotateCredentialAzureIncorrectSecretRef(t *testing.T) {
	eventCh := internaltesting.NewControllerEventChan[*aigv1a1.AIServiceBackend]()
	cl := fake.NewClientBuilder().WithScheme(Scheme).Build()
	c := NewBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, eventCh.Ch)

	tenantID := "some-tenant-id"
	clientID := "some-client-id"
	secretName := rotators.GetBSPSecretName("some-secret")
	err := cl.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: "default",
		},
		Data: map[string][]byte{
			"client-secret": []byte("client-secret"),
		},
	})
	require.NoError(t, err)

	bsp := &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "some-policy", Namespace: "default"},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
			AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
				ClientID:        clientID,
				TenantID:        tenantID,
				ClientSecretRef: &gwapiv1.SecretObjectReference{Name: gwapiv1.ObjectName("some-other-secret-name"), Namespace: ptr.To[gwapiv1.Namespace]("default")},
			},
		},
	}
	err = cl.Create(t.Context(), bsp)
	require.NoError(t, err)

	res, err := c.rotateCredential(t.Context(), bsp)
	require.Error(t, err)
	require.Equal(t, time.Duration(0), res.RequeueAfter)
}

func TestBackendSecurityPolicyController_ExecutionRotation(t *testing.T) {
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

	eventCh := internaltesting.NewControllerEventChan[*aigv1a1.AIServiceBackend]()
	cl := fake.NewClientBuilder().WithScheme(Scheme).Build()
	c := NewBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, eventCh.Ch)
	bspNamespace := "default"
	bspName := "some-back-end-security-policy"
	oidcSecretName := "oidcClientSecret"
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
					BackendSecurityPolicyOIDC: aigv1a1.BackendSecurityPolicyOIDC{
						OIDC: oidc,
					},
				},
				Region: "us-east-1",
			},
		},
	}
	require.NoError(t, cl.Create(t.Context(), bsp))
	ctx := oidcv3.InsecureIssuerURLContext(t.Context(), discoveryServer.URL)
	data := map[string][]byte{
		"credentials": []byte(fmt.Sprintf("[%s]\naws_access_key_id = %s\naws_secret_access_key = %s\naws_session_token = %s\nregion = %s\n",
			"default", "accessKey", "secretKey", "sessionToken", "us-east-2")),
	}
	now := time.Now()
	expirationTime := now.Add(-1 * time.Hour)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("ai-eg-bsp-%s-OIDC", bspName),
			Namespace: bspNamespace,
			Annotations: map[string]string{
				rotators.ExpirationTimeAnnotationKey: expirationTime.Format(time.RFC3339),
			},
		},
		Data: data,
	}
	require.NoError(t, cl.Create(ctx, secret))

	rotator, err := rotators.NewAWSOIDCRotator(
		ctx,
		cl,
		&mockSTSClient{now.Add(time.Hour)},
		fake2.NewClientset(),
		ctrl.Log, bspNamespace,
		bsp.Name,
		preRotationWindow,
		oidc,
		"placeholder",
		"us-east-1",
	)
	require.NoError(t, err)
	res, err := c.executeRotation(ctx, rotator, bsp)
	require.NoError(t, err)
	require.Less(t, res.RequeueAfter, time.Hour)
}

func TestValidateGCPCredentialsParams(t *testing.T) {
	tests := []struct {
		name      string
		input     *aigv1a1.BackendSecurityPolicyGCPCredentials
		wantError string
	}{
		{
			name:      "nil credentials",
			input:     nil,
			wantError: "invalid backend security policy, gcp credentials cannot be nil",
		},
		{
			name: "empty project name",
			input: &aigv1a1.BackendSecurityPolicyGCPCredentials{
				ProjectName: "",
				Region:      "us-central1",
				WorkLoadIdentityFederationConfig: aigv1a1.GCPWorkLoadIdentityFederationConfig{
					ProjectID:                "pid",
					WorkloadIdentityPoolName: "pool",
					WorkloadIdentityProvider: aigv1a1.GCPWorkloadIdentityProvider{Name: "provider"},
				},
			},
			wantError: "invalid GCP credentials configuration: projectName cannot be empty",
		},
		{
			name: "empty region",
			input: &aigv1a1.BackendSecurityPolicyGCPCredentials{
				ProjectName: "proj",
				Region:      "",
				WorkLoadIdentityFederationConfig: aigv1a1.GCPWorkLoadIdentityFederationConfig{
					ProjectID:                "pid",
					WorkloadIdentityPoolName: "pool",
					WorkloadIdentityProvider: aigv1a1.GCPWorkloadIdentityProvider{Name: "provider"},
				},
			},
			wantError: "invalid GCP credentials configuration: region cannot be empty",
		},
		{
			name: "empty projectID",
			input: &aigv1a1.BackendSecurityPolicyGCPCredentials{
				ProjectName: "proj",
				Region:      "us-central1",
				WorkLoadIdentityFederationConfig: aigv1a1.GCPWorkLoadIdentityFederationConfig{
					ProjectID:                "",
					WorkloadIdentityPoolName: "pool",
					WorkloadIdentityProvider: aigv1a1.GCPWorkloadIdentityProvider{Name: "provider"},
				},
			},
			wantError: "invalid GCP Workload Identity Federation configuration: projectID cannot be empty",
		},
		{
			name: "empty workloadIdentityPoolName",
			input: &aigv1a1.BackendSecurityPolicyGCPCredentials{
				ProjectName: "proj",
				Region:      "us-central1",
				WorkLoadIdentityFederationConfig: aigv1a1.GCPWorkLoadIdentityFederationConfig{
					ProjectID:                "pid",
					WorkloadIdentityPoolName: "",
					WorkloadIdentityProvider: aigv1a1.GCPWorkloadIdentityProvider{Name: "provider"},
				},
			},
			wantError: "invalid GCP Workload Identity Federation configuration: workloadIdentityPoolName cannot be empty",
		},
		{
			name: "empty workloadIdentityProvider name",
			input: &aigv1a1.BackendSecurityPolicyGCPCredentials{
				ProjectName: "proj",
				Region:      "us-central1",
				WorkLoadIdentityFederationConfig: aigv1a1.GCPWorkLoadIdentityFederationConfig{
					ProjectID:                "pid",
					WorkloadIdentityPoolName: "pool",
					WorkloadIdentityProvider: aigv1a1.GCPWorkloadIdentityProvider{Name: ""},
				},
			},
			wantError: "invalid GCP Workload Identity Federation configuration: workloadIdentityProvider.name cannot be empty",
		},
		{
			name: "valid credentials",
			input: &aigv1a1.BackendSecurityPolicyGCPCredentials{
				ProjectName: "proj",
				Region:      "us-central1",
				WorkLoadIdentityFederationConfig: aigv1a1.GCPWorkLoadIdentityFederationConfig{
					ProjectID:                "pid",
					WorkloadIdentityPoolName: "pool",
					WorkloadIdentityProvider: aigv1a1.GCPWorkloadIdentityProvider{Name: "provider"},
				},
			},
			wantError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGCPCredentialsParams(tt.input)
			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Equal(t, tt.wantError, err.Error())
			}
		})
	}
}

func TestBackendSecurityPolicyController_RotateCredential_GCPCredentials(t *testing.T) {
	bspNamespace := "default"
	bspName := "test-gcp-policy"
	tests := []struct {
		name           string
		bsp            *aigv1a1.BackendSecurityPolicySpec
		expectedErrMsg string
	}{
		{
			name: "nil gcp credentials",
			bsp: &aigv1a1.BackendSecurityPolicySpec{
				Type:           aigv1a1.BackendSecurityPolicyTypeGCPCredentials,
				GCPCredentials: nil,
			},
			expectedErrMsg: "invalid backend security policy, gcp credentials cannot be nil",
		},
		{
			name: "empty gcp credentials",
			bsp: &aigv1a1.BackendSecurityPolicySpec{
				Type:           aigv1a1.BackendSecurityPolicyTypeGCPCredentials,
				GCPCredentials: &aigv1a1.BackendSecurityPolicyGCPCredentials{},
			},
			expectedErrMsg: "invalid GCP credentials configuration: projectName cannot be empty",
		},
	}

	c := NewBackendSecurityPolicyController(fake.NewFakeClient(), fake2.NewClientset(), ctrl.Log, nil)

	for _, tt := range tests {
		bsp := &aigv1a1.BackendSecurityPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bspName,
				Namespace: bspNamespace,
			},
			Spec: *tt.bsp,
		}
		t.Run(tt.name, func(t *testing.T) {
			// Initial rotation should create a new secret
			res, err := c.rotateCredential(context.Background(), bsp)

			switch {
			case tt.expectedErrMsg != "" && err == nil:
				t.Errorf("expected error but got none, expected: %s", tt.expectedErrMsg)
			case tt.expectedErrMsg == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.expectedErrMsg != "" && err != nil:
				strings.Contains(err.Error(), tt.expectedErrMsg)
			default:
				require.NoError(t, err)
				require.NotZero(t, res.RequeueAfter)
			}
		})
	}
}
