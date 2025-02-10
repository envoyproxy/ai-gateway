// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fake2 "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"testing"
	"time"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func TestBackendSecurityController_Reconcile(t *testing.T) {
	ch := make(chan ConfigSinkEvent, 100)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := newBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, ch)
	backendSecurityPolicyName := "mybackendSecurityPolicy"
	namespace := "default"

	err := cl.Create(t.Context(), &aigv1a1.BackendSecurityPolicy{ObjectMeta: metav1.ObjectMeta{Name: backendSecurityPolicyName, Namespace: namespace}})
	require.NoError(t, err)
	err = cl.Create(context.Background(), &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-OIDC", backendSecurityPolicyName), Namespace: namespace},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
			AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
				Region: "us-east-1",
				OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{
					OIDC: egv1a1.OIDC{},
				},
			},
		},
	})
	require.NoError(t, err)
	res, err := c.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: backendSecurityPolicyName}})
	require.NoError(t, err)
	require.False(t, res.Requeue)
	item, ok := <-ch
	require.True(t, ok)
	require.IsType(t, &aigv1a1.BackendSecurityPolicy{}, item)
	require.Equal(t, backendSecurityPolicyName, item.(*aigv1a1.BackendSecurityPolicy).Name)
	require.Equal(t, namespace, item.(*aigv1a1.BackendSecurityPolicy).Namespace)

	res, err = c.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: fmt.Sprintf("%s-OIDC", backendSecurityPolicyName)}})
	require.NoError(t, err)
	require.True(t, res.Requeue)
	require.Equal(t, res.RequeueAfter, time.Minute)

	// Test the case where the BackendSecurityPolicy is being deleted.
	err = cl.Delete(context.Background(), &aigv1a1.BackendSecurityPolicy{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-OIDC", backendSecurityPolicyName), Namespace: namespace}})
	require.NoError(t, err)

	res, err = c.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: fmt.Sprintf("%s-OIDC", backendSecurityPolicyName)}})
	require.NoError(t, err)
	require.False(t, res.Requeue)
}

func TestBackendSecurityController_GetBackendSecurityPolicyAuthOIDC(t *testing.T) {
	require.Nil(t, getBackendSecurityPolicyAuthOIDC(aigv1a1.BackendSecurityPolicySpec{
		Type:   aigv1a1.BackendSecurityPolicyTypeAPIKey,
		APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{},
	}))

	require.Nil(t, getBackendSecurityPolicyAuthOIDC(aigv1a1.BackendSecurityPolicySpec{
		Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
		AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
			Region:          "us-east-1",
			CredentialsFile: &aigv1a1.AWSCredentialsFile{},
		},
	}))

	oidc := egv1a1.OIDC{
		Provider: egv1a1.OIDCProvider{
			Issuer: "https://oidc.example.com",
		},
		ClientID: "client-id",
		ClientSecret: gwapiv1.SecretObjectReference{
			Name: "client-secret",
		},
	}

	actualOIDC := getBackendSecurityPolicyAuthOIDC(aigv1a1.BackendSecurityPolicySpec{
		Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
		AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
			Region: "us-east-1",
			OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{
				OIDC: oidc,
			},
		},
	})
	require.NotNil(t, actualOIDC)
	require.Equal(t, oidc.ClientID, actualOIDC.ClientID)
	require.Equal(t, oidc.Provider.Issuer, actualOIDC.Provider.Issuer)
	require.Equal(t, oidc.ClientSecret.Name, actualOIDC.ClientSecret.Name)
}
