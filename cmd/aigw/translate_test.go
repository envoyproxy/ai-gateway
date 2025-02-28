// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func Test_translate(t *testing.T) {
	for _, tc := range []struct {
		name, in, out string
	}{
		{
			name: "basic",
			in:   "testdata/translate_basic.in.yaml",
			out:  "testdata/translate_basic.out.yaml",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			err := translate(cmdTranslate{Paths: []string{tc.in}}, buf, os.Stderr)
			require.NoError(t, err)
			outBuf, err := os.ReadFile(tc.out)
			require.NoError(t, err)
			outHTTPRoutes, outEnvoyExtensionPolicy, outConfigMaps, outSecrets, outDeployments, outServices := requireCollectTranslatedObjects(t, buf.String())

			expectedHTTPRoutes, expectedEnvoyExtensionPolicy, expectedConfigMaps, expectedSecrets, expectedDeployments, expectedServices := requireCollectTranslatedObjects(t, string(outBuf))
			assert.Equal(t, expectedHTTPRoutes, outHTTPRoutes)
			assert.Equal(t, expectedEnvoyExtensionPolicy, outEnvoyExtensionPolicy)
			assert.Equal(t, expectedConfigMaps, outConfigMaps)
			assert.Equal(t, expectedSecrets, outSecrets)
			assert.Equal(t, expectedDeployments, outDeployments)
			assert.Equal(t, expectedServices, outServices)
		})
	}
}

func requireCollectTranslatedObjects(t *testing.T, yamlInput string) (
	outHTTPRoutes []gwapiv1.HTTPRoute,
	outEnvoyExtensionPolicy []egv1a1.EnvoyExtensionPolicy,
	outConfigMaps []corev1.ConfigMap,
	outSecrets []corev1.Secret,
	outDeployments []appsv1.Deployment,
	outServices []corev1.Service,
) {
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(yamlInput)), 4096)
	for {
		var rawObj runtime.RawExtension
		err := decoder.Decode(&rawObj)
		if errors.Is(err, io.EOF) {
			return
		} else if err != nil {
			t.Fatal(err)
		}

		if len(rawObj.Raw) == 0 {
			continue
		}

		obj := &unstructured.Unstructured{}
		_, _, err = unstructured.UnstructuredJSONScheme.Decode(rawObj.Raw, nil, obj)
		a, _ := obj.MarshalJSON()
		require.NoError(t, err, cmp.Diff(string(a), ""))
		switch obj.GetKind() {
		case "HTTPRoute":
			httpRoute := &gwapiv1.HTTPRoute{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, httpRoute)
			require.NoError(t, err)
			outHTTPRoutes = append(outHTTPRoutes, *httpRoute)
		case "EnvoyExtensionPolicy":
			extensionPolicy := &egv1a1.EnvoyExtensionPolicy{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, extensionPolicy)
			require.NoError(t, err)
			outEnvoyExtensionPolicy = append(outEnvoyExtensionPolicy, *extensionPolicy)
		case "ConfigMap":
			configMap := &corev1.ConfigMap{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, configMap)
			require.NoError(t, err)
			outConfigMaps = append(outConfigMaps, *configMap)
		case "Secret":
			secret := &corev1.Secret{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, secret)
			require.NoError(t, err)
			outSecrets = append(outSecrets, *secret)
		case "Deployment":
			deployment := &appsv1.Deployment{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, deployment)
			require.NoError(t, err)
			outDeployments = append(outDeployments, *deployment)
		case "Service":
			service := &corev1.Service{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, service)
			require.NoError(t, err)
			outServices = append(outServices, *service)
		default:
			t.Fatalf("unexpected kind: %s", obj.GetKind())
		}
	}
}
