// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller/rotators"
	"github.com/envoyproxy/ai-gateway/internal/controller/tokenprovider"
)

const (
	// clientSecretKey is key used to store Azure and OIDC client secret in Kubernetes secrets.
	clientSecretKey = "client-secret"

	// azureScopeURL specifies Microsoft Azure OAuth 2.0 scope to authenticate and authorize when accessing Azure OpenAI.
	azureScopeURL = "https://cognitiveservices.azure.com/.default"

	// preRotationWindow specifies how long before expiry to rotate credentials.
	// Temporarily a fixed duration.
	preRotationWindow = 5 * time.Minute
)

// BackendSecurityPolicyController implements [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
//
// Exported for testing purposes.
type BackendSecurityPolicyController struct {
	client                    client.Client
	kube                      kubernetes.Interface
	logger                    logr.Logger
	aiServiceBackendEventChan chan event.GenericEvent
}

func NewBackendSecurityPolicyController(client client.Client, kube kubernetes.Interface, logger logr.Logger, aiServiceBackendEventChan chan event.GenericEvent) *BackendSecurityPolicyController {
	return &BackendSecurityPolicyController{
		client:                    client,
		kube:                      kube,
		logger:                    logger,
		aiServiceBackendEventChan: aiServiceBackendEventChan,
	}
}

// Reconcile implements the [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
func (c *BackendSecurityPolicyController) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, err error) {
	var bsp aigv1a1.BackendSecurityPolicy
	if err = c.client.Get(ctx, req.NamespacedName, &bsp); err != nil {
		if apierrors.IsNotFound(err) {
			c.logger.Info("Deleting backend security policy",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	c.logger.Info("Reconciling backend security policy", "namespace", req.Namespace, "name", req.Name)
	res, err = c.reconcile(ctx, &bsp)
	if err != nil {
		c.logger.Error(err, "failed to reconcile backend security policy", "namespace", req.Namespace, "name", req.Name)
		c.updateBackendSecurityPolicyStatus(ctx, &bsp, aigv1a1.ConditionTypeNotAccepted, err.Error())
	} else {
		c.updateBackendSecurityPolicyStatus(ctx, &bsp, aigv1a1.ConditionTypeAccepted, "BackendSecurityPolicy reconciled successfully")
	}
	return
}

// reconcile reconciles BackendSecurityPolicy but extracted from Reconcile to centralize error handling.
func (c *BackendSecurityPolicyController) reconcile(ctx context.Context, bsp *aigv1a1.BackendSecurityPolicy) (res ctrl.Result, err error) {
	if bsp.Spec.Type != aigv1a1.BackendSecurityPolicyTypeAPIKey {
		res, err = c.rotateCredential(ctx, bsp)
		if err != nil {
			return res, err
		}
	}
	err = c.syncBackendSecurityPolicy(ctx, bsp)
	return res, err
}

// rotateCredential rotates the credentials using the access token from OIDC provider and return the requeue time for next rotation.
func (c *BackendSecurityPolicyController) rotateCredential(ctx context.Context, bsp *aigv1a1.BackendSecurityPolicy) (res ctrl.Result, err error) {
	var rotator rotators.Rotator

	switch bsp.Spec.Type {
	case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
		oidc := getBackendSecurityPolicyAuthOIDC(bsp.Spec)
		if oidc != nil {
			region := bsp.Spec.AWSCredentials.Region
			roleArn := bsp.Spec.AWSCredentials.OIDCExchangeToken.AwsRoleArn
			rotator, err = rotators.NewAWSOIDCRotator(ctx, c.client, nil, c.kube, c.logger, bsp.Namespace, bsp.Name, preRotationWindow, *oidc, roleArn, region)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			return ctrl.Result{}, nil
		}
	case aigv1a1.BackendSecurityPolicyTypeAzureCredentials:
		clientID := bsp.Spec.AzureCredentials.ClientID
		tenantID := bsp.Spec.AzureCredentials.TenantID
		var provider tokenprovider.TokenProvider
		options := policy.TokenRequestOptions{Scopes: []string{azureScopeURL}}

		oidc := getBackendSecurityPolicyAuthOIDC(bsp.Spec)
		if oidc != nil {
			var oidcProvider tokenprovider.TokenProvider
			oidcProvider, err = tokenprovider.NewOidcTokenProvider(ctx, c.client, oidc)
			if err != nil {
				return ctrl.Result{}, err
			}
			provider, err = tokenprovider.NewAzureTokenProvider(ctx, tenantID, clientID, oidcProvider, options)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else if secretRef := bsp.Spec.AzureCredentials.ClientSecretRef; secretRef != nil {
			secretNamespace := bsp.Namespace
			if secretRef.Namespace != nil {
				secretNamespace = string(*secretRef.Namespace)
			}
			secretName := string(secretRef.Name)
			var secret *corev1.Secret
			secret, err = rotators.LookupSecret(ctx, c.client, secretNamespace, secretName)
			if err != nil {
				c.logger.Error(err, "failed to lookup azure client secret", "namespace", secretNamespace, "name", secretName)
				return ctrl.Result{}, err
			}
			secretValue, exists := secret.Data[clientSecretKey]
			if !exists {
				return ctrl.Result{}, fmt.Errorf("missing azure client secret key %s", clientSecretKey)
			}
			clientSecret := string(secretValue)
			provider, err = tokenprovider.NewAzureClientSecretTokenProvider(tenantID, clientID, clientSecret, options)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			return ctrl.Result{}, fmt.Errorf("one of secret ref or oidc must be defined, namespace %s name %s", bsp.Namespace, bsp.Name)
		}

		rotator, err = rotators.NewAzureTokenRotator(c.client, c.kube, c.logger, bsp.Namespace, bsp.Name, preRotationWindow, provider)
		if err != nil {
			return ctrl.Result{}, err
		}
	case aigv1a1.BackendSecurityPolicyTypeGCPCredentials:
		if err = validateGCPCredentialsParams(bsp.Spec.GCPCredentials); err != nil {
			return ctrl.Result{}, fmt.Errorf("invalid GCP credentials configuration: %w", err)
		}

		oidc := getBackendSecurityPolicyAuthOIDC(bsp.Spec)

		// Create the OIDC token provider that will be used to get tokens from the OIDC provider
		var oidcProvider tokenprovider.TokenProvider
		oidcProvider, err = tokenprovider.NewOidcTokenProvider(ctx, c.client, oidc)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to initialize OIDC provider: %w", err)
		}
		rotator, err = rotators.NewGCPOIDCTokenRotator(c.client, c.logger, *bsp, preRotationWindow, oidcProvider)
		if err != nil {
			return ctrl.Result{}, err
		}

	default:
		err = fmt.Errorf("backend security type %s does not support OIDC token exchange", bsp.Spec.Type)
		c.logger.Error(err, "unsupported backend security type", "namespace", bsp.Namespace, "name", bsp.Name)
		return ctrl.Result{}, err
	}
	return c.executeRotation(ctx, rotator, bsp)
}

func (c *BackendSecurityPolicyController) executeRotation(ctx context.Context, rotator rotators.Rotator, bsp *aigv1a1.BackendSecurityPolicy) (res ctrl.Result, err error) {
	requeue := time.Minute
	var rotationTime time.Time
	rotationTime, err = rotator.GetPreRotationTime(ctx)
	if err != nil && !apierrors.IsNotFound(err) {
		c.logger.Error(err, "failed to get rotation time, retry in one minute")
	} else {
		if rotator.IsExpired(rotationTime) {
			var expirationTime time.Time
			expirationTime, err = rotator.Rotate(ctx)
			if err != nil {
				c.logger.Error(err, "failed to rotate token, retry in one minute")
			} else {
				rotationTime = expirationTime.Add(-preRotationWindow)
				if r := time.Until(rotationTime); r > 0 {
					requeue = r
					c.logger.Info(
						fmt.Sprintf("successfully rotated credential for %s in namespace %s of auth type %s, renewing in %f minutes",
							bsp.Name, bsp.Namespace, bsp.Spec.Type, requeue.Minutes()))
				} else {
					c.logger.Error(fmt.Errorf("newly rotated credential is already expired %s",
						rotationTime), "namespace", bsp.Namespace, "name", bsp.Name)
				}
			}
		} else {
			requeue = time.Until(rotationTime)
			c.logger.Info(fmt.Sprintf("credentials has not yet expired for %s in namespace %s of auth type %s, renewing in %f minutes",
				bsp.Name, bsp.Namespace, bsp.Spec.Type, requeue.Minutes()))
		}
	}
	return ctrl.Result{RequeueAfter: requeue}, err
}

// getBackendSecurityPolicyAuthOIDC returns the backendSecurityPolicy's OIDC pointer or nil.
func getBackendSecurityPolicyAuthOIDC(spec aigv1a1.BackendSecurityPolicySpec) *egv1a1.OIDC {
	switch spec.Type {
	case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
		if spec.AWSCredentials != nil && spec.AWSCredentials.OIDCExchangeToken != nil {
			return &spec.AWSCredentials.OIDCExchangeToken.OIDC
		}
	case aigv1a1.BackendSecurityPolicyTypeAzureCredentials:
		if spec.AzureCredentials != nil && spec.AzureCredentials.OIDCExchangeToken != nil {
			return &spec.AzureCredentials.OIDCExchangeToken.OIDC
		}
		return nil
	case aigv1a1.BackendSecurityPolicyTypeGCPCredentials:
		if spec.GCPCredentials != nil {
			return &spec.GCPCredentials.WorkLoadIdentityFederationConfig.WorkloadIdentityProvider.OIDCProvider.OIDC
		}
	}
	return nil
}

// backendSecurityPolicyKey returns the key used for indexing and caching the backendSecurityPolicy.
func backendSecurityPolicyKey(namespace, name string) string {
	return fmt.Sprintf("%s.%s", name, namespace)
}

func (c *BackendSecurityPolicyController) syncBackendSecurityPolicy(ctx context.Context, bsp *aigv1a1.BackendSecurityPolicy) error {
	key := backendSecurityPolicyKey(bsp.Namespace, bsp.Name)
	var aiServiceBackends aigv1a1.AIServiceBackendList
	err := c.client.List(ctx, &aiServiceBackends, client.MatchingFields{k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend: key})
	if err != nil {
		return fmt.Errorf("failed to list AIServiceBackendList: %w", err)
	}
	for i := range aiServiceBackends.Items {
		aiBackend := &aiServiceBackends.Items[i]
		c.logger.Info("Syncing AIServiceBackend", "namespace", aiBackend.Namespace, "name", aiBackend.Name)
		c.aiServiceBackendEventChan <- event.GenericEvent{Object: aiBackend}
	}
	return nil
}

// updateBackendSecurityPolicyStatus updates the status of the BackendSecurityPolicy.
func (c *BackendSecurityPolicyController) updateBackendSecurityPolicyStatus(ctx context.Context, route *aigv1a1.BackendSecurityPolicy, conditionType string, message string) {
	route.Status.Conditions = newConditions(conditionType, message)
	if err := c.client.Status().Update(ctx, route); err != nil {
		c.logger.Error(err, "failed to update BackendSecurityPolicy status")
	}
}

func validateGCPCredentialsParams(gcpCreds *aigv1a1.BackendSecurityPolicyGCPCredentials) error {
	if gcpCreds == nil {
		return fmt.Errorf("invalid backend security policy, gcp credentials cannot be nil")
	}
	if gcpCreds.ProjectName == "" {
		return fmt.Errorf("invalid GCP credentials configuration: projectName cannot be empty")
	}
	if gcpCreds.Region == "" {
		return fmt.Errorf("invalid GCP credentials configuration: region cannot be empty")
	}

	wifConfig := gcpCreds.WorkLoadIdentityFederationConfig
	if wifConfig.ProjectID == "" {
		return fmt.Errorf("invalid GCP Workload Identity Federation configuration: projectID cannot be empty")
	}
	if wifConfig.WorkloadIdentityPoolName == "" {
		return fmt.Errorf("invalid GCP Workload Identity Federation configuration: workloadIdentityPoolName cannot be empty")
	}
	if wifConfig.WorkloadIdentityProvider.Name == "" {
		return fmt.Errorf("invalid GCP Workload Identity Federation configuration: workloadIdentityProvider.name cannot be empty")
	}

	return nil
}
