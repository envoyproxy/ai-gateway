// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_parseAndValidateFlags(t *testing.T) {
	t.Run("no flags", func(t *testing.T) {
		f, err := parseAndValidateFlags([]string{})
		require.Equal(t, "info", f.extProcLogLevel)
		require.Equal(t, "docker.io/envoyproxy/ai-gateway-extproc:latest", f.extProcImage)
		require.Equal(t, corev1.PullIfNotPresent, f.extProcImagePullPolicy)
		require.True(t, f.enableLeaderElection)
		require.Equal(t, "info", f.logLevel.String())
		require.Equal(t, ":1063", f.extensionServerPort)
		require.Equal(t, "/certs", f.tlsCertDir)
		require.Equal(t, "tls.crt", f.tlsCertName)
		require.Equal(t, "tls.key", f.tlsKeyName)
		require.Equal(t, 4*1024*1024, f.maxRecvMsgSize)
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
					tc.dash + "extProcImagePullPolicy=Always",
					tc.dash + "enableLeaderElection=false",
					tc.dash + "logLevel=debug",
					tc.dash + "port=:8080",
					tc.dash + "extProcExtraEnvVars=OTEL_SERVICE_NAME=test;OTEL_TRACES_EXPORTER=console",
					tc.dash + "spanRequestHeaderAttributes=x-session-id:session.id",
					tc.dash + "maxRecvMsgSize=33554432",
					tc.dash + "watchNamespaces=default,envoy-ai-gateway-system",
					tc.dash + "cacheSyncTimeout=5m",
				}
				f, err := parseAndValidateFlags(args)
				require.Equal(t, "debug", f.extProcLogLevel)
				require.Equal(t, "example.com/extproc:latest", f.extProcImage)
				require.Equal(t, corev1.PullAlways, f.extProcImagePullPolicy)
				require.False(t, f.enableLeaderElection)
				require.Equal(t, "debug", f.logLevel.String())
				require.Equal(t, ":8080", f.extensionServerPort)
				require.Equal(t, "OTEL_SERVICE_NAME=test;OTEL_TRACES_EXPORTER=console", f.extProcExtraEnvVars)
				require.Equal(t, "x-session-id:session.id", f.spanRequestHeaderAttributes)
				require.Equal(t, 32*1024*1024, f.maxRecvMsgSize)
				require.Equal(t, []string{"default", "envoy-ai-gateway-system"}, f.watchNamespaces)
				require.Equal(t, 5*time.Minute, f.cacheSyncTimeout)
				require.NoError(t, err)
			})
		}
	})

	t.Run("deprecated metricsRequestHeaderLabels flag", func(t *testing.T) {
		args := []string{
			"--metricsRequestHeaderLabels=x-team-id:team.id",
		}
		f, err := parseAndValidateFlags(args)
		require.NoError(t, err)
		// Verify the deprecated flag value is used for metricsRequestHeaderAttributes
		require.Equal(t, "x-team-id:team.id", f.metricsRequestHeaderAttributes)
		require.Equal(t, "x-team-id:team.id", f.metricsRequestHeaderLabels)
	})

	t.Run("new flag takes precedence over deprecated flag", func(t *testing.T) {
		args := []string{
			"--metricsRequestHeaderLabels=x-old:old.value",
			"--metricsRequestHeaderAttributes=x-new:new.value",
		}
		f, err := parseAndValidateFlags(args)
		require.NoError(t, err)
		// Verify the new flag takes precedence
		require.Equal(t, "x-new:new.value", f.metricsRequestHeaderAttributes)
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
			{
				name:   "invalid extProcImagePullPolicy",
				flags:  []string{"--extProcImagePullPolicy=invalid"},
				expErr: "invalid external processor pull policy: \"invalid\"",
			},
			{
				name:   "invalid extProcExtraEnvVars - missing value",
				flags:  []string{"--extProcExtraEnvVars=OTEL_SERVICE_NAME"},
				expErr: "invalid extProc extra env vars",
			},
			{
				name:   "invalid extProcExtraEnvVars - empty key",
				flags:  []string{"--extProcExtraEnvVars==value"},
				expErr: "invalid extProc extra env vars",
			},
			{
				name:   "invalid spanRequestHeaderAttributes - missing colon",
				flags:  []string{"--spanRequestHeaderAttributes=x-session-id"},
				expErr: "invalid tracing header attributes",
			},
			{
				name:   "invalid spanRequestHeaderAttributes - empty header",
				flags:  []string{"--spanRequestHeaderAttributes=:session.id"},
				expErr: "invalid tracing header attributes",
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

func Test_parseAndValidateFlags_extProcImagePullSecrets(t *testing.T) {
	tests := []struct {
		name     string
		flags    []string
		expected string
		wantErr  bool
	}{
		{
			name:     "no image pull secrets",
			flags:    []string{},
			expected: "",
			wantErr:  false,
		},
		{
			name:     "single image pull secret",
			flags:    []string{"--extProcImagePullSecrets=my-registry-secret"},
			expected: "my-registry-secret",
			wantErr:  false,
		},
		{
			name:     "multiple image pull secrets",
			flags:    []string{"--extProcImagePullSecrets=my-registry-secret;backup-secret;third-secret"},
			expected: "my-registry-secret;backup-secret;third-secret",
			wantErr:  false,
		},
		{
			name:     "image pull secrets with spaces",
			flags:    []string{"--extProcImagePullSecrets= my-registry-secret ; backup-secret "},
			expected: " my-registry-secret ; backup-secret ",
			wantErr:  false,
		},
		{
			name:     "empty string",
			flags:    []string{"--extProcImagePullSecrets="},
			expected: "",
			wantErr:  false,
		},
		{
			name:     "empty secret names (valid - filtered out during parsing)",
			flags:    []string{"--extProcImagePullSecrets=my-secret;;backup-secret"},
			expected: "my-secret;;backup-secret",
			wantErr:  false,
		},
		{
			name:     "only semicolons (valid - results in empty list during parsing)",
			flags:    []string{"--extProcImagePullSecrets=;;;"},
			expected: ";;;",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := parseAndValidateFlags(tt.flags)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, f.extProcImagePullSecrets)
			}
		})
	}
}

func Test_parseAndValidateFlags_watchNamespaces(t *testing.T) {
	tests := []struct {
		name     string
		flags    []string
		expected []string
	}{
		{"no watch namespaces", []string{}, nil},
		{"single watch namespace", []string{"--watchNamespaces=default"}, []string{"default"}},
		{"multiple watch namespaces", []string{"--watchNamespaces=default,envoy-ai-gateway-system"}, []string{"default", "envoy-ai-gateway-system"}},
		{"watch namespaces with spaces", []string{"--watchNamespaces= default , envoy-ai-gateway-system "}, []string{"default", "envoy-ai-gateway-system"}},
		{"empty string", []string{"--watchNamespaces="}, nil},
		{"empty namespace names", []string{"--watchNamespaces=default,,envoy-ai-gateway-system"}, []string{"default", "envoy-ai-gateway-system"}},
		{"only commas", []string{"--watchNamespaces=,,,"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := parseAndValidateFlags(tt.flags)
			require.NoError(t, err)
			require.Equal(t, tt.expected, f.watchNamespaces)
		})
	}
}

func TestSetupCache(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		c := setupCache(flags{})

		require.NotNil(t, c.DefaultTransform)
		require.Nil(t, c.DefaultNamespaces)
	})

	t.Run("empty watch namespaces", func(t *testing.T) {
		c := setupCache(flags{watchNamespaces: []string{}})

		require.NotNil(t, c.DefaultTransform)
		require.Nil(t, c.DefaultNamespaces)
	})

	t.Run("watch namespaces", func(t *testing.T) {
		c := setupCache(flags{watchNamespaces: []string{"default", "envoy-ai-gateway-system"}})

		require.NotNil(t, c.DefaultTransform)
		require.Equal(t, map[string]cache.Config{
			"default":                 {},
			"envoy-ai-gateway-system": {},
		}, c.DefaultNamespaces)
	})
}
