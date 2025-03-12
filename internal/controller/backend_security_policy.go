// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
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
	syncAIServiceBackend syncAIServiceBackendFn
}

func NewBackendSecurityPolicyController(client client.Client, kube kubernetes.Interface, logger logr.Logger, syncAIServiceBackend syncAIServiceBackendFn) *BackendSecurityPolicyController {
	return &BackendSecurityPolicyController{
		client:               client,
		kube:                 kube,
		logger:               logger,
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

	c.logger.Info("Reconciling Backend Security Policy", "namespace", req.Namespace, "name", req.Name)
	res, err = c.reconcile(ctx, &backendSecurityPolicy)
	if err != nil {
		c.logger.Error(err, "failed to reconcile Backend Security Policy")
		c.updateBackendSecurityPolicyStatus(ctx, &backendSecurityPolicy, aigv1a1.ConditionTypeNotAccepted, err.Error())
	} else {
		c.updateBackendSecurityPolicyStatus(ctx, &backendSecurityPolicy, aigv1a1.ConditionTypeAccepted, "BackendSecurityPolicy reconciled successfully")
	}
	return
}

// reconcile reconciles BackendSecurityPolicy but extracted from Reconcile to centralize error handling.
func (c *BackendSecurityPolicyController) reconcile(ctx context.Context, backendSecurityPolicy *aigv1a1.BackendSecurityPolicy) (res ctrl.Result, err error) {
	if backendSecurityPolicy.Spec.Type != aigv1a1.BackendSecurityPolicyTypeAPIKey {
		res, err = c.rotateCredential(ctx, *backendSecurityPolicy)
		if err != nil {
			return res, err
		}
	}
	err = c.syncBackendSecurityPolicy(ctx, backendSecurityPolicy)
	return res, err
}

// getBackendSecurityPolicyAuthOIDC returns the backendSecurityPolicy's OIDC pointer or nil.
func getBackendSecurityPolicyAuthOIDC(spec aigv1a1.BackendSecurityPolicySpec) *egv1a1.OIDC {
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

func (c *BackendSecurityPolicyController) rotateCredential(ctx context.Context, backendSecurityPolicy aigv1a1.BackendSecurityPolicy) (res ctrl.Result, err error) {
	var rotator rotators.Rotator
	switch backendSecurityPolicy.Spec.Type {
	case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
		if oidc := getBackendSecurityPolicyAuthOIDC(backendSecurityPolicy.Spec); oidc != nil {
			region := backendSecurityPolicy.Spec.AWSCredentials.Region
			roleArn := backendSecurityPolicy.Spec.AWSCredentials.OIDCExchangeToken.AwsRoleArn
			rotator, err = rotators.NewAWSOIDCRotator(ctx, c.client, nil, c.kube, c.logger, backendSecurityPolicy.Namespace, backendSecurityPolicy.Name,
				preRotationWindow, *oidc, roleArn, region)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			return ctrl.Result{}, nil
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
			var expirationTime time.Time
			expirationTime, err = rotator.Rotate(ctx)
			if err != nil {
				c.logger.Error(err, "failed to rotate OIDC exchange token, retry in one minute")
			} else {
				rotationTime = expirationTime.Add(-preRotationWindow)
				if r := time.Until(rotationTime); r > 0 {
					requeue = r
					c.logger.Info(
						fmt.Sprintf("successfully rotated credential for %s in namespace %s of auth type %s, renewing in %f minutes",
							backendSecurityPolicy.Name, backendSecurityPolicy.Namespace, backendSecurityPolicy.Spec.Type, requeue.Minutes()))
				} else {
					c.logger.Error(fmt.Errorf("newly rotated credential is already expired %s",
						rotationTime), "namespace", backendSecurityPolicy.Namespace, "name", backendSecurityPolicy.Name)
				}

			}
		} else {
			requeue = time.Until(rotationTime)
			c.logger.Info(fmt.Sprintf("credentials has not yet expired for %s in namespace %s of auth type %s, renewing in %f minutes",
				backendSecurityPolicy.Name, backendSecurityPolicy.Namespace, backendSecurityPolicy.Spec.Type, requeue.Minutes()))
		}
	}
	return ctrl.Result{RequeueAfter: requeue}, err
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
