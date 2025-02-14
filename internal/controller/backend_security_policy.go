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
	StsOP          rotators.STSClient
	oidcTokenCache map[string]*oauth2.Token
}

func newBackendSecurityPolicyController(client client.Client, kube kubernetes.Interface, logger logr.Logger, ch chan ConfigSinkEvent) *backendSecurityPolicyController {
	return &backendSecurityPolicyController{
		client:         client,
		kube:           kube,
		logger:         logger,
		eventChan:      ch,
		StsOP:          nil,
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
		var requeue time.Duration
		requeue = time.Minute
		region := backendSecurityPolicy.Spec.AWSCredentials.Region

		rotator, err := rotators.NewAWSOIDCRotator(ctx, b.client, b.kube, b.logger, backendSecurityPolicy.Namespace, backendSecurityPolicy.Name, preRotationWindow, region)
		if err != nil {
			b.logger.Error(err, "failed to create AWS OIDC rotator")
		} else if rotator.IsExpired() {
			bspKey := fmt.Sprintf("%s.%s", backendSecurityPolicy.Name, backendSecurityPolicy.Namespace)

			var validToken *oauth2.Token
			if tokenResponse, ok := b.oidcTokenCache[bspKey]; !ok || rotators.IsExpired(preRotationWindow, tokenResponse.Expiry) {
				oidcProvider := oauth.NewOIDCProvider(oauth.NewClientCredentialsProvider(b.client), oidc)
				// Valid Token will be nil if fetch token errors.

				timeOutCtx, cancelFunc := context.WithTimeout(ctx, outGoingTimeOut)
				defer cancelFunc()
				validToken, err = oidcProvider.FetchToken(timeOutCtx)
				if err != nil {
					b.logger.Error(err, "failed to fetch OIDC provider token")
				} else {
					b.oidcTokenCache[bspKey] = validToken
				}
			} else {
				validToken = tokenResponse
			}

			if validToken != nil {
				b.oidcTokenCache[bspKey] = validToken
				awsCredentials := backendSecurityPolicy.Spec.AWSCredentials

				// This is to abstract the real STS behavior for testing purpose.
				if b.StsOP != nil {
					rotator.SetSTSOperations(b.StsOP)
				}

				// Set a timeout for rotate.
				timeOutCtx, cancelFunc2 := context.WithTimeout(ctx, outGoingTimeOut)
				defer cancelFunc2()
				rotator.UpdateCtx(timeOutCtx)
				token := validToken.AccessToken
				err = rotator.Rotate(awsCredentials.Region, awsCredentials.OIDCExchangeToken.AwsRoleArn, token)
				if err != nil {
					b.logger.Error(err, "failed to rotate AWS OIDC exchange token")
					requeue = time.Minute
				} else {
					requeue = time.Until(rotator.GetPreRotationTime())
				}

			}
		}
		// TODO: Investigate how to stop stale events from re-queuing.
		res = ctrl.Result{RequeueAfter: requeue}
	}
	// Send the backend security policy to the config sink so that it can modify the configuration together with the state of other resources.
	b.eventChan <- backendSecurityPolicy.DeepCopy()
	return
}

// getBackendSecurityPolicyAuthOIDC returns the backendSecurityPolicy's OIDC pointer or nil.
func getBackendSecurityPolicyAuthOIDC(spec aigv1a1.BackendSecurityPolicySpec) *egv1a1.OIDC {
	if spec.AWSCredentials != nil && spec.AWSCredentials.OIDCExchangeToken != nil {
		return &spec.AWSCredentials.OIDCExchangeToken.OIDC
	}
	return nil
}
