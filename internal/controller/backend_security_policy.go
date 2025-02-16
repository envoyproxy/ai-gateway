// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"golang.org/x/oauth2"
	"k8s.io/apimachinery/pkg/api/errors"
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

// backendSecurityPolicyController implements [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
//
// This handles the BackendSecurityPolicy resource and sends it to the config sink so that it can modify configuration.
type backendSecurityPolicyController struct {
	client         client.Client
	kube           kubernetes.Interface
	logger         logr.Logger
	eventChan      chan ConfigSinkEvent
	oidcTokenCache map[string]*oauth2.Token
}

func newBackendSecurityPolicyController(client client.Client, kube kubernetes.Interface, logger logr.Logger, ch chan ConfigSinkEvent) *backendSecurityPolicyController {
	return &backendSecurityPolicyController{
		client:         client,
		kube:           kube,
		logger:         logger,
		eventChan:      ch,
		oidcTokenCache: make(map[string]*oauth2.Token),
	}
}

// Reconcile implements the [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
func (b *backendSecurityPolicyController) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, err error) {
	var backendSecurityPolicy aigv1a1.BackendSecurityPolicy
	if err = b.client.Get(ctx, req.NamespacedName, &backendSecurityPolicy); err != nil {
		if errors.IsNotFound(err) {
			ctrl.Log.Info("Deleting Backend Security Policy",
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
			rotator, err = rotators.NewAWSOIDCRotator(ctx, b.client, nil, b.kube, b.logger, backendSecurityPolicy.Namespace, backendSecurityPolicy.Name, preRotationWindow, roleArn, region)
			if err != nil {
				return ctrl.Result{}, err
			}
		default:
			err = fmt.Errorf("backend security type %s does not support OIDC token exchange", backendSecurityPolicy.Spec.Type)
			ctrl.Log.Error(err, "namespace", backendSecurityPolicy.Namespace, "name", backendSecurityPolicy.Name)
			return ctrl.Result{}, err
		}

		requeue := time.Minute
		var rotationTime time.Time
		rotationTime, err = rotator.GetPreRotationTime(ctx)
		if err != nil {
			b.logger.Error(err, "failed to get rotation time, retry in one minute")
		} else {
			if rotator.IsExpired(rotationTime) {
				requeue, err = b.rotateCredential(ctx, &backendSecurityPolicy, *oidc, rotator)
				if err != nil {
					b.logger.Error(err, "failed to rotate OIDC exchange token, retry in one minute")
				}
			} else {
				requeue = time.Until(rotationTime)
			}
		}
		res = ctrl.Result{RequeueAfter: requeue}
	}
	// Send the backend security policy to the config sink so that it can modify the configuration together with the state of other resources.
	b.eventChan <- backendSecurityPolicy.DeepCopy()
	return
}

// rotateCredential rotates the credentials using the access token from OIDC provider and return the requeue time for next rotation.
func (b *backendSecurityPolicyController) rotateCredential(ctx context.Context, policy *aigv1a1.BackendSecurityPolicy, oidcCreds egv1a1.OIDC, rotator rotators.Rotator) (time.Duration, error) {
	bspKey := backendSecurityPolicyKey(policy.Namespace, policy.Name)

	var err error
	validToken, ok := b.oidcTokenCache[bspKey]
	if !ok || validToken == nil || rotators.IsBufferedTimeExpired(preRotationWindow, validToken.Expiry) {
		oidcProvider := oauth.NewOIDCProvider(b.client, oidcCreds)
		validToken, err = oidcProvider.FetchToken(ctx)
		if err != nil {
			return time.Minute, err
		}
		b.oidcTokenCache[bspKey] = validToken
	}

	token := validToken.AccessToken
	err = rotator.Rotate(ctx, token)
	if err != nil {
		return time.Minute, err
	}
	rotationTime, err := rotator.GetPreRotationTime(ctx)
	if err != nil {
		return time.Minute, err
	}
	return time.Until(rotationTime), nil
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
