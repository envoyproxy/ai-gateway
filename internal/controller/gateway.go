// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"cmp"
	"context"
	"fmt"

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
	"sigs.k8s.io/yaml"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/controller/rotators"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

const (
	// FilterConfigKeyInSecret is the key to store the filter config in the secret.
	FilterConfigKeyInSecret = "filter-config.yaml" //nolint: gosec
	// defaultOwnedBy is the default value for the ModelsOwnedBy field in the filter config.
	defaultOwnedBy = "Envoy AI Gateway"
)

// NewGatewayController creates a new reconcile.TypedReconciler for gwapiv1.Gateway.
//
// extProcImage is the image of the external processor sidecar container which will be used
// to check if the pods of the gateway deployment need to be rolled out.
func NewGatewayController(
	client client.Client, kube kubernetes.Interface, logger logr.Logger,
	envoyGatewayNamespace, udsPath, extProcImage string,
) *GatewayController {
	return &GatewayController{
		client:                client,
		kube:                  kube,
		logger:                logger,
		envoyGatewayNamespace: envoyGatewayNamespace,
		udsPath:               udsPath,
		extProcImage:          extProcImage,
	}
}

// GatewayController implements reconcile.TypedReconciler for gwapiv1.Gateway.
type GatewayController struct {
	client                client.Client
	kube                  kubernetes.Interface
	logger                logr.Logger
	envoyGatewayNamespace string
	udsPath               string
	extProcImage          string // The image of the external processor sidecar container.
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
		return ctrl.Result{}, err
	}

	if len(routes.Items) == 0 {
		// This means that the gateway is not attached to any AIGatewayRoute.
		c.logger.Info("No AIGatewayRoute attached to the Gateway", "namespace", gw.Namespace, "name", gw.Name)
		return ctrl.Result{}, nil
	}

	// We need to create the filter config in Envoy Gateway system namespace because the sidecar extproc need
	// to access it.
	if err := c.reconcileFilterConfigSecret(ctx, &gw, routes.Items, gw.Name); err != nil {
		return ctrl.Result{}, err
	}

	// Finally, we need to annotate the pods of the gateway deployment with the new uuid to propagate the filter config Secret update faster.
	// If the pod doesn't have the extproc container, it will roll out the deployment altogether which eventually ends up
	// the mutation hook invoked.
	if err := c.annotateGatewayPods(ctx, &gw, uuid.NewString()); err != nil {
		c.logger.Error(err, "Failed to annotate gateway pods", "namespace", gw.Namespace, "name", gw.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// schemaToFilterAPI converts an aigv1a1.VersionedAPISchema to filterapi.VersionedAPISchema.
func schemaToFilterAPI(schema aigv1a1.VersionedAPISchema) filterapi.VersionedAPISchema {
	ret := filterapi.VersionedAPISchema{}
	ret.Name = filterapi.APISchemaName(schema.Name)
	if schema.Name == aigv1a1.APISchemaOpenAI {
		// When the schema is OpenAI, we default to the v1 version if not specified or nil.
		ret.Version = cmp.Or(ptr.Deref(schema.Version, "v1"), "v1")
	} else {
		ret.Version = ptr.Deref(schema.Version, "")
	}
	return ret
}

// reconcileFilterConfigSecret updates the filter config secret for the external processor.
func (c *GatewayController) reconcileFilterConfigSecret(ctx context.Context, gw *gwapiv1.Gateway, aiGatewayRoutes []aigv1a1.AIGatewayRoute, uuid string) error {
	// Precondition: aiGatewayRoutes is not empty as we early return if it is empty.
	input := aiGatewayRoutes[0].Spec.APISchema

	ec := &filterapi.Config{UUID: uuid}
	ec.Schema = schemaToFilterAPI(input)
	ec.ModelNameHeaderKey = aigv1a1.AIModelHeaderKey
	var err error
	llmCosts := map[string]struct{}{}
	for i := range aiGatewayRoutes {
		aiGatewayRoute := &aiGatewayRoutes[i]
		spec := aiGatewayRoute.Spec
		for i := range spec.Rules {
			rule := &spec.Rules[i]
			for _, m := range rule.Matches {
				for _, h := range m.Headers {
					// If explicitly set to something that is not an exact match, skip.
					// If not set, we assume it's an exact match.
					//
					// Also, we only care about the AIModel header to declare models.
					if (h.Type != nil && *h.Type != gwapiv1.HeaderMatchExact) || string(h.Name) != aigv1a1.AIModelHeaderKey {
						continue
					}
					ec.Models = append(ec.Models, filterapi.Model{
						Name:      h.Value,
						CreatedAt: ptr.Deref[metav1.Time](rule.ModelsCreatedAt, aiGatewayRoute.CreationTimestamp).Time.UTC(),
						OwnedBy:   ptr.Deref(rule.ModelsOwnedBy, defaultOwnedBy),
					})
				}
			}
			for j := range rule.BackendRefs {
				backendRef := &rule.BackendRefs[j]
				b := filterapi.Backend{}
				b.Name = internalapi.PerRouteRuleRefBackendName(aiGatewayRoute.Namespace, backendRef.Name, aiGatewayRoute.Name, i, j)
				b.ModelNameOverride = backendRef.ModelNameOverride
				if backendRef.IsInferencePool() {
					// We assume that InferencePools are all OpenAI schema.
					schema := aiGatewayRoute.Spec.APISchema
					b.Schema = filterapi.VersionedAPISchema{Name: filterapi.APISchemaName(schema.Name), Version: ptr.Deref(schema.Version, "v1")}
				} else {
					var backendObj *aigv1a1.AIServiceBackend
					var bsp *aigv1a1.BackendSecurityPolicy
					backendObj, bsp, err = c.backendWithMaybeBSP(ctx, aiGatewayRoute.Namespace, backendRef.Name)
					if err != nil {
						return fmt.Errorf("failed to get AIServiceBackend %s: %w", b.Name, err)
					}
					b.Schema = schemaToFilterAPI(backendObj.Spec.APISchema)
					if bsp != nil {
						b.Auth, err = c.bspToFilterAPIBackendAuth(ctx, bsp)
						if err != nil {
							return fmt.Errorf("failed to create backend auth: %w", err)
						}
					}
				}

				ec.Backends = append(ec.Backends, b)
			}

			for _, cost := range aiGatewayRoute.Spec.LLMRequestCosts {
				fc := filterapi.LLMRequestCost{MetadataKey: cost.MetadataKey}
				_, ok := llmCosts[cost.MetadataKey]
				if ok {
					c.logger.Info("LLMRequestCost with the same metadata key already exists, skipping",
						"metadataKey", cost.MetadataKey, "route", aiGatewayRoute.Name)
					continue
				}
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
				llmCosts[cost.MetadataKey] = struct{}{}
			}
		}
	}

	ec.MetadataNamespace = aigv1a1.AIGatewayFilterMetadataNamespace

	marshaled, err := yaml.Marshal(ec)
	if err != nil {
		return fmt.Errorf("failed to marshal extproc config: %w", err)
	}

	name := FilterConfigSecretPerGatewayName(gw.Name, gw.Namespace)
	// We need to create the filter config in Envoy Gateway system namespace because the sidecar extproc need
	// to access it.
	data := map[string]string{FilterConfigKeyInSecret: string(marshaled)}
	secret, err := c.kube.CoreV1().Secrets(c.envoyGatewayNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.envoyGatewayNamespace},
				StringData: data,
			}
			if _, err = c.kube.CoreV1().Secrets(c.envoyGatewayNamespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("failed to create secret %s: %w", name, err)
			}
			return nil
		}
		return fmt.Errorf("failed to get secret %s: %w", name, err)
	}

	secret.StringData = data
	if _, err := c.kube.CoreV1().Secrets(c.envoyGatewayNamespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update secret %s: %w", secret.Name, err)
	}
	return nil
}

func (c *GatewayController) bspToFilterAPIBackendAuth(ctx context.Context, backendSecurityPolicy *aigv1a1.BackendSecurityPolicy) (*filterapi.BackendAuth, error) {
	namespace := backendSecurityPolicy.Namespace
	switch backendSecurityPolicy.Spec.Type {
	case aigv1a1.BackendSecurityPolicyTypeAPIKey:
		secretName := string(backendSecurityPolicy.Spec.APIKey.SecretRef.Name)
		apiKey, err := c.getSecretData(ctx, namespace, secretName, apiKeyInSecret)
		if err != nil {
			return nil, fmt.Errorf("failed to get secret %s: %w", secretName, err)
		}
		return &filterapi.BackendAuth{APIKey: &filterapi.APIKeyAuth{Key: apiKey}}, nil
	case aigv1a1.BackendSecurityPolicyTypeAWSCredentials:
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
		return &filterapi.BackendAuth{
			AWSAuth: &filterapi.AWSAuth{
				CredentialFileLiteral: credentialsLiteral,
				Region:                backendSecurityPolicy.Spec.AWSCredentials.Region,
			},
		}, nil
	case aigv1a1.BackendSecurityPolicyTypeAzureCredentials:
		secretName := rotators.GetBSPSecretName(backendSecurityPolicy.Name)
		azureAccessToken, err := c.getSecretData(ctx, namespace, secretName, rotators.AzureAccessTokenKey)
		if err != nil {
			return nil, fmt.Errorf("failed to get secret %s: %w", secretName, err)
		}
		return &filterapi.BackendAuth{
			AzureAuth: &filterapi.AzureAuth{AccessToken: azureAccessToken},
		}, nil
	case aigv1a1.BackendSecurityPolicyTypeGCPCredentials:
		gcpCreds := backendSecurityPolicy.Spec.GCPCredentials
		secretName := rotators.GetBSPSecretName(backendSecurityPolicy.Name)
		gcpAccessToken, err := c.getSecretData(ctx, namespace, secretName, rotators.GCPAccessTokenKey)
		if err != nil {
			return nil, fmt.Errorf("failed to get secret %s: %w", secretName, err)
		}
		return &filterapi.BackendAuth{
			GCPAuth: &filterapi.GCPAuth{
				AccessToken: gcpAccessToken,
				Region:      gcpCreds.Region,
				ProjectName: gcpCreds.ProjectName,
			},
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

// backendWithMaybeBSP retrieves the AIServiceBackend and its associated BackendSecurityPolicy if it exists.
func (c *GatewayController) backendWithMaybeBSP(ctx context.Context, namespace, name string) (backend *aigv1a1.AIServiceBackend, bsp *aigv1a1.BackendSecurityPolicy, err error) {
	backend = &aigv1a1.AIServiceBackend{}
	if err = c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, backend); err != nil {
		return
	}

	// Old Pattern using BackendSecurityPolicyRef. Prioritize this field over the new pattern as per the documentation.
	if bspRef := backend.Spec.BackendSecurityPolicyRef; bspRef != nil {
		bsp, err = c.backendSecurityPolicy(ctx, namespace, string(bspRef.Name))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get BackendSecurityPolicy %s: %w", bspRef.Name, err)
		}
		return
	}

	// New Pattern using BackendSecurityPolicy.
	var backendSecurityPolicyList aigv1a1.BackendSecurityPolicyList
	key := fmt.Sprintf("%s.%s", name, namespace)
	if err := c.client.List(ctx, &backendSecurityPolicyList, client.InNamespace(namespace),
		client.MatchingFields{k8sClientIndexAIServiceBackendToTargetingBackendSecurityPolicy: key}); err != nil {
		return nil, nil, fmt.Errorf("failed to list BackendSecurityPolicies for backend %s: %w", name, err)
	}
	switch len(backendSecurityPolicyList.Items) {
	case 0:
	case 1:
		bsp = &backendSecurityPolicyList.Items[0]
	default:
		// We reject the case of multiple BackendSecurityPolicies for the same backend since that could be potentially
		// a security issue. API is clearly documented to allow only one BackendSecurityPolicy per backend.
		//
		// Same validation happens in the AIServiceBackend controller, but it might be the case that a new BackendSecurityPolicy
		// is created after the AIServiceBackend's reconciliation.
		c.logger.Info("multiple BackendSecurityPolicies found for backend", "backend_name", name, "backend_namespace", namespace,
			"count", len(backendSecurityPolicyList.Items))
		return nil, nil, fmt.Errorf("multiple BackendSecurityPolicies found for backend %s", name)
	}
	return
}

func (c *GatewayController) backendSecurityPolicy(ctx context.Context, namespace, name string) (*aigv1a1.BackendSecurityPolicy, error) {
	backendSecurityPolicy := &aigv1a1.BackendSecurityPolicy{}
	return backendSecurityPolicy, c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, backendSecurityPolicy)
}

// annotateGatewayPods annotates the pods of GW with the new uuid to propagate the filter config Secret update faster.
// If the pod doesn't have the extproc container, it will roll out the deployment altogether, which eventually ends up
// the mutation hook invoked.
//
// See https://neonmirrors.net/post/2022-12/reducing-pod-volume-update-times/ for explanation.
func (c *GatewayController) annotateGatewayPods(ctx context.Context, gw *gwapiv1.Gateway, uuid string) error {
	pods, err := c.kube.CoreV1().Pods(c.envoyGatewayNamespace).List(ctx, metav1.ListOptions{
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
			// If there's an extproc container with the current target image, we don't need to roll out the deployment.
			if podSpec.Containers[i].Name == extProcContainerName && podSpec.Containers[i].Image == c.extProcImage {
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
		deps, err := c.kube.AppsV1().Deployments(c.envoyGatewayNamespace).List(ctx, metav1.ListOptions{
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
					`{"spec":{"template":{"metadata":{"annotations":{"%s":"%s"}}}}}`, aigatewayUUIDAnnotationKey, uuid),
				), metav1.PatchOptions{})
			if err != nil {
				return fmt.Errorf("failed to patch deployment %s: %w", dep.Name, err)
			}
		}

		daemonSets, err := c.kube.AppsV1().DaemonSets(c.envoyGatewayNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s,%s=%s",
				egOwningGatewayNameLabel, gw.Name, egOwningGatewayNamespaceLabel, gw.Namespace),
		})
		if err != nil {
			return fmt.Errorf("failed to list daemonsets: %w", err)
		}

		for _, daemonSet := range daemonSets.Items {
			c.logger.Info("rolling out daemonSet", "namespace", daemonSet.Namespace, "name", daemonSet.Name)
			_, err = c.kube.AppsV1().DaemonSets(daemonSet.Namespace).Patch(ctx, daemonSet.Name, types.MergePatchType,
				[]byte(fmt.Sprintf(
					`{"spec":{"template":{"metadata":{"annotations":{"%s":"%s"}}}}}`, aigatewayUUIDAnnotationKey, uuid),
				), metav1.PatchOptions{})
			if err != nil {
				return fmt.Errorf("failed to patch daemonset %s: %w", daemonSet.Name, err)
			}
		}
	}
	return nil
}
