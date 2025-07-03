// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gwaiev1a2 "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1b1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(Scheme))
	utilruntime.Must(aigv1a1.AddToScheme(Scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(Scheme))
	utilruntime.Must(egv1a1.AddToScheme(Scheme))
	utilruntime.Must(gwapiv1.Install(Scheme))
	utilruntime.Must(gwapiv1b1.Install(Scheme))
	utilruntime.Must(gwaiev1a2.Install(Scheme))
}

// Scheme contains the necessary schemes for the AI Gateway.
//
// This is exported for testing purposes.
var Scheme = runtime.NewScheme()

// Options defines the program configurable options that may be passed on the command line.
type Options struct {
	// ExtProcLogLevel is the log level for the external processor, e.g., debug, info, warn, or error.
	ExtProcLogLevel string
	// ExtProcImage is the image for the external processor set on Deployment.
	ExtProcImage string
	// ExtProcImagePullPolicy is the image pull policy for the external processor set on Deployment.
	ExtProcImagePullPolicy corev1.PullPolicy
	// EnableLeaderElection enables leader election for the controller manager.
	// Enabling this ensures there is only one active controller manager.
	EnableLeaderElection bool
	// EnvoyGatewayNamespace is the namespace where the Envoy Gateway system resources are deployed.
	EnvoyGatewayNamespace string
	// UDSPath is the path to the UDS socket for the external processor.
	UDSPath string
	// DisableMutatingWebhook disables the mutating webhook for the Gateway for testing purposes.
	DisableMutatingWebhook bool
}

// StartControllers starts the controllers for the AI Gateway.
// This blocks until the manager is stopped.
//
// Note: this is tested with envtest, hence the test exists outside of this package. See /tests/controller_test.go.
func StartControllers(ctx context.Context, mgr manager.Manager, config *rest.Config, logger logr.Logger, options Options) (err error) {
	c := mgr.GetClient()
	indexer := mgr.GetFieldIndexer()
	if err = ApplyIndexing(ctx, indexer.IndexField); err != nil {
		return fmt.Errorf("failed to apply indexing: %w", err)
	}

	gatewayEventChan := make(chan event.GenericEvent, 100)
	gatewayC := NewGatewayController(c, kubernetes.NewForConfigOrDie(config),
		logger.WithName("gateway"), options.EnvoyGatewayNamespace, options.UDSPath, options.ExtProcImage)
	if err = TypedControllerBuilderForCRD(mgr, &gwapiv1.Gateway{}).
		// We need the annotation change event to reconcile the Gateway referenced by AIGatewayRoutes.
		WithEventFilter(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{})).
		WatchesRawSource(source.Channel(
			gatewayEventChan,
			&handler.EnqueueRequestForObject{},
		)).
		Complete(gatewayC); err != nil {
		return fmt.Errorf("failed to create controller for Gateway: %w", err)
	}

	aiGatewayRouteEventChan := make(chan event.GenericEvent, 100)
	routeC := NewAIGatewayRouteController(c, kubernetes.NewForConfigOrDie(config), logger.WithName("ai-gateway-route"),
		gatewayEventChan,
	)
	if err = TypedControllerBuilderForCRD(mgr, &aigv1a1.AIGatewayRoute{}).
		Owns(&egv1a1.EnvoyExtensionPolicy{}).
		Owns(&gwapiv1.HTTPRoute{}).
		WatchesRawSource(source.Channel(
			aiGatewayRouteEventChan,
			&handler.EnqueueRequestForObject{},
		)).
		Complete(routeC); err != nil {
		return fmt.Errorf("failed to create controller for AIGatewayRoute: %w", err)
	}

	aiServiceBackendEventChan := make(chan event.GenericEvent, 100)
	backendC := NewAIServiceBackendController(c, kubernetes.NewForConfigOrDie(config), logger.
		WithName("ai-service-backend"), aiGatewayRouteEventChan)
	if err = TypedControllerBuilderForCRD(mgr, &aigv1a1.AIServiceBackend{}).
		WatchesRawSource(source.Channel(
			aiServiceBackendEventChan,
			&handler.EnqueueRequestForObject{},
		)).
		Complete(backendC); err != nil {
		return fmt.Errorf("failed to create controller for AIServiceBackend: %w", err)
	}

	backendSecurityPolicyEventChan := make(chan event.GenericEvent, 100)
	backendSecurityPolicyC := NewBackendSecurityPolicyController(c, kubernetes.NewForConfigOrDie(config), logger.
		WithName("backend-security-policy"), aiServiceBackendEventChan)
	if err = TypedControllerBuilderForCRD(mgr, &aigv1a1.BackendSecurityPolicy{}).
		WatchesRawSource(source.Channel(
			backendSecurityPolicyEventChan,
			&handler.EnqueueRequestForObject{},
		)).
		Complete(backendSecurityPolicyC); err != nil {
		return fmt.Errorf("failed to create controller for BackendSecurityPolicy: %w", err)
	}

	secretC := NewSecretController(c, kubernetes.NewForConfigOrDie(config), logger.
		WithName("secret"), backendSecurityPolicyEventChan)
	// Do not use TypedControllerBuilderForCRD for secret, as changing a secret content doesn't change the generation.
	if err = ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		Complete(secretC); err != nil {
		return fmt.Errorf("failed to create controller for Secret: %w", err)
	}

	if !options.DisableMutatingWebhook {
		h := admission.WithCustomDefaulter(Scheme, &corev1.Pod{}, newGatewayMutator(c, kubernetes.NewForConfigOrDie(config),
			logger.WithName("gateway-mutator"),
			options.ExtProcImage,
			options.ExtProcImagePullPolicy,
			options.ExtProcLogLevel,
			options.EnvoyGatewayNamespace,
			options.UDSPath,
		))
		mgr.GetWebhookServer().Register("/mutate", &webhook.Admission{Handler: h})
	}

	if err = mgr.Start(ctx); err != nil { // This blocks until the manager is stopped.
		return fmt.Errorf("failed to start controller manager: %w", err)
	}
	return nil
}

// TypedControllerBuilderForCRD returns a new controller builder for the given CRD object type.
//
// This is to share the common logic for setting up a controller for a given object type.
//
// Exported for testing purposes in tests/controller_test.go.
func TypedControllerBuilderForCRD(mgr ctrl.Manager, obj client.Object) *ctrl.Builder {
	return ctrl.NewControllerManagedBy(mgr).
		For(obj).
		// We do not need to watch for changes in the status subresource.
		WithEventFilter(predicate.GenerationChangedPredicate{})
}

const (
	// k8sClientIndexAIGatewayRouteToAttachedGateway is the index name that maps from a Gateway to the
	// AIGatewayRoute that attaches to it.
	k8sClientIndexAIGatewayRouteToAttachedGateway = "GWAPIGatewayToReferencingAIGatewayRoute"
	// k8sClientIndexSecretToReferencingBackendSecurityPolicy is the index name that maps
	// from a Secret to the BackendSecurityPolicy that references it.
	k8sClientIndexSecretToReferencingBackendSecurityPolicy = "SecretToReferencingBackendSecurityPolicy"
	// k8sClientIndexBackendToReferencingAIGatewayRoute is the index name that maps from a Backend to the
	// AIGatewayRoute that references it.
	k8sClientIndexBackendToReferencingAIGatewayRoute = "BackendToReferencingAIGatewayRoute"
	// k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend is the index name that maps from a BackendSecurityPolicy
	// to the AIServiceBackend that references it.
	k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend = "BackendSecurityPolicyToReferencingAIServiceBackend"
)

// ApplyIndexing applies indexing to the given indexer. This is exported for testing purposes.
func ApplyIndexing(ctx context.Context, indexer func(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error) error {
	err := indexer(ctx, &aigv1a1.AIGatewayRoute{},
		k8sClientIndexBackendToReferencingAIGatewayRoute, aiGatewayRouteIndexFunc)
	if err != nil {
		return fmt.Errorf("failed to create index from Backends to AIGatewayRoute: %w", err)
	}
	err = indexer(ctx, &aigv1a1.AIGatewayRoute{},
		k8sClientIndexAIGatewayRouteToAttachedGateway, aiGatewayRouteToAttachedGatewayIndexFunc)
	if err != nil {
		return fmt.Errorf("failed to create index from Gateway to AIGatewayRoute: %w", err)
	}
	err = indexer(ctx, &aigv1a1.AIServiceBackend{},
		k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend, aiServiceBackendIndexFunc)
	if err != nil {
		return fmt.Errorf("failed to create index from BackendSecurityPolicy to AIServiceBackend: %w", err)
	}
	err = indexer(ctx, &aigv1a1.BackendSecurityPolicy{},
		k8sClientIndexSecretToReferencingBackendSecurityPolicy, backendSecurityPolicyIndexFunc)
	if err != nil {
		return fmt.Errorf("failed to create index from Secret to BackendSecurityPolicy: %w", err)
	}
	return nil
}

func aiGatewayRouteToAttachedGatewayIndexFunc(o client.Object) []string {
	aiGatewayRoute := o.(*aigv1a1.AIGatewayRoute)
	var ret []string
	for _, ref := range aiGatewayRoute.Spec.TargetRefs { // TODO: handle parentRefs per #580.
		ret = append(ret, fmt.Sprintf("%s.%s", ref.Name, aiGatewayRoute.Namespace))
	}
	return ret
}

func aiGatewayRouteIndexFunc(o client.Object) []string {
	aiGatewayRoute := o.(*aigv1a1.AIGatewayRoute)
	var ret []string
	for _, rule := range aiGatewayRoute.Spec.Rules {
		for _, backend := range rule.BackendRefs {
			key := fmt.Sprintf("%s.%s", backend.Name, aiGatewayRoute.Namespace)
			ret = append(ret, key)
		}
	}
	return ret
}

func aiServiceBackendIndexFunc(o client.Object) []string {
	aiServiceBackend := o.(*aigv1a1.AIServiceBackend)
	var ret []string
	if ref := aiServiceBackend.Spec.BackendSecurityPolicyRef; ref != nil {
		ret = append(ret, fmt.Sprintf("%s.%s", ref.Name, aiServiceBackend.Namespace))
	}
	return ret
}

func backendSecurityPolicyIndexFunc(o client.Object) []string {
	backendSecurityPolicy := o.(*aigv1a1.BackendSecurityPolicy)
	var key string
	switch backendSecurityPolicy.Spec.Type {
	case aigv1a1.BackendSecurityPolicyTypeAPIKey:
		apiKey := backendSecurityPolicy.Spec.APIKey
		key = getSecretNameAndNamespace(apiKey.SecretRef, backendSecurityPolicy.Namespace)
	case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
		awsCreds := backendSecurityPolicy.Spec.AWSCredentials
		if awsCreds.CredentialsFile != nil {
			key = getSecretNameAndNamespace(awsCreds.CredentialsFile.SecretRef, backendSecurityPolicy.Namespace)
		} else if awsCreds.OIDCExchangeToken != nil {
			key = backendSecurityPolicyKey(backendSecurityPolicy.Namespace, backendSecurityPolicy.Name)
		}
	}
	return []string{key}
}

func getSecretNameAndNamespace(secretRef *gwapiv1.SecretObjectReference, namespace string) string {
	if secretRef.Namespace != nil {
		return fmt.Sprintf("%s.%s", secretRef.Name, *secretRef.Namespace)
	}
	return fmt.Sprintf("%s.%s", secretRef.Name, namespace)
}

// newConditions creates a new condition with the given type and message.
//
// Currently, we only set one condition at a time either "Accepted" or "NotAccepted".
// In the future, if we can have multiple conditions like multiple errors, we can make changes here.
func newConditions(conditionType, message string) []metav1.Condition {
	condition := metav1.Condition{Message: message, LastTransitionTime: metav1.Now()}
	// Note: we use the fixed reason for now since the message is enough to describe the error and
	// reason doesn't fit the entire message.
	switch conditionType {
	case aigv1a1.ConditionTypeAccepted:
		condition.Type = aigv1a1.ConditionTypeAccepted
		condition.Status = metav1.ConditionTrue
		condition.Reason = "ReconciliationSucceeded"
	case aigv1a1.ConditionTypeNotAccepted:
		condition.Type = aigv1a1.ConditionTypeNotAccepted
		condition.Status = metav1.ConditionFalse
		condition.Reason = "ReconciliationFailed"
	}
	return []metav1.Condition{condition}
}
