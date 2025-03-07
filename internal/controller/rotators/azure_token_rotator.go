// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package rotators

import (
	"context"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type AzureTokenRotator struct {
	// client is used for Kubernetes API operations.
	client client.Client
	// kube provides additional API capabilities.
	kube kubernetes.Interface
	// logger is used for structured logging.
	logger logr.Logger
	// backendSecurityPolicyName provides name of backend security policy
	backendSecurityPolicyName string
	// backendSecurityPolicyNamespace provides namespace of backend security policy.
	backendSecurityPolicyNamespace string
	// preRotationWindow specifies how long before expiry to rotate.
	preRotationWindow time.Duration
	// azureAuthClient provides Azure authentication with a client secret
	azureAuthClient azidentity.ClientSecretCredential
}

func NewAzureTokenRotator(
	client client.Client,
	kube kubernetes.Interface,
	logger logr.Logger,
	backendSecurityPolicyNamespace string,
	backendSecurityPolicyName string,
	preRotationWindow time.Duration,
	azureTenantID string,
	azureClientID string,
	azureClientSecret string,
) (*AzureTokenRotator, error) {
	// TODO XL it seems Azure SDK requires env: AZURE_REGIONAL_AUTHORITY_NAME to construct ClientSecretCredential
	// in confidential_client.go line 75 for region
	// may not needed, need to run e2e test to verify
	azureAuthClient, err := azidentity.NewClientSecretCredential(azureTenantID, azureClientID, azureClientSecret, nil)
	if err != nil {
		return nil, err
	}
	return &AzureTokenRotator{
		client:                         client,
		kube:                           kube,
		logger:                         logger.WithName("azure-token-rotator"),
		backendSecurityPolicyNamespace: backendSecurityPolicyNamespace,
		backendSecurityPolicyName:      backendSecurityPolicyName,
		preRotationWindow:              preRotationWindow,
		azureAuthClient:                *azureAuthClient,
	}, nil
}

// IsExpired checks if the preRotation time is before the current time.
func (r *AzureTokenRotator) IsExpired(preRotationExpirationTime time.Time) bool {
	return IsBufferedTimeExpired(0, preRotationExpirationTime)
}

func (r *AzureTokenRotator) GetPreRotationTime(ctx context.Context) (time.Time, error) {
	secret, err := LookupSecret(ctx, r.client, r.backendSecurityPolicyNamespace, GetBSPSecretName(r.backendSecurityPolicyName))
	if err != nil {
		if apierrors.IsNotFound(err) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	expirationTime, err := GetExpirationSecretAnnotation(secret)
	if err != nil {
		return time.Time{}, err
	}
	preRotationTime := expirationTime.Add(-r.preRotationWindow)
	return preRotationTime, nil
}

func (r *AzureTokenRotator) Rotate(ctx context.Context, _ string) (time.Time, error) {
	policyNamespace := r.backendSecurityPolicyNamespace
	policyName := r.backendSecurityPolicyName
	secretName := GetBSPSecretName(policyName)
	r.logger.Info("rotating Azure access token", "namespace", policyNamespace, "name", policyName)

	// TODO XL think about how and where to pass info to construct policy.RequestTokenOption
	// which only include "scope" info
	// hardcoded here for now, maybe can be passed in from resource via helm chart deployment without code change/binary upgrade
	scopes := []string{"https://cognitiveservices.azure.com/.default"}
	options := policy.TokenRequestOptions{Scopes: scopes}
	azureToken, err := r.azureAuthClient.GetToken(ctx, options)
	if err != nil {
		r.logger.Error(err, "failed to get Azure access token", "scopes", scopes)
		return time.Time{}, err
	}
	secret, err := LookupSecret(ctx, r.client, policyNamespace, secretName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.logger.Info("creating a new azure access token into secret", "namespace", policyNamespace, "name", policyName)
			// store Azure access token into k8s secret
			secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: policyNamespace,
				},
				Type: corev1.SecretTypeOpaque,
				Data: make(map[string][]byte),
			}
			populateAzureAccessToken(secret, azureToken)
			return azureToken.ExpiresOn, r.client.Create(ctx, secret)
		}
		r.logger.Error(err, "failed to lookup azure access token secret", "namespace", policyNamespace, "name", policyName)
		return time.Time{}, err
	}
	r.logger.Info("updating existing azure access token secret", "namespace", policyNamespace, "name", policyName)

	// updateExpirationSecretAnnotation(secret, azureToken.ExpiresOn)

	populateAzureAccessToken(secret, azureToken)
	return azureToken.ExpiresOn, r.client.Update(ctx, secret)
}
