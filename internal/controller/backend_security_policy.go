// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller/oauth"
	"github.com/envoyproxy/ai-gateway/internal/controller/rotators"
)

// preRotationWindow specifies how long before expiry to rotate credentials.
// Temporarily a fixed duration.
const preRotationWindow = 5 * time.Minute

type TokenExpiry struct {
	Token     string
	ExpiresAt time.Time
}

// BackendSecurityPolicyController implements [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
//
// Exported for testing purposes.
type BackendSecurityPolicyController struct {
	client               client.Client
	kube                 kubernetes.Interface
	logger               logr.Logger
	tokenCache           map[string]TokenExpiry
	cacheMutex           sync.RWMutex
	syncAIServiceBackend syncAIServiceBackendFn
}

func NewBackendSecurityPolicyController(client client.Client, kube kubernetes.Interface, logger logr.Logger, syncAIServiceBackend syncAIServiceBackendFn) *BackendSecurityPolicyController {
	return &BackendSecurityPolicyController{
		client:               client,
		kube:                 kube,
		logger:               logger,
		tokenCache:           make(map[string]TokenExpiry),
		syncAIServiceBackend: syncAIServiceBackend,
	}
}

// Reconcile implements the [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
func (c *BackendSecurityPolicyController) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, err error) {
	var bsp aigv1a1.BackendSecurityPolicy
	if err = c.client.Get(ctx, req.NamespacedName, &bsp); err != nil {
		if apierrors.IsNotFound(err) {
			c.logger.Info("Deleting Backend Security Policy",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	c.logger.Info("Reconciling Backend Security Policy", "namespace", req.Namespace, "name", req.Name)
	res, err = c.reconcile(ctx, &bsp)
	if err != nil {
		c.logger.Error(err, "failed to reconcile Backend Security Policy")
		c.updateBackendSecurityPolicyStatus(ctx, &bsp, aigv1a1.ConditionTypeNotAccepted, err.Error())
	} else {
		c.updateBackendSecurityPolicyStatus(ctx, &bsp, aigv1a1.ConditionTypeAccepted, "BackendSecurityPolicy reconciled successfully")
	}
	return
}

// reconcile reconciles BackendSecurityPolicy but extracted from Reconcile to centralize error handling.
func (c *BackendSecurityPolicyController) reconcile(ctx context.Context, bsp *aigv1a1.BackendSecurityPolicy) (res ctrl.Result, err error) {
	pName := bsp.Name
	pType := bsp.Spec.Type
	pNameSpace := bsp.Namespace

	var rotator rotators.Rotator

	switch bsp.Spec.Type {
	case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
		region := bsp.Spec.AWSCredentials.Region
		roleArn := bsp.Spec.AWSCredentials.OIDCExchangeToken.AwsRoleArn
		rotator, err = rotators.NewAWSOIDCRotator(ctx, c.client, nil, c.kube, c.logger, pNameSpace, pName, preRotationWindow, roleArn, region)
		if err != nil {
			return ctrl.Result{}, err
		}

	case aigv1a1.BackendSecurityPolicyTypeAzureCredentials:
		c.logger.Info(fmt.Sprintf("creating azure rotator %s ", bsp.Spec.Type))
		clientID := bsp.Spec.AzureCredentials.ClientID
		tenantID := bsp.Spec.AzureCredentials.TenantID
		clientSecretRef := bsp.Spec.AzureCredentials.ClientSecretRef
		// TODO XL to check
		if clientSecretRef.Namespace == nil {
			return ctrl.Result{}, errors.New("missing client secret ref")
		}
		secretNamespace := string(*clientSecretRef.Namespace)
		secretName := string(clientSecretRef.Name)
		var secret *corev1.Secret
		secret, err = rotators.LookupSecret(ctx, c.client, secretNamespace, secretName)
		if err != nil {
			c.logger.Error(err, "failed to lookup client secret", "namespace", secretNamespace, "name", secretName)
			return ctrl.Result{}, err
		}
		secretValue, exists := secret.Data[rotators.AzureAccessTokenKey]
		if !exists {
			return ctrl.Result{}, errors.New("missing azure access token")
		}
		azureClientSecret := string(secretValue)
		rotator, err = rotators.NewAzureTokenRotator(c.client, c.kube, c.logger, pNameSpace, pName, preRotationWindow, clientID, tenantID, azureClientSecret)
		if err != nil {
			return ctrl.Result{}, err
		}
	case aigv1a1.BackendSecurityPolicyTypeAPIKey:
		// maintain original logic.
		// TODO XL question: is original logic correct though - when oidc is nil, syncBackendSecurityPolicy immediately?
		return res, c.syncBackendSecurityPolicy(ctx, bsp)
	default:
		err = fmt.Errorf("backend security type %s is not supported", pType)
		c.logger.Error(err, "namespace", pNameSpace, "name", pName)
		return ctrl.Result{}, err
	}

	duration := time.Minute
	var rotationTime time.Time
	rotationTime, err = rotator.GetPreRotationTime(ctx)
	if err != nil {
		c.logger.Error(err, fmt.Sprintf("failed to get credentials rotation time for %s in namespace %s of auth type %s,  retry in one minute", pName, pNameSpace, pType))
	} else {
		if rotator.IsExpired(rotationTime) {
			c.logger.Info(fmt.Sprintf("credentials for %s in namespace %s of auth type %s is expired", pName, pNameSpace, pType))
			duration, err = c.rotateCredential(ctx, bsp, rotator)
			if err != nil {
				c.logger.Error(err, fmt.Sprintf("failed to rotate credentials for %s in namespace %s of auth type %s, retry in one minute", pName, pNameSpace, pType))
			} else {
				c.logger.Info(fmt.Sprintf("rotated credentials for %s in namespace %s of auth type %s, next rotation will happen in %f minutes", pName, pNameSpace, pType, duration.Minutes()))
			}
		} else {
			duration = time.Until(rotationTime)
		}
	}
	c.logger.Info(fmt.Sprintf("requene credentials for %s in namespace %s of auth type %s in %f minutes", pName, pNameSpace, pType, duration.Minutes()))
	res = ctrl.Result{RequeueAfter: duration}
	return res, c.syncBackendSecurityPolicy(ctx, bsp)
}

// rotateCredential rotates the credentials using the access token from OIDC provider and return the requeue time for next rotation.
func (c *BackendSecurityPolicyController) rotateCredential(ctx context.Context, policy *aigv1a1.BackendSecurityPolicy, rotator rotators.Rotator) (time.Duration, error) {
	pNamespace := policy.Namespace
	pName := policy.Name

	key := backendSecurityPolicyKey(pNamespace, pName)

	c.cacheMutex.RLock()
	tokenExpiry, exists := c.tokenCache[key]
	c.cacheMutex.RUnlock()

	var tokenValue string
	expired := rotators.IsBufferedTimeExpired(preRotationWindow, tokenExpiry.ExpiresAt)
	if !exists || expired {
		if !exists {
			c.logger.Info(fmt.Sprintf("cache does not have token, key %s", key))
		}
		if expired {
			c.logger.Info(fmt.Sprintf("token from cache expired, key %s", key))
		}
		// generate new token via oidc
		oidc := getAuthOIDC(policy.Spec)
		if oidc != nil {
			provider := oauth.NewOIDCProvider(c.client, *oidc)
			oauthToken, err := provider.FetchToken(ctx)
			if err != nil {
				// it's possible oidc is nil
				c.logger.Error(err, fmt.Sprintf("failed to fetch token via OIDC provider for policy name %s in namespace %s", pName, pNamespace))
				return time.Minute, err
			}
			c.logger.Info(fmt.Sprintf("fetched token via OIDC provider for policy name %s in namespace %s", pName, pNamespace))
			tokenExpiry = TokenExpiry{Token: oauthToken.AccessToken, ExpiresAt: oauthToken.Expiry}
			c.cacheMutex.Lock()
			c.logger.Info(fmt.Sprintf("save token expiry to cache, cache key %s", key))
			c.tokenCache[key] = tokenExpiry
			c.cacheMutex.Unlock()
			tokenValue = tokenExpiry.Token
		}
		// in azure case, no oidc, rotate accept empty tokenValue
	}
	expiration, err := rotator.Rotate(ctx, tokenValue)
	if err != nil {
		c.logger.Error(err, fmt.Sprintf("failed to rotate token for policy name %s in namespace %s", pName, pNamespace))
		return time.Minute, err
	}
	rotationTime := expiration.Add(-preRotationWindow)
	if duration := time.Until(rotationTime); duration > 0 {
		return duration, nil
	}
	return time.Minute, fmt.Errorf("newly rotated credentials is already expired (%v) for policy name %s in namespace %s", rotationTime, pName, pNamespace)
}

// getBackendSecurityPolicyAuthOIDC returns the backendSecurityPolicy's OIDC pointer or nil.
func getAuthOIDC(spec aigv1a1.BackendSecurityPolicySpec) *egv1a1.OIDC {
	// Currently only AWS support OIDC.
	switch spec.Type {
	case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
		if spec.AWSCredentials != nil && spec.AWSCredentials.OIDCExchangeToken != nil {
			return &spec.AWSCredentials.OIDCExchangeToken.OIDC
		}
	case aigv1a1.BackendSecurityPolicyTypeAzureCredentials:
		return nil
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

	var errs []error
	for i := range aiServiceBackends.Items {
		aiBackend := &aiServiceBackends.Items[i]
		c.logger.Info("Syncing AIServiceBackend", "namespace", aiBackend.Namespace, "name", aiBackend.Name)
		if err = c.syncAIServiceBackend(ctx, aiBackend); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", aiBackend.Namespace, aiBackend.Name, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
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
