package controller

import (
	"context"
	"fmt"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
)

// reconcileEnvoyProxyResources reconciles the [egv1a1.EnvoyProxy] resource for the given LLMRoute.
// Currently, this only takes effect for the AWSBedrock provider policy.
//
// This either creates a new [egv1a1.EnvoyProxy] resource or updates the existing one per [gwapiv1.Gateway] target of the route.
func (c *controller) reconcileEnvoyProxyResources(ctx context.Context, route *aigv1a1.LLMRoute, ownerRef []metav1.OwnerReference) error {
	var aws *aigv1a1.LLMProviderAWSBedrock
	for _, r := range route.Spec.Backends {
		if r.ProviderPolicy != nil && r.ProviderPolicy.Type == aigv1a1.LLMProviderTypeAWSBedrock {
			if aws != nil {
				return fmt.Errorf("multiple AWSBedrock provider policies are not supported")
			}
			aws = r.ProviderPolicy.AWSBedrock
		}
	}

	if aws == nil {
		return nil
	}

	for _, targetEGRef := range route.Spec.TargetRefs {
		// Get the EnvoyProxy resource attached to the Gateway.
		targetGWName := string(targetEGRef.Name)
		var gw gwapiv1.Gateway
		if err := c.client.Get(ctx, client.ObjectKey{Name: string(targetEGRef.Name), Namespace: route.Namespace}, &gw); err != nil {
			// If the Gateway is not found, skip the reconciliation.
			ctrl.Log.Error(err, "failed to get Gateway", "name", string(targetEGRef.Name), "namespace", route.Namespace)
			continue
		}

		ep := c.getEnvoyProxyAttachedToGateway(ctx, &gw)
		var envs []corev1.EnvVar
		switch aws.Type {
		case aigv1a1.LLMProviderAWSBedrockTypeInlineCredential:
			envs = []corev1.EnvVar{
				{Name: "AWS_ACCESS_KEY_ID", Value: aws.InlineCredential.AccessKeyID},
				{Name: "AWS_SECRET_ACCESS_KEY", Value: aws.InlineCredential.SecretAccessKey},
			}
			if aws.InlineCredential.SessionToken != "" {
				envs = append(envs, corev1.EnvVar{Name: "AWS_SESSION_TOKEN", Value: aws.InlineCredential.SessionToken})
			}
		case aigv1a1.LLMProviderAWSBedrockTypeCredentialsFIle:
			credentialFile := "~/.aws/credentials" // nolint: gosec
			if aws.CredentialsFile.Path != "" {
				credentialFile = aws.CredentialsFile.Path
			}
			profile := "default"
			if aws.CredentialsFile.Profile != "" {
				profile = aws.CredentialsFile.Profile
			}
			envs = []corev1.EnvVar{
				{Name: "AWS_SHARED_CREDENTIALS_FILE", Value: credentialFile},
				{Name: "AWS_PROFILE", Value: profile},
			}
		default:
			return fmt.Errorf("unsupported AWSBedrock provider type %q", aws.Type)
		}

		if err := c.reconcileEnvoyProxyResource(ctx, route.Name, route.Namespace, ownerRef, targetGWName, ep, envs); err != nil {
			return err
		}
	}
	return nil
}

// reconcileEnvoyProxyResourceAWSInlineCredential reconciles the EnvoyProxy resource.
func (c *controller) reconcileEnvoyProxyResource(
	ctx context.Context, routeName, routeNamespace string,
	ownerRef []metav1.OwnerReference,
	targetGatewayName string,
	existingEP *egv1a1.EnvoyProxy,
	envVars []corev1.EnvVar,
) error {
	if existingEP != nil {
		p := existingEP.Spec.Provider
		if p == nil {
			p = &egv1a1.EnvoyProxyProvider{Type: egv1a1.ProviderTypeKubernetes}
			existingEP.Spec.Provider = p
		}
		k := p.Kubernetes
		if k == nil {
			k = &egv1a1.EnvoyProxyKubernetesProvider{}
			p.Kubernetes = k
		}
		d := k.EnvoyDeployment
		if d == nil {
			d = &egv1a1.KubernetesDeploymentSpec{}
			k.EnvoyDeployment = d
		}
		container := d.Container
		if container == nil {
			container = &egv1a1.KubernetesContainerSpec{}
			d.Container = container
		}
		container.Env = updateEnvs(container.Env, envVars)
		// Update the existing resource.
		if err := c.client.Update(ctx, existingEP); err != nil {
			ctrl.Log.Error(err, "failed to update EnvoyProxy", "name", existingEP.Name)
			return fmt.Errorf("failed to update EnvoyProxy %s: %w", existingEP.Name, err)
		}
		return nil
	}

	// When we create a new EnvoyProxy resource, we use the name of the route and the target Gateway.
	name := fmt.Sprintf("%s-%s", routeName, targetGatewayName)
	if envoyProxy, err := c.getEnvoyProxy(ctx, name, routeNamespace); err != nil {
		envoyProxy = &egv1a1.EnvoyProxy{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       routeNamespace,
				Labels:          map[string]string{owningLabel: routeName},
				OwnerReferences: ownerRef,
			},
			Spec: egv1a1.EnvoyProxySpec{
				Provider: &egv1a1.EnvoyProxyProvider{
					Type: egv1a1.ProviderTypeKubernetes,
					Kubernetes: &egv1a1.EnvoyProxyKubernetesProvider{
						EnvoyDeployment: &egv1a1.KubernetesDeploymentSpec{
							Container: &egv1a1.KubernetesContainerSpec{Env: envVars},
						},
					},
				},
			},
		}
		if client.IgnoreNotFound(err) == nil {
			// Resource not found, create it.
			if err := c.client.Create(ctx, envoyProxy); err != nil {
				ctrl.Log.Error(err, "failed to create EnvoyProxy", "name", name)
				return fmt.Errorf("failed to create EnvoyProxy %s: %w", name, err)
			}
		} else {
			return fmt.Errorf("failed to get EnvoyProxy %s: %w", name, err)
		}
	} else {
		envoyProxy.Spec.Provider.Kubernetes.EnvoyDeployment.Container.Env = updateEnvs(envoyProxy.Spec.Provider.Kubernetes.EnvoyDeployment.Container.Env, envVars)
		if err := c.client.Update(ctx, envoyProxy); err != nil {
			ctrl.Log.Error(err, "failed to update EnvoyProxy", "name", name)
			return fmt.Errorf("failed to update EnvoyProxy %s: %w", name, err)
		}
	}
	return nil
}

func (c *controller) getEnvoyProxyAttachedToGateway(ctx context.Context, gw *gwapiv1.Gateway) *egv1a1.EnvoyProxy {
	if gw.Spec.Infrastructure == nil || gw.Spec.Infrastructure.ParametersRef == nil {
		return nil
	}

	p := gw.Spec.Infrastructure.ParametersRef
	if p.Kind != "EnvoyProxy" {
		return nil
	}
	ep, err := c.getEnvoyProxy(ctx, p.Name, gw.Namespace)
	if err != nil {
		ctrl.Log.Error(err, "failed to get EnvoyProxy", "name", p.Name, "namespace", gw.Namespace)
		return nil
	}
	return ep.DeepCopy()
}

// getEnvoyProxy returns the EnvoyProxy resource for the given route.
func (c *controller) getEnvoyProxy(ctx context.Context, routeName, routeNamespace string) (*egv1a1.EnvoyProxy, error) {
	var envoyProxy egv1a1.EnvoyProxy
	if err := c.client.Get(ctx, client.ObjectKey{Name: routeName, Namespace: routeNamespace}, &envoyProxy); err != nil {
		return nil, fmt.Errorf("failed to get EnvoyProxy %s: %w", routeName, err)
	}
	return &envoyProxy, nil
}

func updateEnvs(envs []corev1.EnvVar, newEnvs []corev1.EnvVar) []corev1.EnvVar {
	for _, newEnv := range newEnvs {
		found := false
		for i, env := range envs {
			if env.Name == newEnv.Name {
				envs[i] = newEnv
				found = true
				break
			}
		}
		if !found {
			envs = append(envs, newEnv)
		}
	}
	return envs
}
