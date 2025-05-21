// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	"sigs.k8s.io/yaml"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/controller/rotators"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

// FilterConfigKeyInSecret is the key to store the filter config in the secret.
const FilterConfigKeyInSecret = "filter-config.yaml" //nolint: gosec

// NewGatewayController creates a new reconcile.TypedReconciler for gwapiv1.Gateway.
func NewGatewayController(
	client client.Client, kube kubernetes.Interface, logger logr.Logger,
	envoyGatewaySystemNamespace, udsPath string,
) *GatewayController {
	return &GatewayController{
		client:                      client,
		kube:                        kube,
		logger:                      logger,
		envoyGatewaySystemNamespace: envoyGatewaySystemNamespace,
		udsPath:                     udsPath,
	}
}

// GatewayController implements reconcile.TypedReconciler for gwapiv1.Gateway.
type GatewayController struct {
	client                      client.Client
	kube                        kubernetes.Interface
	logger                      logr.Logger
	envoyGatewaySystemNamespace string
	udsPath                     string
}

// Reconcile implements the reconcile.Reconciler for gwapiv1.Gateway.
func (c *GatewayController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var gw gwapiv1.Gateway
	if err := c.client.Get(ctx, req.NamespacedName, &gw); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	var routes aigv1a1.AIGatewayRouteList
	err := c.client.List(ctx, &routes, client.MatchingFields{
		k8sClientIndexAIGatewayRouteToAttachedGateway: fmt.Sprintf("%s.%s", req.Name, req.Namespace),
	})
	if err != nil {
		c.logger.Error(err, "Failed to list AIGatewayRoutes",
			"namespace", gw.Namespace, "name", gw.Name)
		return ctrl.Result{}, err
	}

	if len(routes.Items) == 0 {
		// This means that the gateway is not attached to any AIGatewayRoute.
		return ctrl.Result{}, nil
	}
	if err := c.ensureExtensionPolicy(ctx, &gw); err != nil {
		c.logger.Error(err, "Failed to ensure extension policy",
			"namespace", gw.Namespace, "name", gw.Name)
		return ctrl.Result{}, err
	}

	// We need to create the filter config in Envoy Gateway system namespace because the sidecar extproc need
	// to access it.
	if err := c.reconcileFilterConfigSecret(ctx, &gw, routes.Items, gw.Name); err != nil {
		c.logger.Error(err, "Failed to reconcile filter config secret",
			"namespace", gw.Namespace, "name", gw.Name)
		return ctrl.Result{}, err
	}

	// Finally, we need to annotate the pods of the gateway deployment with the new uuid to propagate the filter config Secret update faster.
	// If the pod doesn't have the extproc container, it will roll out the deployment altogether which eventually ends up
	// the mutation hook invoked.
	if err := c.annotateGatewayPods(ctx, &gw, uuid.NewString()); err != nil {
		c.logger.Error(err, "Failed to annotate gateway pods",
			"namespace", gw.Namespace, "name", gw.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// ensureExtensionPolicy creates or updates the extension policy for the external process running as a sidecar.
func (c *GatewayController) ensureExtensionPolicy(ctx context.Context, gw *gwapiv1.Gateway) (err error) {
	// Ensure that the backend that makes Envoy talk to the UDS exists.
	const extProcBackendName = "envoy-ai-gateway-extproc-backend"
	var backend egv1a1.Backend
	if err = c.client.Get(ctx, client.ObjectKey{Name: extProcBackendName, Namespace: gw.Namespace}, &backend); err != nil {
		if apierrors.IsNotFound(err) {
			backend = egv1a1.Backend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      extProcBackendName,
					Namespace: gw.Namespace,
				},
				Spec: egv1a1.BackendSpec{
					Endpoints: []egv1a1.BackendEndpoint{{Unix: &egv1a1.UnixSocket{Path: c.udsPath}}},
				},
			}
			if err = c.client.Create(ctx, &backend); err != nil {
				return fmt.Errorf("failed to create backend: %w", err)
			}
		} else {
			return fmt.Errorf("failed to get backend: %w", err)
		}
	}

	perGatewayEEPName := fmt.Sprintf("ai-eg-eep-%s", gw.Name)
	var existingPolicy egv1a1.EnvoyExtensionPolicy
	if err = c.client.Get(ctx, client.ObjectKey{Name: perGatewayEEPName, Namespace: gw.Namespace}, &existingPolicy); err == nil {
		return
	} else if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get extension policy: %w", err)
	}

	extPolicy := &egv1a1.EnvoyExtensionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: perGatewayEEPName, Namespace: gw.Namespace},
		Spec: egv1a1.EnvoyExtensionPolicySpec{
			PolicyTargetReferences: egv1a1.PolicyTargetReferences{TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{
					LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
						Kind:  "Gateway",
						Group: "gateway.networking.k8s.io",
						Name:  gwapiv1.ObjectName(gw.Name),
					},
				},
			}},
			ExtProc: []egv1a1.ExtProc{{
				ProcessingMode: &egv1a1.ExtProcProcessingMode{
					AllowModeOverride: true, // Streaming completely overrides the buffered mode.
					Request:           &egv1a1.ProcessingModeOptions{Body: ptr.To(egv1a1.BufferedExtProcBodyProcessingMode)},
				},
				BackendCluster: egv1a1.BackendCluster{BackendRefs: []egv1a1.BackendRef{{
					BackendObjectReference: gwapiv1.BackendObjectReference{
						Name:      gwapiv1.ObjectName(extProcBackendName),
						Kind:      ptr.To(gwapiv1.Kind("Backend")),
						Group:     ptr.To(gwapiv1.Group("gateway.envoyproxy.io")),
						Namespace: ptr.To(gwapiv1.Namespace(gw.Namespace)),
					},
				}}},
			}},
		},
	}
	if err = c.client.Create(ctx, extPolicy); err != nil {
		err = fmt.Errorf("failed to create extension policy: %w", err)
	}
	return
}

// reconcileFilterConfigSecret updates the filter config secret for the external processor.
func (c *GatewayController) reconcileFilterConfigSecret(ctx context.Context, gw *gwapiv1.Gateway, aiGatewayRoutes []aigv1a1.AIGatewayRoute, uuid string) error {
	// Precondition: aiGatewayRoutes is not empty as we early return if it is empty.
	input := aiGatewayRoutes[0].Spec.APISchema

	ec := &filterapi.Config{UUID: uuid}
	ec.Schema.Name = filterapi.APISchemaName(input.Name)
	ec.Schema.Version = input.Version
	ec.ModelNameHeaderKey = aigv1a1.AIModelHeaderKey
	ec.SelectedRouteHeaderKey = selectedRouteHeaderKey
	backends := map[string]*filterapi.Backend{}
	var err error
	for i := range aiGatewayRoutes {
		aiGatewayRoute := &aiGatewayRoutes[i]
		spec := aiGatewayRoute.Spec
		for i := range spec.Rules {
			rule := &spec.Rules[i]
			for j := range rule.BackendRefs {
				backendRef := &rule.BackendRefs[j]
				key := fmt.Sprintf("%s.%s", backendRef.Name, aiGatewayRoute.Namespace)
				if _, ok := backends[key]; ok {
					continue
				}
				b := &filterapi.Backend{Name: key}
				backends[key] = b
				var backendObj *aigv1a1.AIServiceBackend
				backendObj, err = c.backend(ctx, aiGatewayRoute.Namespace, backendRef.Name)
				if err != nil {
					return fmt.Errorf("failed to get AIServiceBackend %s: %w", key, err)
				}
				b.Schema.Name = filterapi.APISchemaName(backendObj.Spec.APISchema.Name)
				b.Schema.Version = backendObj.Spec.APISchema.Version
				if bspRef := backendObj.Spec.BackendSecurityPolicyRef; bspRef != nil {
					b.Auth, err = c.bspToFilterAPIBackendAuth(ctx, aiGatewayRoute.Namespace, string(bspRef.Name))
					if err != nil {
						return fmt.Errorf("failed to create backend auth: %w", err)
					}
				}
			}
			configRule := filterapi.RouteRule{}
			configRule.Name = routeName(aiGatewayRoute, i)
			configRule.Headers = make([]filterapi.HeaderMatch, len(rule.Matches))
			for j, match := range rule.Matches {
				configRule.Headers[j].Name = match.Headers[0].Name
				configRule.Headers[j].Value = match.Headers[0].Value
			}
			ec.Rules = append(ec.Rules, configRule)

			for _, cost := range aiGatewayRoute.Spec.LLMRequestCosts {
				fc := filterapi.LLMRequestCost{MetadataKey: cost.MetadataKey}
				switch cost.Type {
				case aigv1a1.LLMRequestCostTypeInputToken:
					fc.Type = filterapi.LLMRequestCostTypeInputToken
				case aigv1a1.LLMRequestCostTypeOutputToken:
					fc.Type = filterapi.LLMRequestCostTypeOutputToken
				case aigv1a1.LLMRequestCostTypeTotalToken:
					fc.Type = filterapi.LLMRequestCostTypeTotalToken
				case aigv1a1.LLMRequestCostTypeCEL:
					fc.Type = filterapi.LLMRequestCostTypeCEL
					expr := *cost.CEL
					// Sanity check the CEL expression.
					_, err = llmcostcel.NewProgram(expr)
					if err != nil {
						return fmt.Errorf("invalid CEL expression: %w", err)
					}
					fc.CEL = expr
				default:
					return fmt.Errorf("unknown request cost type: %s", cost.Type)
				}
				ec.LLMRequestCosts = append(ec.LLMRequestCosts, fc)
			}
		}
	}
	ec.Backends = make([]*filterapi.Backend, 0, len(backends))
	for _, backend := range backends {
		ec.Backends = append(ec.Backends, backend)
	}
	sort.Slice(ec.Backends, func(i, j int) bool {
		return ec.Backends[i].Name < ec.Backends[j].Name
	})

	ec.MetadataNamespace = aigv1a1.AIGatewayFilterMetadataNamespace

	marshaled, err := yaml.Marshal(ec)
	if err != nil {
		return fmt.Errorf("failed to marshal extproc config: %w", err)
	}

	name := FilterConfigSecretPerGatewayName(gw.Name, gw.Namespace)
	// We need to create the filter config in Envoy Gateway system namespace because the sidecar extproc need
	// to access it.
	data := map[string]string{FilterConfigKeyInSecret: string(marshaled)}
	secret, err := c.kube.CoreV1().Secrets(c.envoyGatewaySystemNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.envoyGatewaySystemNamespace},
				StringData: data,
			}
			if _, err = c.kube.CoreV1().Secrets(c.envoyGatewaySystemNamespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("failed to create secret %s: %w", name, err)
			}
			return nil
		}
		return fmt.Errorf("failed to get secret %s: %w", name, err)
	}

	secret.StringData = data
	if _, err := c.kube.CoreV1().Secrets(c.envoyGatewaySystemNamespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update secret %s: %w", secret.Name, err)
	}
	return nil
}

func (c *GatewayController) bspToFilterAPIBackendAuth(ctx context.Context, namespace, bspName string) (*filterapi.BackendAuth, error) {
	backendSecurityPolicy, err := c.backendSecurityPolicy(ctx, namespace, bspName)
	if err != nil {
		return nil, fmt.Errorf("failed to get BackendSecurityPolicy %s: %w", bspName, err)
	}
	switch backendSecurityPolicy.Spec.Type {
	case aigv1a1.BackendSecurityPolicyTypeAPIKey:
		secretName := string(backendSecurityPolicy.Spec.APIKey.SecretRef.Name)
		apiKey, err := c.getSecretData(ctx, namespace, secretName, apiKey)
		if err != nil {
			return nil, fmt.Errorf("failed to get secret %s: %w", secretName, err)
		}
		return &filterapi.BackendAuth{APIKey: &filterapi.APIKeyAuth{Key: apiKey}}, nil
	case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
		if backendSecurityPolicy.Spec.AWSCredentials == nil {
			return nil, fmt.Errorf("AWSCredentials type selected but not defined %s", backendSecurityPolicy.Name)
		}
		var secretName string
		if awsCred := backendSecurityPolicy.Spec.AWSCredentials; awsCred.CredentialsFile != nil {
			secretName = string(awsCred.CredentialsFile.SecretRef.Name)
		} else {
			secretName = rotators.GetBSPSecretName(backendSecurityPolicy.Name)
		}
		credentialsLiteral, err := c.getSecretData(ctx, namespace, secretName, rotators.AwsCredentialsKey)
		if err != nil {
			return nil, fmt.Errorf("failed to get secret %s: %w", secretName, err)
		}
		if awsCred := backendSecurityPolicy.Spec.AWSCredentials; awsCred.CredentialsFile != nil || awsCred.OIDCExchangeToken != nil {
			return &filterapi.BackendAuth{
				AWSAuth: &filterapi.AWSAuth{
					CredentialFileLiteral: credentialsLiteral,
					Region:                backendSecurityPolicy.Spec.AWSCredentials.Region,
				},
			}, nil
		}
		return nil, nil
	case aigv1a1.BackendSecurityPolicyTypeAzureCredentials:
		if backendSecurityPolicy.Spec.AzureCredentials == nil {
			return nil, fmt.Errorf("AzureCredentials type selected but not defined %s", backendSecurityPolicy.Name)
		}
		secretName := rotators.GetBSPSecretName(backendSecurityPolicy.Name)
		azureAccessToken, err := c.getSecretData(ctx, namespace, secretName, rotators.AzureAccessTokenKey)
		if err != nil {
			return nil, fmt.Errorf("failed to get secret %s: %w", secretName, err)
		}
		return &filterapi.BackendAuth{
			AzureAuth: &filterapi.AzureAuth{AccessToken: azureAccessToken},
		}, nil
	default:
		return nil, fmt.Errorf("invalid backend security type %s for policy %s", backendSecurityPolicy.Spec.Type,
			backendSecurityPolicy.Name)
	}
}

func (c *GatewayController) getSecretData(ctx context.Context, namespace, name, dataKey string) (string, error) {
	secret, err := c.kube.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s: %w", name, err)
	}
	if secret.Data != nil {
		if value, ok := secret.Data[dataKey]; ok {
			return string(value), nil
		}
	}
	if secret.StringData != nil {
		if value, ok := secret.StringData[dataKey]; ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("secret %s does not contain key %s", name, dataKey)
}

func (c *GatewayController) backend(ctx context.Context, namespace, name string) (*aigv1a1.AIServiceBackend, error) {
	backend := &aigv1a1.AIServiceBackend{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, backend); err != nil {
		return nil, err
	}
	return backend, nil
}

func (c *GatewayController) backendSecurityPolicy(ctx context.Context, namespace, name string) (*aigv1a1.BackendSecurityPolicy, error) {
	backendSecurityPolicy := &aigv1a1.BackendSecurityPolicy{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, backendSecurityPolicy); err != nil {
		return nil, err
	}
	return backendSecurityPolicy, nil
}

// annotateGatewayPods annotates the pods of GW with the new uuid to propagate the filter config Secret update faster.
// If the pod doesn't have the extproc container, it will roll out the deployment altogether which eventually ends up
// the mutation hook invoked.
//
// See https://neonmirrors.net/post/2022-12/reducing-pod-volume-update-times/ for explanation.
func (c *GatewayController) annotateGatewayPods(ctx context.Context, gw *gwapiv1.Gateway, uuid string) error {
	pods, err := c.kube.CoreV1().Pods(c.envoyGatewaySystemNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s",
			egOwningGatewayNameLabel, gw.Name, egOwningGatewayNamespaceLabel, gw.Namespace),
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	rollout := true
	for _, pod := range pods.Items {
		// Get the pod spec and check if it has the extproc container.
		podSpec := pod.Spec
		for i := range podSpec.Containers {
			if strings.Contains(podSpec.Containers[i].Name, mutationNamePrefix) {
				rollout = false
				break
			}
		}

		c.logger.Info("annotating pod", "namespace", pod.Namespace, "name", pod.Name)
		_, err = c.kube.CoreV1().Pods(pod.Namespace).Patch(ctx, pod.Name, types.MergePatchType,
			[]byte(fmt.Sprintf(
				`{"metadata":{"annotations":{"%s":"%s"}}}`, aigatewayUUIDAnnotationKey, uuid),
			), metav1.PatchOptions{})
		if err != nil {
			return fmt.Errorf("failed to patch pod %s: %w", pod.Name, err)
		}
	}

	if rollout {
		deps, err := c.kube.AppsV1().Deployments(c.envoyGatewaySystemNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s,%s=%s",
				egOwningGatewayNameLabel, gw.Name, egOwningGatewayNamespaceLabel, gw.Namespace),
		})
		if err != nil {
			return fmt.Errorf("failed to list deployments: %w", err)
		}

		for _, dep := range deps.Items {
			c.logger.Info("rolling out deployment", "namespace", dep.Namespace, "name", dep.Name)
			_, err = c.kube.AppsV1().Deployments(dep.Namespace).Patch(ctx, dep.Name, types.MergePatchType,
				[]byte(fmt.Sprintf(
					`{"spec":{"template":{"metadata":{"annotations":{"%s":"%s"}}}}}}`, aigatewayUUIDAnnotationKey, uuid),
				), metav1.PatchOptions{})
			if err != nil {
				return fmt.Errorf("failed to patch deployment %s: %w", dep.Name, err)
			}
		}
	}
	return nil
}
