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
			// Multiple files should be supported and duplicated resources should be deduplicated.
			err := translate(cmdTranslate{Paths: []string{tc.in, tc.in}}, buf, os.Stderr)
			require.NoError(t, err)
			outBuf, err := os.ReadFile(tc.out)
			require.NoError(t, err)
			outHTTPRoutes, outEnvoyExtensionPolicy, outHTTPRouteFilter,
				outConfigMaps, outSecrets, outDeployments, outServices := requireCollectTranslatedObjects(t, buf.String())
			expHTTPRoutes, expEnvoyExtensionPolicy, expHTTPRouteFilter,
				expConfigMaps, expSecrets, expDeployments, expServices := requireCollectTranslatedObjects(t, string(outBuf))
			assert.Equal(t, expHTTPRoutes, outHTTPRoutes)
			assert.Equal(t, expEnvoyExtensionPolicy, outEnvoyExtensionPolicy)
			assert.Equal(t, expHTTPRouteFilter, outHTTPRouteFilter)
			assert.Equal(t, expConfigMaps, outConfigMaps)
			assert.Equal(t, expSecrets, outSecrets)
			assert.Equal(t, expDeployments, outDeployments)
			assert.Equal(t, expServices, outServices)
		})
	}
}

func requireCollectTranslatedObjects(t *testing.T, yamlInput string) (
	outHTTPRoutes []gwapiv1.HTTPRoute,
	outEnvoyExtensionPolicy []egv1a1.EnvoyExtensionPolicy,
	outHTTPRouteFilter []egv1a1.HTTPRouteFilter,
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
		require.NoError(t, err)
		switch obj.GetKind() {
		case "HTTPRoute":
			mustExtractAndAppend(obj, &outHTTPRoutes)
		case "HTTPRouteFilter":
			mustExtractAndAppend(obj, &outHTTPRouteFilter)
		case "EnvoyExtensionPolicy":
			mustExtractAndAppend(obj, &outEnvoyExtensionPolicy)
		case "ConfigMap":
			mustExtractAndAppend(obj, &outConfigMaps)
		case "Secret":
			mustExtractAndAppend(obj, &outSecrets)
		case "Deployment":
			mustExtractAndAppend(obj, &outDeployments)
		case "Service":
			mustExtractAndAppend(obj, &outServices)
		default:
			t.Fatalf("unexpected kind: %s", obj.GetKind())
		}
	}
}

func Test_readYamlsAsString(t *testing.T) {
	tmpDir := t.TempDir()
	p1 := tmpDir + "/file1.yaml"
	err := os.WriteFile(p1, []byte("foo"), 0o600)
	require.NoError(t, err)
	p2 := tmpDir + "/file2.yaml"
	err = os.WriteFile(p2, []byte("bar"), 0o600)
	require.NoError(t, err)

	got, err := readYamlsAsString([]string{p1, p2})
	require.NoError(t, err)
	assert.Equal(t, `foo
---
bar
---
`, got)
}
