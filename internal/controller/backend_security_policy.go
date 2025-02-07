// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	backendauthrotators "github.com/envoyproxy/ai-gateway/internal/controller/rotators"
)

// backendSecurityPolicyController implements [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
//
// This handles the BackendSecurityPolicy resource and sends it to the config sink so that it can modify configuration.
type backendSecurityPolicyController struct {
	client       client.Client
	kube         kubernetes.Interface
	logger       logr.Logger
	eventChan    chan ConfigSinkEvent
	reconcileAll bool
}

func newBackendSecurityPolicyController(client client.Client, kube kubernetes.Interface, logger logr.Logger, ch chan ConfigSinkEvent) *backendSecurityPolicyController {
	return &backendSecurityPolicyController{
		client:       client,
		kube:         kube,
		logger:       logger,
		eventChan:    ch,
		reconcileAll: true,
	}
}

// Reconcile implements the [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
func (b *backendSecurityPolicyController) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, err error) {
	if b.reconcileAll {
		var backendSecPolicyList aigv1a1.BackendSecurityPolicyList
		err = b.client.List(ctx, &backendSecPolicyList)
		if err != nil {
			b.logger.Error(err, "failed to trigger refresh for existing backendSecPolicy resources")
		} else {
			refreshTime := time.Now().String()
			for _, backendSecurityPolicy := range backendSecPolicyList.Items {
				if isBackendSecurityPolicyAuthOIDC(backendSecurityPolicy.Spec) {
					if len(backendSecurityPolicy.Annotations) == 0 {
						backendSecurityPolicy.Annotations = make(map[string]string)
					}
					backendSecurityPolicy.Annotations["refresh"] = refreshTime
				}
				err = b.client.Update(ctx, &backendSecurityPolicy)
				if err != nil {
					b.logger.Error(err, "failed to trigger refresh for existing backendSecPolicy resource", "name", backendSecurityPolicy.Name)
				}
			}
			b.reconcileAll = false
		}
	}
	var backendSecurityPolicy aigv1a1.BackendSecurityPolicy
	if err = b.client.Get(ctx, req.NamespacedName, &backendSecurityPolicy); err != nil {
		if errors.IsNotFound(err) {
			ctrl.Log.Info("Deleting Backend Security Policy",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if isBackendSecurityPolicyAuthOIDC(backendSecurityPolicy.Spec) {
		var requeue time.Duration
		requeue = time.Minute
		region := backendSecurityPolicy.Spec.AWSCredentials.Region
		rotator, err := backendauthrotators.NewAWSOIDCRotator(b.client, b.kube, b.logger, backendSecurityPolicy.Namespace, backendSecurityPolicy.Name, preRotationWindow, region)
		if err != nil {
			b.logger.Error(err, "failed to create AWS OIDC rotator")
		} else if expired, err := rotator.IsExpired(); err != nil && !expired {
			requeue = time.Until(*rotator.GetPreRotationTime())
			if requeue.Seconds() == 0 {
				requeue = time.Minute
			}
		}
		res = ctrl.Result{RequeueAfter: requeue, Requeue: true}
	}
	// Send the backend security policy to the config sink so that it can modify the configuration together with the state of other resources.
	b.eventChan <- backendSecurityPolicy.DeepCopy()
	return
}

func isBackendSecurityPolicyAuthOIDC(spec aigv1a1.BackendSecurityPolicySpec) bool {
	if spec.AWSCredentials != nil {
		return spec.AWSCredentials.OIDCExchangeToken != nil
	}
	return false
}

func getBackendSecurityPolicyAuthOIDC(spec aigv1a1.BackendSecurityPolicySpec) *egv1a1.OIDC {
	if isBackendSecurityPolicyAuthOIDC(spec) {
		if spec.AWSCredentials.OIDCExchangeToken != nil {
			return &spec.AWSCredentials.OIDCExchangeToken.OIDC
		}
	}
	return nil
}
