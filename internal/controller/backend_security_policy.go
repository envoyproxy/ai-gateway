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
	"golang.org/x/oauth2"
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

// BackendSecurityPolicyController implements [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
//
// Exported for testing purposes.
type BackendSecurityPolicyController struct {
	client               client.Client
	kube                 kubernetes.Interface
	logger               logr.Logger
	oidcTokenCache       map[string]*oauth2.Token
	oidcTokenCacheMutex  sync.RWMutex
	syncAIServiceBackend syncAIServiceBackendFn
}

func NewBackendSecurityPolicyController(client client.Client, kube kubernetes.Interface, logger logr.Logger, syncAIServiceBackend syncAIServiceBackendFn) *BackendSecurityPolicyController {
	return &BackendSecurityPolicyController{
		client:               client,
		kube:                 kube,
		logger:               logger,
		oidcTokenCache:       make(map[string]*oauth2.Token),
		syncAIServiceBackend: syncAIServiceBackend,
	}
}

// Reconcile implements the [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
func (c *BackendSecurityPolicyController) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, err error) {
	var backendSecurityPolicy aigv1a1.BackendSecurityPolicy
	if err = c.client.Get(ctx, req.NamespacedName, &backendSecurityPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			c.logger.Info("Deleting Backend Security Policy",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if oidc := getBackendSecurityPolicyAuthOIDC(backendSecurityPolicy.Spec); oidc != nil {
		var rotator rotators.Rotator
		switch backendSecurityPolicy.Spec.Type {
		case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
			region := backendSecurityPolicy.Spec.AWSCredentials.Region
			roleArn := backendSecurityPolicy.Spec.AWSCredentials.OIDCExchangeToken.AwsRoleArn
			rotator, err = rotators.NewAWSOIDCRotator(ctx, c.client, nil, c.kube, c.logger, backendSecurityPolicy.Namespace, backendSecurityPolicy.Name, preRotationWindow, roleArn, region)
			if err != nil {
				return ctrl.Result{}, err
			}
		default:
			err = fmt.Errorf("backend security type %s does not support OIDC token exchange", backendSecurityPolicy.Spec.Type)
			c.logger.Error(err, "namespace", backendSecurityPolicy.Namespace, "name", backendSecurityPolicy.Name)
			return ctrl.Result{}, err
		}

		requeue := time.Minute
		var rotationTime time.Time
		rotationTime, err = rotator.GetPreRotationTime(ctx)
		if err != nil {
			c.logger.Error(err, "failed to get rotation time, retry in one minute")
		} else {
			if rotator.IsExpired(rotationTime) {
				requeue, err = c.rotateCredential(ctx, &backendSecurityPolicy, *oidc, rotator)
				if err != nil {
					c.logger.Error(err, "failed to rotate OIDC exchange token, retry in one minute")
				} else {
					c.logger.Info(
						fmt.Sprintf("successfully rotated credentials for %s in namespace %s of auth type %s, renewing in %f minutes",
							req.Name, req.Namespace, backendSecurityPolicy.Spec.Type, requeue.Minutes()))
				}
			} else {
				requeue = time.Until(rotationTime)
			}
		}
		res = ctrl.Result{RequeueAfter: requeue}
	}
	return res, c.syncBackendSecurityPolicy(ctx, &backendSecurityPolicy)
}

// rotateCredential rotates the credentials using the access token from OIDC provider and return the requeue time for next rotation.
func (c *BackendSecurityPolicyController) rotateCredential(ctx context.Context, policy *aigv1a1.BackendSecurityPolicy, oidcCreds egv1a1.OIDC, rotator rotators.Rotator) (time.Duration, error) {
	bspKey := backendSecurityPolicyKey(policy.Namespace, policy.Name)
	var err error
	c.oidcTokenCacheMutex.RLock()
	validToken, ok := c.oidcTokenCache[bspKey]
	c.oidcTokenCacheMutex.RUnlock()
	if !ok || validToken == nil || rotators.IsBufferedTimeExpired(preRotationWindow, validToken.Expiry) {
		oidcProvider := oauth.NewOIDCProvider(c.client, oidcCreds)
		validToken, err = oidcProvider.FetchToken(ctx)
		if err != nil {
			return time.Minute, err
		}
		c.oidcTokenCacheMutex.Lock()
		c.oidcTokenCache[bspKey] = validToken
		c.oidcTokenCacheMutex.Unlock()
	}

	token := validToken.AccessToken
	expiration, err := rotator.Rotate(ctx, token)
	if err != nil {
		return time.Minute, err
	}
	rotationTime := expiration.Add(-preRotationWindow)
	if requeue := time.Until(rotationTime); requeue > 0 {
		return requeue, nil
	}
	return time.Minute, fmt.Errorf("newly rotate credentials is already expired (%v) for policy %s in %s", rotationTime, policy.Name, policy.Namespace)
}

// getBackendSecurityPolicyAuthOIDC returns the backendSecurityPolicy's OIDC pointer or nil.
func getBackendSecurityPolicyAuthOIDC(spec aigv1a1.BackendSecurityPolicySpec) *egv1a1.OIDC {
	// Currently only supports AWS.
	switch spec.Type {
	case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
		if spec.AWSCredentials != nil && spec.AWSCredentials.OIDCExchangeToken != nil {
			return &spec.AWSCredentials.OIDCExchangeToken.OIDC
		}
	default:
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
