// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_parseAndValidateFlags(t *testing.T) {
	t.Run("no flags", func(t *testing.T) {
		f, err := parseAndValidateFlags([]string{})
		require.Equal(t, "info", f.extProcLogLevel)
		require.Equal(t, "docker.io/envoyproxy/ai-gateway-extproc:latest", f.extProcImage)
		require.True(t, f.enableLeaderElection)
		require.Equal(t, "info", f.logLevel.String())
		require.Equal(t, ":1063", f.extensionServerPort)
		require.Equal(t, "/certs", f.tlsCertDir)
		require.Equal(t, "tls.crt", f.tlsCertName)
		require.Equal(t, "tls.key", f.tlsKeyName)
		require.Equal(t, "envoy-gateway-system", f.envoyGatewayNamespace)
		require.NoError(t, err)
	})
	t.Run("all flags", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			dash string
		}{
			{"single dash", "-"},
			{"double dash", "--"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				args := []string{
					tc.dash + "extProcLogLevel=debug",
					tc.dash + "extProcImage=example.com/extproc:latest",
					tc.dash + "enableLeaderElection=false",
					tc.dash + "logLevel=debug",
					tc.dash + "port=:8080",
				}
				f, err := parseAndValidateFlags(args)
				require.Equal(t, "debug", f.extProcLogLevel)
				require.Equal(t, "example.com/extproc:latest", f.extProcImage)
				require.False(t, f.enableLeaderElection)
				require.Equal(t, "debug", f.logLevel.String())
				require.Equal(t, ":8080", f.extensionServerPort)
				require.NoError(t, err)
			})
		}
	})

	t.Run("invalid flags", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			flags  []string
			expErr string
		}{
			{
				name:   "invalid extProcLogLevel",
				flags:  []string{"--extProcLogLevel=invalid"},
				expErr: "invalid external processor log level: \"invalid\"",
			},
			{
				name:   "invalid logLevel",
				flags:  []string{"--logLevel=invalid"},
				expErr: "invalid log level: \"invalid\"",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := parseAndValidateFlags(tc.flags)
				require.ErrorContains(t, err, tc.expErr)
			})
		}
	})
}

func Test_maybePatchAdmissionWebhook(t *testing.T) {
	const ns = "envoy-ai-gateway-system"
	t.Setenv("POD_NAMESPACE", ns)
	c := fake.NewClientBuilder().Build()

	err := maybePatchAdmissionWebhook(t.Context(), c, "")
	require.ErrorContains(t, err, `"envoy-ai-gateway-gateway-pod-mutator.envoy-ai-gateway-system" not found`)

	w := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: mutatingWebhookConfigurationName + "." + ns,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{},
	}
	err = c.Create(t.Context(), w, &client.CreateOptions{})
	require.NoError(t, err)

	err = maybePatchAdmissionWebhook(t.Context(), c, "")
	require.ErrorContains(t, err, `expected 1 webhook in envoy-ai-gateway-gateway-pod-mutator.envoy-ai-gateway-system, got 0`)

	w.Webhooks = append(w.Webhooks, admissionregistrationv1.MutatingWebhook{
		ClientConfig: admissionregistrationv1.WebhookClientConfig{},
	})
	err = c.Update(t.Context(), w, &client.UpdateOptions{})
	require.NoError(t, err)

	err = maybePatchAdmissionWebhook(t.Context(), c, "/path/to/invalid/bundle")
	require.ErrorContains(t, err, `failed to read CA bundle: open /path/to/invalid/bundle: no such file or directory`)

	p := t.TempDir() + "/bundle"
	err = os.WriteFile(p, []byte("somebundle"), 0o600)
	require.NoError(t, err)
	err = maybePatchAdmissionWebhook(t.Context(), c, p)
	require.NoError(t, err)

	updated := &admissionregistrationv1.MutatingWebhookConfiguration{}
	err = c.Get(t.Context(), client.ObjectKey{Name: w.Name}, updated)
	require.NoError(t, err)
	require.Equal(t, updated.Webhooks[0].ClientConfig.CABundle, []byte("somebundle"))
}
