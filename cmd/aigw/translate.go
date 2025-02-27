// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	kyaml "sigs.k8s.io/yaml"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller"
)

type translateFn func(cmd cmdTranslate, stdout, stderr io.Writer) error

func translate(cmd cmdTranslate, stdout, stderr io.Writer) error {
	var buf strings.Builder
	for _, path := range cmd.Paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("error reading file %s: %w", path, err)
		}
		buf.Write(content)
		buf.WriteRune('\n')
	}

	aigwRoutes, aigwBackends, backendSecurityPolicies, err := collectCustomResourceObjects(buf.String(), stderr)
	if err != nil {
		return fmt.Errorf("error translating: %w", err)
	}

	err = translateCustomResourceObjects(aigwRoutes, aigwBackends, backendSecurityPolicies, stdout, stderr)
	if err != nil {
		return fmt.Errorf("error emitting: %w", err)
	}
	return nil
}

func translateCustomResourceObjects(aigwRoutes []*aigv1a1.AIGatewayRoute, aigwBackends []*aigv1a1.AIServiceBackend, backendSecurityPolicies []*aigv1a1.BackendSecurityPolicy, stdout, stderr io.Writer) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	builder := fake.NewClientBuilder().WithScheme(controller.Scheme).
		WithStatusSubresource(&aigv1a1.AIGatewayRoute{}).
		WithStatusSubresource(&aigv1a1.AIServiceBackend{}).
		WithStatusSubresource(&aigv1a1.BackendSecurityPolicy{})
	err := controller.ApplyIndexing(ctx, func(_ context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
		builder = builder.WithIndex(obj, field, extractValue)
		return nil
	})
	if err != nil {
		panic(err) // Should never happen.
	}
	fakeClient := builder.Build()
	fakeClientSet := fake2.NewClientset()

	bspC := controller.NewBackendSecurityPolicyController(fakeClient, fakeClientSet, logr.Discard(),
		func(context.Context, *aigv1a1.AIServiceBackend) error { return nil })
	aisbC := controller.NewAIServiceBackendController(fakeClient, fakeClientSet, logr.Discard(),
		func(context.Context, *aigv1a1.AIGatewayRoute) error { return nil })
	airC := controller.NewAIGatewayRouteController(fakeClient, fakeClientSet, logr.Discard(),
		"docker.io/envoyproxy/ai-gateway-extproc:latest",
		"info",
	)
	for _, bsp := range backendSecurityPolicies {
		fmt.Fprintf(stderr, "Fake creating BackendSecurityPolicy %s\n", bsp.Name)
		err = fakeClient.Create(ctx, bsp.DeepCopy())
		if err != nil {
			return fmt.Errorf("error creating BackendSecurityPolicy %s: %w", bsp.Name, err)
		}
		fmt.Fprintf(stderr, "Fake reconciling BackendSecurityPolicy %s\n", bsp.Name)
		_, err = bspC.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: bsp.Namespace, Name: bsp.Name}})
		if err != nil {
			return fmt.Errorf("error reconciling BackendSecurityPolicy %s: %w", bsp.Name, err)
		}
	}
	for _, backend := range aigwBackends {
		fmt.Fprintf(stderr, "Fake creating AIServiceBackend %s\n", backend.Name)
		err = fakeClient.Create(ctx, backend.DeepCopy())
		if err != nil {
			return fmt.Errorf("error creating AIServiceBackend %s: %w", backend.Name, err)
		}
		fmt.Fprintf(stderr, "Fake reconciling AIServiceBackend %s\n", backend.Name)
		_, err = aisbC.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: backend.Namespace, Name: backend.Name}})
		if err != nil {
			return fmt.Errorf("error reconciling AIServiceBackend %s: %w", backend.Name, err)
		}
	}
	for _, route := range aigwRoutes {
		fmt.Fprintf(stderr, "Fake creating AIGatewayRoute %s\n", route.Name)
		err = fakeClient.Create(ctx, route.DeepCopy())
		if err != nil {
			return fmt.Errorf("error creating AIGatewayRoute %s: %w", route.Name, err)
		}
		fmt.Fprintf(stderr, "Fake reconciling AIGatewayRoute %s\n", route.Name)
		_, err = airC.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: route.Namespace, Name: route.Name}})
		if err != nil {
			return fmt.Errorf("error reconciling AIGatewayRoute %s: %w", route.Name, err)
		}
	}

	// Now you can retrieve the translated objects from the fake client: HTTPRoutes.
	var httpRoutes gwapiv1.HTTPRouteList
	err = fakeClient.List(ctx, &httpRoutes)
	if err != nil {
		return fmt.Errorf("error listing HTTPRoutes: %w", err)
	}
	var extensionPolicies egv1a1.EnvoyExtensionPolicyList
	err = fakeClient.List(ctx, &extensionPolicies)
	if err != nil {
		return fmt.Errorf("error listing EnvoyExtensionPolicies: %w", err)
	}
	configMaps, err := fakeClientSet.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing ConfigMaps: %w", err)
	}
	secrets, err := fakeClientSet.CoreV1().Secrets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing Secrets: %w", err)
	}
	deployments, err := fakeClientSet.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing Deployments: %w", err)
	}
	services, err := fakeClientSet.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing Services: %w", err)
	}

	// Emit the translated objects.
	for _, httpRoute := range httpRoutes.Items {
		_, _ = stdout.Write([]byte("---\n"))
		marshaled, err := kyaml.Marshal(httpRoute)
		if err != nil {
			return fmt.Errorf("error marshaling HTTPRoute: %w", err)
		}
		_, _ = stdout.Write(marshaled)
	}
	for _, extensionPolicy := range extensionPolicies.Items {
		_, _ = stdout.Write([]byte("---\n"))
		marshaled, err := kyaml.Marshal(extensionPolicy)
		if err != nil {
			return fmt.Errorf("error marshaling EnvoyExtensionPolicy: %w", err)
		}
		_, _ = stdout.Write(marshaled)
	}
	for _, configMap := range configMaps.Items {
		_, _ = stdout.Write([]byte("---\n"))
		configMap.ManagedFields = nil
		marshaled, err := kyaml.Marshal(configMap)
		if err != nil {
			return fmt.Errorf("error marshaling ConfigMap: %w", err)
		}
		_, _ = stdout.Write(marshaled)
	}
	for _, secret := range secrets.Items {
		_, _ = stdout.Write([]byte("---\n"))
		secret.ManagedFields = nil
		marshaled, err := kyaml.Marshal(secret)
		if err != nil {
			return fmt.Errorf("error marshaling Secret: %w", err)
		}
		_, _ = stdout.Write(marshaled)
	}
	for _, deployment := range deployments.Items {
		_, _ = stdout.Write([]byte("---\n"))
		deployment.ManagedFields = nil
		marshaled, err := kyaml.Marshal(deployment)
		if err != nil {
			return fmt.Errorf("error marshaling Deployment: %w", err)
		}
		_, _ = stdout.Write(marshaled)
	}
	for _, service := range services.Items {
		_, _ = stdout.Write([]byte("---\n"))
		service.ManagedFields = nil
		marshaled, err := kyaml.Marshal(service)
		if err != nil {
			return fmt.Errorf("error marshaling Service: %w", err)
		}
		_, _ = stdout.Write(marshaled)
	}
	return nil
}

func collectCustomResourceObjects(yamlInput string, stderr io.Writer) (
	aigwRoutes []*aigv1a1.AIGatewayRoute,
	aigwBackends []*aigv1a1.AIServiceBackend,
	backendSecurityPolicies []*aigv1a1.BackendSecurityPolicy,
	err error,
) {
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(yamlInput)), 4096)
	for {
		var rawObj runtime.RawExtension
		err = decoder.Decode(&rawObj)
		if errors.Is(err, io.EOF) {
			err = nil
			return
		} else if err != nil {
			log.Fatalf("Error decoding YAML: %v", err)
		}

		if len(rawObj.Raw) == 0 {
			continue
		}

		// Decode the raw JSON (converted from YAML) into an unstructured object.
		obj := &unstructured.Unstructured{}
		_, _, err = unstructured.UnstructuredJSONScheme.Decode(rawObj.Raw, nil, obj)
		if err != nil {
			err = fmt.Errorf("error decoding unstructured object: %w", err)
			return
		}

		switch obj.GetKind() {
		case "AIGatewayRoute":
			var route *aigv1a1.AIGatewayRoute
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.UnstructuredContent(), &route)
			if err != nil {
				err = fmt.Errorf("error converting to AIGatewayRoute: %w", err)
				return
			}
			aigwRoutes = append(aigwRoutes, route)
		case "AIServiceBackend":
			var backend *aigv1a1.AIServiceBackend
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.UnstructuredContent(), &backend)
			if err != nil {
				err = fmt.Errorf("error converting to AIServiceBackend: %w", err)
				return
			}
			aigwBackends = append(aigwBackends, backend)
		case "BackendSecurityPolicy":
			var bsp *aigv1a1.BackendSecurityPolicy
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.UnstructuredContent(), &bsp)
			if err != nil {
				err = fmt.Errorf("error converting to BackendSecurityPolicy: %w", err)
				return
			}
			backendSecurityPolicies = append(backendSecurityPolicies, bsp)
		default:
			// Now you can inspect or manipulate the CRD.
			_, _ = stderr.Write([]byte(fmt.Sprintf("Skipping non-AIGateway object: %s.%s: %s\n",
				obj.GetAPIVersion(), obj.GetKind(), obj.GetName())))
		}
	}
}
