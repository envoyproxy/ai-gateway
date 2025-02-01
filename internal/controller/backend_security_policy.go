package controller

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller/token_rotators"
)

// backendSecurityPolicyController implements [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
//
// This handles the BackendSecurityPolicy resource and sends it to the config sink so that it can modify configuration.
// It also manages credential rotation through the TokenManager when AWS credentials are configured.
type backendSecurityPolicyController struct {
	client       client.Client
	kube         kubernetes.Interface
	logger       logr.Logger
	eventChan    chan ConfigSinkEvent
	tokenManager *TokenManager
}

func newBackendSecurityPolicyController(
	client client.Client,
	kube kubernetes.Interface,
	logger logr.Logger,
	ch chan ConfigSinkEvent,
	tokenManager *TokenManager,
) *backendSecurityPolicyController {
	return &backendSecurityPolicyController{
		client:       client,
		kube:         kube,
		logger:       logger,
		eventChan:    ch,
		tokenManager: tokenManager,
	}
}

// Reconcile implements the [reconcile.TypedReconciler] for [aigv1a1.BackendSecurityPolicy].
func (b backendSecurityPolicyController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var backendSecurityPolicy aigv1a1.BackendSecurityPolicy
	if err := b.client.Get(ctx, req.NamespacedName, &backendSecurityPolicy); err != nil {
		if errors.IsNotFound(err) {
			ctrl.Log.Info("Deleting Backend Security Policy",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Send the backend security policy to the config sink so that it can modify the configuration together with the state of other resources.
	b.eventChan <- backendSecurityPolicy.DeepCopy()

	// Handle AWS credential rotation if configured
	if err := b.handleAWSCredentialRotation(ctx, &backendSecurityPolicy); err != nil {
		b.logger.Error(err, "failed to handle AWS credential rotation",
			"namespace", req.Namespace,
			"name", req.Name)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// handleAWSCredentialRotation processes AWS credential rotation if configured in the BackendSecurityPolicy
func (b backendSecurityPolicyController) handleAWSCredentialRotation(ctx context.Context, policy *aigv1a1.BackendSecurityPolicy) error {
	if policy.Spec.Type != aigv1a1.BackendSecurityPolicyType("AWSCredentials") {
		return nil
	}

	// Handle IAM credentials rotation
	if policy.Spec.AWSCredentials != nil && policy.Spec.AWSCredentials.CredentialsFile != nil {
		event := token_rotators.RotationEvent{
			Namespace: policy.Namespace,
			Name:      string(policy.Spec.AWSCredentials.CredentialsFile.SecretRef.Name),
			Type:      token_rotators.RotationTypeAWSCredentials,
			Metadata:  make(map[string]string),
		}
		if err := b.tokenManager.RequestRotation(ctx, event); err != nil {
			return err
		}
	}

	// Handle OIDC token rotation
	if policy.Spec.AWSCredentials != nil && policy.Spec.AWSCredentials.OIDCExchangeToken != nil {
		event := token_rotators.RotationEvent{
			Namespace: policy.Namespace,
			Name:      policy.Name,
			Type:      token_rotators.RotationTypeAWSOIDC,
			Metadata: map[string]string{
				"role_arn": policy.Spec.AWSCredentials.OIDCExchangeToken.AwsRoleArn,
				// Note: id_token will be added by the token manager when available
			},
		}
		if err := b.tokenManager.RequestRotation(ctx, event); err != nil {
			return err
		}
	}

	return nil
}
