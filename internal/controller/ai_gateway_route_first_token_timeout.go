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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
)

const firstTokenTimeoutPolicySuffix = "-aieg-ttft" //nolint:gosec // G101: not a credential — Kubernetes name suffix.

func firstTokenTimeoutPolicyName(route *aigv1b1.AIGatewayRoute) string {
	return route.Name + firstTokenTimeoutPolicySuffix
}

// reconcileFirstTokenTimeoutPolicy creates, updates, or removes the EnvoyPatchPolicy that
// carries the per-route idle_timeout derived from FirstTokenTimeout. Retry configuration is
// intentionally left to the user's BackendTrafficPolicy so we don't clobber it.
func (c *AIGatewayRouteController) reconcileFirstTokenTimeoutPolicy(ctx context.Context, aiGatewayRoute *aigv1b1.AIGatewayRoute) error {
	desired, err := c.buildFirstTokenTimeoutPolicy(ctx, aiGatewayRoute)
	if err != nil {
		return err
	}

	key := client.ObjectKey{
		Name:      firstTokenTimeoutPolicyName(aiGatewayRoute),
		Namespace: aiGatewayRoute.Namespace,
	}
	var existing egv1a1.EnvoyPatchPolicy
	getErr := c.client.Get(ctx, key, &existing)
	notFound := apierrors.IsNotFound(getErr)

	switch {
	case desired == nil && notFound:
		return nil
	case desired == nil:
		if err := c.client.Delete(ctx, &existing); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete obsolete EnvoyPatchPolicy %s: %w", key.Name, err)
		}
		return nil
	case notFound:
		if err := ctrlutil.SetControllerReference(aiGatewayRoute, desired, c.client.Scheme()); err != nil {
			return fmt.Errorf("BUG: failed to set controller reference for EnvoyPatchPolicy %s: %w", key.Name, err)
		}
		if err := c.client.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create EnvoyPatchPolicy %s: %w", key.Name, err)
		}
		return nil
	case getErr != nil:
		return fmt.Errorf("failed to get EnvoyPatchPolicy %s: %w", key.Name, getErr)
	default:
		existing.Spec = desired.Spec
		if err := c.client.Update(ctx, &existing); err != nil {
			return fmt.Errorf("failed to update EnvoyPatchPolicy %s: %w", key.Name, err)
		}
		return nil
	}
}

// buildFirstTokenTimeoutPolicy returns the desired EnvoyPatchPolicy, or nil when there is
// nothing to patch (no rule wants TTFT, no parent ref, or the parent Gateway isn't visible
// yet). The returned object has no owner reference set.
func (c *AIGatewayRouteController) buildFirstTokenTimeoutPolicy(ctx context.Context, aiGatewayRoute *aigv1b1.AIGatewayRoute) (*egv1a1.EnvoyPatchPolicy, error) {
	type ruleEntry struct {
		idx      int
		duration time.Duration
	}
	var rules []ruleEntry
	for i := range aiGatewayRoute.Spec.Rules {
		if d := aiGatewayRoute.Spec.Rules[i].GetFirstTokenTimeout(); d > 0 {
			rules = append(rules, ruleEntry{idx: i, duration: d})
		}
	}
	if len(rules) == 0 {
		return nil, nil
	}

	// EnvoyPatchPolicy.TargetRef is singular, so multi-parent routes only get the first one patched.
	if len(aiGatewayRoute.Spec.ParentRefs) == 0 {
		return nil, nil
	}
	parentRef := aiGatewayRoute.Spec.ParentRefs[0]
	if len(aiGatewayRoute.Spec.ParentRefs) > 1 {
		c.logger.Info("AIGatewayRoute references multiple parents; FirstTokenTimeout patch will target only the first",
			"namespace", aiGatewayRoute.Namespace, "name", aiGatewayRoute.Name,
			"parentRef", parentRef.Name)
	}

	gw, err := c.resolveGatewayForFirstTokenTimeout(ctx, aiGatewayRoute, parentRef)
	if err != nil {
		return nil, err
	}
	if gw == nil {
		return nil, nil
	}

	var patches []egv1a1.EnvoyJSONPatchConfig
	for _, listener := range gw.Spec.Listeners {
		if listener.Protocol != gwapiv1.HTTPProtocolType && listener.Protocol != gwapiv1.HTTPSProtocolType {
			continue
		}
		routeConfigName := fmt.Sprintf("%s/%s/%s", gw.Namespace, gw.Name, listener.Name)
		for _, r := range rules {
			// Envoy Gateway xDS route naming: "httproute/<ns>/<name>/rule/<idx>/match/<midx>[/<host>]".
			routeNamePrefix := fmt.Sprintf("httproute/%s/%s/rule/%d/",
				aiGatewayRoute.Namespace, aiGatewayRoute.Name, r.idx)
			patches = append(patches, egv1a1.EnvoyJSONPatchConfig{
				Type: egv1a1.RouteConfigurationEnvoyResourceType,
				Name: routeConfigName,
				Operation: egv1a1.JSONPatchOperation{
					Op:       "add",
					JSONPath: new(fmt.Sprintf("$..routes[?(@.name ^= '%s')]", routeNamePrefix)),
					Path:     new("/route/idleTimeout"),
					Value:    durationJSONValue(r.duration),
				},
			})
		}
	}
	if len(patches) == 0 {
		return nil, nil
	}

	return &egv1a1.EnvoyPatchPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      firstTokenTimeoutPolicyName(aiGatewayRoute),
			Namespace: aiGatewayRoute.Namespace,
		},
		Spec: egv1a1.EnvoyPatchPolicySpec{
			Type:        egv1a1.JSONPatchEnvoyPatchType,
			JSONPatches: patches,
			TargetRef: gwapiv1.LocalPolicyTargetReference{
				Group: gwapiv1.GroupName,
				Kind:  "Gateway",
				Name:  parentRef.Name,
			},
		},
	}, nil
}

// resolveGatewayForFirstTokenTimeout returns (nil, nil) when the parent Gateway is not yet
// present so the caller emits no patch; the reconciler is re-triggered when it appears.
func (c *AIGatewayRouteController) resolveGatewayForFirstTokenTimeout(ctx context.Context, aiGatewayRoute *aigv1b1.AIGatewayRoute, parentRef gwapiv1.ParentReference) (*gwapiv1.Gateway, error) {
	ns := aiGatewayRoute.Namespace
	if parentRef.Namespace != nil {
		ns = string(*parentRef.Namespace)
	}
	var gw gwapiv1.Gateway
	if err := c.client.Get(ctx, client.ObjectKey{Name: string(parentRef.Name), Namespace: ns}, &gw); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get parent Gateway %s/%s: %w", ns, parentRef.Name, err)
	}
	return &gw, nil
}

// durationJSONValue encodes d as a google.protobuf.Duration JSON literal (e.g. "10s", "1.5s").
// time.Duration.String() can't be reused — it emits forms like "1m30s" / "500ms" that the
// Envoy xDS validator rejects.
func durationJSONValue(d time.Duration) *apiextensionsv1.JSON {
	seconds := float64(d) / float64(time.Second)
	encoded := fmt.Sprintf("%.3f", seconds)
	for len(encoded) > 0 && encoded[len(encoded)-1] == '0' {
		encoded = encoded[:len(encoded)-1]
	}
	if len(encoded) > 0 && encoded[len(encoded)-1] == '.' {
		encoded = encoded[:len(encoded)-1]
	}
	return &apiextensionsv1.JSON{Raw: fmt.Appendf(nil, `"%ss"`, encoded)}
}
