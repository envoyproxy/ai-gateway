// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

// requireNewFakeClientForGatewayConfig creates a fake client for GatewayConfig tests.
func requireNewFakeClientForGatewayConfig(t *testing.T) client.Client {
	t.Helper()
	builder := fake.NewClientBuilder().WithScheme(Scheme).
		WithStatusSubresource(&aigv1b1.GatewayConfig{}).
		WithIndex(&gwapiv1.Gateway{}, k8sClientIndexGatewayToGatewayConfig, gatewayToGatewayConfigIndexFunc)
	return builder.Build()
}

// createGatewayReferencingConfig creates a minimal Gateway in the default namespace annotated to
// reference configName.
func createGatewayReferencingConfig(t *testing.T, c client.Client, name, configName string) *gwapiv1.Gateway {
	t.Helper()
	gateway := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Annotations: map[string]string{
				GatewayConfigAnnotationKey: configName,
			},
		},
		Spec: gwapiv1.GatewaySpec{
			GatewayClassName: "test-class",
			Listeners: []gwapiv1.Listener{
				{
					Name:     "http",
					Port:     8080,
					Protocol: gwapiv1.HTTPProtocolType,
				},
			},
		},
	}
	require.NoError(t, c.Create(t.Context(), gateway))
	return gateway
}

// gatewayConfigWithRateLimitNamespace returns a GatewayConfig whose GlobalRateLimits read from the
// given source metadata namespace.
func gatewayConfigWithRateLimitNamespace(name, namespace string) *aigv1b1.GatewayConfig {
	return &aigv1b1.GatewayConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: aigv1b1.GatewayConfigSpec{
			GlobalRateLimits: []aigv1b1.RateLimitOverride{
				{
					MetadataKey: "llm_input_token_limit",
					Source: aigv1b1.RateLimitOverrideSource{
						FromMetadata: aigv1b1.RateLimitMetadataSource{
							Namespace: namespace,
							Key:       "input-limit",
						},
					},
				},
			},
		},
	}
}

type errorListClient struct {
	client.Client
	listErr error
}

func (c *errorListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if c.listErr != nil {
		return c.listErr
	}
	return c.Client.List(ctx, list, opts...)
}

type errorPatchClient struct {
	client.Client
	patchErr error
	// failNames restricts which object names fail with patchErr. When nil, every Patch fails.
	failNames map[string]bool
}

func (c *errorPatchClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.patchErr != nil && (c.failNames == nil || c.failNames[obj.GetName()]) {
		return c.patchErr
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestGatewayConfigController_Reconcile(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	// Create a GatewayConfig.
	gatewayConfig := &aigv1b1.GatewayConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: "default",
		},
		Spec: aigv1b1.GatewayConfigSpec{
			ExtProc: &aigv1b1.GatewayConfigExtProc{
				Kubernetes: &egv1a1.KubernetesContainerSpec{
					Env: []corev1.EnvVar{
						{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: "http://otel-collector:4317"},
					},
					Resources: &corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			},
		},
	}
	err := fakeClient.Create(t.Context(), gatewayConfig)
	require.NoError(t, err)

	// Reconcile - should succeed with no referencing Gateways.
	result, err := c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "test-config", Namespace: "default"},
	})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, result)

	// Verify status was updated to Accepted.
	var updated aigv1b1.GatewayConfig
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-config", Namespace: "default"}, &updated)
	require.NoError(t, err)
	require.Len(t, updated.Status.Conditions, 1)
	require.Equal(t, aigv1b1.ConditionTypeAccepted, updated.Status.Conditions[0].Type)
}

func TestGatewayConfigController_NotifyGateways(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	// Create a GatewayConfig.
	gatewayConfig := &aigv1b1.GatewayConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: "default",
		},
		Spec: aigv1b1.GatewayConfigSpec{
			ExtProc: &aigv1b1.GatewayConfigExtProc{
				Kubernetes: &egv1a1.KubernetesContainerSpec{
					Env: []corev1.EnvVar{
						{Name: "LOG_LEVEL", Value: "debug"},
					},
				},
			},
		},
	}
	err := fakeClient.Create(t.Context(), gatewayConfig)
	require.NoError(t, err)

	// Reconcile without any referencing Gateway - should not add finalizer.
	_, err = c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "test-config", Namespace: "default"},
	})
	require.NoError(t, err)

	var updatedConfig aigv1b1.GatewayConfig
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-config", Namespace: "default"}, &updatedConfig)
	require.NoError(t, err)
	require.Empty(t, updatedConfig.Finalizers)

	// Create a Gateway that references the GatewayConfig.
	gateway := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-gateway",
			Namespace: "default",
			Annotations: map[string]string{
				GatewayConfigAnnotationKey: "test-config",
			},
		},
		Spec: gwapiv1.GatewaySpec{
			GatewayClassName: "test-class",
			Listeners: []gwapiv1.Listener{
				{
					Name:     "http",
					Port:     8080,
					Protocol: gwapiv1.HTTPProtocolType,
				},
			},
		},
	}
	err = fakeClient.Create(t.Context(), gateway)
	require.NoError(t, err)

	// Reconcile again - should notify the Gateway and still not add any finalizer.
	_, err = c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "test-config", Namespace: "default"},
	})
	require.NoError(t, err)

	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-config", Namespace: "default"}, &updatedConfig)
	require.NoError(t, err)
	require.Empty(t, updatedConfig.Finalizers)

	// Gateway event should be sent.
	events := eventCh.RequireItemsEventually(t, 1)
	require.Len(t, events, 1)
}

func TestGatewayConfigController_MultipleGatewaysReferencing(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	// Create a GatewayConfig.
	gatewayConfig := &aigv1b1.GatewayConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared-config",
			Namespace: "default",
		},
		Spec: aigv1b1.GatewayConfigSpec{
			ExtProc: &aigv1b1.GatewayConfigExtProc{
				Kubernetes: &egv1a1.KubernetesContainerSpec{
					Env: []corev1.EnvVar{
						{Name: "SHARED_VAR", Value: "shared-value"},
					},
				},
			},
		},
	}
	err := fakeClient.Create(t.Context(), gatewayConfig)
	require.NoError(t, err)

	// Create two Gateways that reference the same GatewayConfig.
	for _, name := range []string{"gateway-1", "gateway-2"} {
		gateway := &gwapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
				Annotations: map[string]string{
					GatewayConfigAnnotationKey: "shared-config",
				},
			},
			Spec: gwapiv1.GatewaySpec{
				GatewayClassName: "test-class",
				Listeners: []gwapiv1.Listener{
					{
						Name:     "http",
						Port:     8080,
						Protocol: gwapiv1.HTTPProtocolType,
					},
				},
			},
		}
		err = fakeClient.Create(t.Context(), gateway)
		require.NoError(t, err)
	}

	// Reconcile - should notify both gateways.
	_, err = c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "shared-config", Namespace: "default"},
	})
	require.NoError(t, err)

	var updatedConfig aigv1b1.GatewayConfig
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "shared-config", Namespace: "default"}, &updatedConfig)
	require.NoError(t, err)
	require.Empty(t, updatedConfig.Finalizers)

	// Both Gateways should have been notified.
	events := eventCh.RequireItemsEventually(t, 2)
	require.Len(t, events, 2)
}

func TestGatewayConfigController_DeletionDoesNotBlock(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	deletionTime := metav1.NewTime(time.Now())

	// Create a GatewayConfig marked for deletion.
	gatewayConfig := &aigv1b1.GatewayConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-config",
			Namespace:         "default",
			DeletionTimestamp: &deletionTime,
		},
		Spec: aigv1b1.GatewayConfigSpec{},
	}
	err := fakeClient.Create(t.Context(), gatewayConfig)
	require.NoError(t, err)

	// Create a Gateway that references the GatewayConfig.
	gateway := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-gateway",
			Namespace: "default",
			Annotations: map[string]string{
				GatewayConfigAnnotationKey: "test-config",
			},
		},
		Spec: gwapiv1.GatewaySpec{
			GatewayClassName: "test-class",
			Listeners: []gwapiv1.Listener{
				{
					Name:     "http",
					Port:     8080,
					Protocol: gwapiv1.HTTPProtocolType,
				},
			},
		},
	}
	err = fakeClient.Create(t.Context(), gateway)
	require.NoError(t, err)

	// Reconcile should not block deletion and should notify the referencing Gateway.
	_, err = c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "test-config", Namespace: "default"},
	})
	require.NoError(t, err)

	events := eventCh.RequireItemsEventually(t, 1)
	require.Len(t, events, 1)
}

func TestGatewayConfigController_ReconcileNotFound(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	result, err := c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "missing-config", Namespace: "default"},
	})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, result)
	require.Empty(t, eventCh.RequireItemsEventually(t, 0))
}

func TestGatewayConfigController_ListErrorSetsNotAcceptedStatus(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	errClient := &errorListClient{
		Client:  fakeClient,
		listErr: errors.New("list failure"),
	}
	c := NewGatewayConfigController(errClient, ctrl.Log, eventCh.Ch)

	gatewayConfig := &aigv1b1.GatewayConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "list-error-config",
			Namespace: "default",
		},
		Spec: aigv1b1.GatewayConfigSpec{},
	}
	err := fakeClient.Create(t.Context(), gatewayConfig)
	require.NoError(t, err)

	_, err = c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "list-error-config", Namespace: "default"},
	})
	require.Error(t, err)

	var updated aigv1b1.GatewayConfig
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "list-error-config", Namespace: "default"}, &updated)
	require.NoError(t, err)
	require.Len(t, updated.Status.Conditions, 1)
	require.Equal(t, aigv1b1.ConditionTypeNotAccepted, updated.Status.Conditions[0].Type)
	require.Equal(t, metav1.ConditionFalse, updated.Status.Conditions[0].Status)
	require.Contains(t, updated.Status.Conditions[0].Message, "failed to find referencing Gateways")
}

func TestGatewayConfigController_GatewayReferencesNonExistingConfig(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	// Create a Gateway that references a GatewayConfig that doesn't exist (e.g., user made a typo).
	gateway := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-gateway",
			Namespace: "default",
			Annotations: map[string]string{
				GatewayConfigAnnotationKey: "typo-config", // This config will never be created
			},
		},
		Spec: gwapiv1.GatewaySpec{
			GatewayClassName: "test-class",
			Listeners: []gwapiv1.Listener{
				{
					Name:     "http",
					Port:     8080,
					Protocol: gwapiv1.HTTPProtocolType,
				},
			},
		},
	}
	err := fakeClient.Create(t.Context(), gateway)
	require.NoError(t, err)

	// Try to reconcile the non-existing GatewayConfig.
	// This should return nil (no error) since the resource doesn't exist.
	_, err = c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "typo-config", Namespace: "default"},
	})
	require.NoError(t, err)

	// No events should be sent since the GatewayConfig doesn't exist.
	require.Empty(t, eventCh.RequireItemsEventually(t, 0))
}

func TestGatewayConfigConditionsNotAccepted(t *testing.T) {
	conds := gatewayConfigConditions(aigv1b1.ConditionTypeNotAccepted, "nope")
	require.Len(t, conds, 1)
	require.Equal(t, aigv1b1.ConditionTypeNotAccepted, conds[0].Type)
	require.Equal(t, metav1.ConditionFalse, conds[0].Status)
	require.Equal(t, "nope", conds[0].Message)
}

func TestRateLimitSourceNamespacesHash(t *testing.T) {
	newConfig := func(namespaces ...string) *aigv1b1.GatewayConfig {
		gc := &aigv1b1.GatewayConfig{}
		for _, ns := range namespaces {
			gc.Spec.GlobalRateLimits = append(gc.Spec.GlobalRateLimits, aigv1b1.RateLimitOverride{
				MetadataKey: "key",
				Source: aigv1b1.RateLimitOverrideSource{
					FromMetadata: aigv1b1.RateLimitMetadataSource{Namespace: ns, Key: "k"},
				},
			})
		}
		return gc
	}

	// No source namespaces yields an empty hash, leaving the Gateway annotation unset.
	require.Empty(t, rateLimitSourceNamespacesHash(newConfig()))
	require.Empty(t, rateLimitSourceNamespacesHash(newConfig("")))

	// Order and duplicates do not affect the hash.
	require.Equal(t,
		rateLimitSourceNamespacesHash(newConfig("a", "b")),
		rateLimitSourceNamespacesHash(newConfig("b", "a", "a")),
	)

	// Different namespace sets produce different hashes.
	require.NotEqual(t,
		rateLimitSourceNamespacesHash(newConfig("a")),
		rateLimitSourceNamespacesHash(newConfig("a", "b")),
	)

	// Changing only the metadata key/value within a namespace leaves the hash unchanged.
	changed := newConfig("a")
	changed.Spec.GlobalRateLimits[0].MetadataKey = "different"
	changed.Spec.GlobalRateLimits[0].Source.FromMetadata.Key = "different"
	require.Equal(t, rateLimitSourceNamespacesHash(newConfig("a")), rateLimitSourceNamespacesHash(changed))
}

func TestGatewayConfigController_StampsRateLimitHashOnGateways(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	gatewayConfig := gatewayConfigWithRateLimitNamespace("rl-config", "envoy.filters.http.ext_authz")
	require.NoError(t, fakeClient.Create(t.Context(), gatewayConfig))
	gateway := createGatewayReferencingConfig(t, fakeClient, "rl-gateway", "rl-config")
	req := reconcile.Request{NamespacedName: client.ObjectKey{Name: "rl-config", Namespace: "default"}}

	// The first reconcile stamps the source-namespaces hash onto the referencing Gateway.
	_, err := c.Reconcile(t.Context(), req)
	require.NoError(t, err)

	var stamped gwapiv1.Gateway
	require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(gateway), &stamped))
	want := rateLimitSourceNamespacesHash(gatewayConfig)
	require.NotEmpty(t, want)
	require.Equal(t, want, stamped.Annotations[GatewayConfigRateLimitHashAnnotationKey])
	stampedResourceVersion := stamped.ResourceVersion

	// Reconciling with an unchanged config must not re-patch the Gateway, so it cannot feed back
	// into an endless reconcile loop.
	_, err = c.Reconcile(t.Context(), req)
	require.NoError(t, err)
	require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(gateway), &stamped))
	require.Equal(t, stampedResourceVersion, stamped.ResourceVersion)

	// Changing the source namespace changes the hash and re-stamps the Gateway.
	require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(gatewayConfig), gatewayConfig))
	gatewayConfig.Spec.GlobalRateLimits[0].Source.FromMetadata.Namespace = "envoy.filters.http.jwt_authn"
	require.NoError(t, fakeClient.Update(t.Context(), gatewayConfig))
	_, err = c.Reconcile(t.Context(), req)
	require.NoError(t, err)
	require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(gateway), &stamped))
	require.Equal(t, rateLimitSourceNamespacesHash(gatewayConfig), stamped.Annotations[GatewayConfigRateLimitHashAnnotationKey])
	require.NotEqual(t, want, stamped.Annotations[GatewayConfigRateLimitHashAnnotationKey])
}

func TestGatewayConfigController_HashPatchErrorSetsNotAcceptedStatus(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	patchErrClient := &errorPatchClient{Client: fakeClient, patchErr: errors.New("patch failure")}
	c := NewGatewayConfigController(patchErrClient, ctrl.Log, eventCh.Ch)

	gatewayConfig := gatewayConfigWithRateLimitNamespace("rl-config", "envoy.filters.http.ext_authz")
	require.NoError(t, fakeClient.Create(t.Context(), gatewayConfig))
	createGatewayReferencingConfig(t, fakeClient, "rl-gateway", "rl-config")

	// A failure stamping the hash annotation must surface as a reconcile error (so it requeues) and
	// set the GatewayConfig NotAccepted, rather than being swallowed as success.
	_, err := c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "rl-config", Namespace: "default"},
	})
	require.Error(t, err)

	var updated aigv1b1.GatewayConfig
	require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKey{Name: "rl-config", Namespace: "default"}, &updated))
	require.Len(t, updated.Status.Conditions, 1)
	require.Equal(t, aigv1b1.ConditionTypeNotAccepted, updated.Status.Conditions[0].Type)
	require.Equal(t, metav1.ConditionFalse, updated.Status.Conditions[0].Status)
	require.Contains(t, updated.Status.Conditions[0].Message, "failed to stamp rate-limit source-namespaces hash")
}

func TestGatewayConfigController_HashPatchPartialFailureStillNotifies(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	// Only gw2's annotation patch fails; gw1's succeeds.
	patchErrClient := &errorPatchClient{
		Client:    fakeClient,
		patchErr:  errors.New("patch failure"),
		failNames: map[string]bool{"gw2": true},
	}
	c := NewGatewayConfigController(patchErrClient, ctrl.Log, eventCh.Ch)

	gatewayConfig := gatewayConfigWithRateLimitNamespace("rl-config", "envoy.filters.http.ext_authz")
	require.NoError(t, fakeClient.Create(t.Context(), gatewayConfig))
	createGatewayReferencingConfig(t, fakeClient, "gw1", "rl-config")
	createGatewayReferencingConfig(t, fakeClient, "gw2", "rl-config")

	// One Gateway failing to stamp fails the reconcile so it requeues...
	_, err := c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "rl-config", Namespace: "default"},
	})
	require.Error(t, err)

	// ...but the healthy Gateways must still be notified (both get an event) so their filter config
	// Secret is rebuilt, and gw1's annotation must have been stamped even though gw2's failed.
	events := eventCh.RequireItemsEventually(t, 2)
	require.Len(t, events, 2)

	var gw1 gwapiv1.Gateway
	require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKey{Name: "gw1", Namespace: "default"}, &gw1))
	require.Contains(t, gw1.Annotations, GatewayConfigRateLimitHashAnnotationKey)

	var gw2 gwapiv1.Gateway
	require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKey{Name: "gw2", Namespace: "default"}, &gw2))
	require.NotContains(t, gw2.Annotations, GatewayConfigRateLimitHashAnnotationKey)
}

func TestGatewayConfigController_HashPatchNotFoundIsIgnored(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	// A Gateway deleted between the List and the Patch surfaces as NotFound.
	patchErrClient := &errorPatchClient{
		Client:   fakeClient,
		patchErr: apierrors.NewNotFound(gwapiv1.Resource("gateways"), "rl-gateway"),
	}
	c := NewGatewayConfigController(patchErrClient, ctrl.Log, eventCh.Ch)

	gatewayConfig := gatewayConfigWithRateLimitNamespace("rl-config", "envoy.filters.http.ext_authz")
	require.NoError(t, fakeClient.Create(t.Context(), gatewayConfig))
	createGatewayReferencingConfig(t, fakeClient, "rl-gateway", "rl-config")

	// A concurrently-deleted Gateway (NotFound on Patch) is benign: the reconcile succeeds and the
	// GatewayConfig stays Accepted rather than churning on requeue.
	_, err := c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "rl-config", Namespace: "default"},
	})
	require.NoError(t, err)

	var updated aigv1b1.GatewayConfig
	require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKey{Name: "rl-config", Namespace: "default"}, &updated))
	require.Len(t, updated.Status.Conditions, 1)
	require.Equal(t, aigv1b1.ConditionTypeAccepted, updated.Status.Conditions[0].Type)
}

func TestGatewayConfigController_RemovesRateLimitHashWhenSourceCleared(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	gatewayConfig := gatewayConfigWithRateLimitNamespace("rl-config", "envoy.filters.http.ext_authz")
	require.NoError(t, fakeClient.Create(t.Context(), gatewayConfig))
	gateway := createGatewayReferencingConfig(t, fakeClient, "rl-gateway", "rl-config")
	req := reconcile.Request{NamespacedName: client.ObjectKey{Name: "rl-config", Namespace: "default"}}

	_, err := c.Reconcile(t.Context(), req)
	require.NoError(t, err)
	var stamped gwapiv1.Gateway
	require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(gateway), &stamped))
	require.Contains(t, stamped.Annotations, GatewayConfigRateLimitHashAnnotationKey)

	// Clearing the rate limits removes the annotation, forcing EG to re-translate and drop the
	// stale forwarding namespace.
	require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(gatewayConfig), gatewayConfig))
	gatewayConfig.Spec.GlobalRateLimits = nil
	require.NoError(t, fakeClient.Update(t.Context(), gatewayConfig))
	_, err = c.Reconcile(t.Context(), req)
	require.NoError(t, err)

	require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(gateway), &stamped))
	require.NotContains(t, stamped.Annotations, GatewayConfigRateLimitHashAnnotationKey)
}
