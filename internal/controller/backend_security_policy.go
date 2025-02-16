// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"time"

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

// outgoingTimeOut will be used to prevent outgoing request from blocking.
const outGoingTimeOut = time.Minute

// backendSecurityPolicyController implements [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
//
// This handles the BackendSecurityPolicy resource and sends it to the config sink so that it can modify configuration.
type backendSecurityPolicyController struct {
	client         client.Client
	kube           kubernetes.Interface
	logger         logr.Logger
	eventChan      chan ConfigSinkEvent
	StsClient      rotators.STSClient
	oidcTokenCache map[string]*oauth2.Token
}

func newBackendSecurityPolicyController(client client.Client, stsClient rotators.STSClient, kube kubernetes.Interface, logger logr.Logger, ch chan ConfigSinkEvent) *backendSecurityPolicyController {
	return &backendSecurityPolicyController{
		client:         client,
		kube:           kube,
		logger:         logger,
		eventChan:      ch,
		StsClient:      stsClient,
		oidcTokenCache: make(map[string]*oauth2.Token),
	}
}

// Reconcile implements the [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
func (b *backendSecurityPolicyController) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, err error) {
	var backendSecurityPolicy aigv1a1.BackendSecurityPolicy
	if err := b.client.Get(ctx, req.NamespacedName, &backendSecurityPolicy); err != nil {
		if errors.IsNotFound(err) {
			ctrl.Log.Info("Deleting Backend Security Policy",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if backendSecurityPolicy.Spec.AWSCredentials != nil && backendSecurityPolicy.Spec.AWSCredentials.OIDCExchangeToken != nil {
		rotator, err := rotators.NewAWSOIDCRotator(ctx, b.client, b.StsClient, b.kube, b.logger, backendSecurityPolicy.Namespace,
			backendSecurityPolicy.Name, preRotationWindow, backendSecurityPolicy.Spec.AWSCredentials.Region, backendSecurityPolicy.Spec.AWSCredentials.OIDCExchangeToken.AwsRoleArn)
		if err != nil {
			b.logger.Error(err, "failed to create AWS OIDC rotator")
			return ctrl.Result{}, err
		}
		var requeue time.Duration
		requeue = time.Minute
		preRotationExpirationTime, err := rotator.GetPreRotationTime(ctx)
		if err != nil {
			return ctrl.Result{}, err
		}
		if rotator.IsExpired(preRotationExpirationTime) {
			err := b.rotateCredential(ctx, rotator, backendSecurityPolicy)
			if err != nil {
				b.logger.Error(err, "failed to rotate OIDC exchange token, retry in one minute")
				requeue = time.Minute
			} else {
				preRotationExpirationTime, err = rotator.GetPreRotationTime(ctx)
				if err != nil {
					return ctrl.Result{}, err
				}
				requeue = time.Until(preRotationExpirationTime)
			}
		}
		res = ctrl.Result{RequeueAfter: requeue}
	}
	// Send the backend security policy to the config sink so that it can modify the configuration together with the state of other resources.
	b.eventChan <- backendSecurityPolicy.DeepCopy()
	return
}

func (b *backendSecurityPolicyController) rotateCredential(ctx context.Context, rotator *rotators.AWSOIDCRotator, policy aigv1a1.BackendSecurityPolicy) error {
	bspKey := backendSecurityPolicyKey(policy.Namespace, policy.Name)
	var validToken *oauth2.Token
	var err error
	if tokenResponse, ok := b.oidcTokenCache[bspKey]; !ok || rotators.IsBufferedTimeExpired(preRotationWindow, tokenResponse.Expiry) {
		oidcProvider := oauth.NewOIDCProvider(oauth.NewClientCredentialsProvider(b.client, policy.Spec.AWSCredentials.OIDCExchangeToken.OIDC), policy.Spec.AWSCredentials.OIDCExchangeToken.OIDC)
		// Valid Token will be nil if fetch token errors.

		timeOutCtx, cancelFunc := context.WithTimeout(ctx, outGoingTimeOut)
		defer cancelFunc()
		validToken, err = oidcProvider.FetchToken(timeOutCtx)
		if err != nil {
			b.logger.Error(err, "failed to fetch OIDC provider token")
			return err
		}
		b.oidcTokenCache[bspKey] = validToken
	} else {
		validToken = tokenResponse
	}

	if validToken != nil {
		b.oidcTokenCache[bspKey] = validToken
		// Set a timeout for rotate.
		timeOutCtx, cancelRotateFunc := context.WithTimeout(ctx, outGoingTimeOut)
		defer cancelRotateFunc()
		token := validToken.AccessToken
		return rotator.Rotate(timeOutCtx, token)
	}
	return nil
}

// backendSecurityPolicyKey returns the key used for indexing and caching the backendSecurityPolicy
func backendSecurityPolicyKey(namespace, name string) string {
	return fmt.Sprintf("%s.%s", name, namespace)
}
