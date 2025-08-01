// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gie "sigs.k8s.io/gateway-api-inference-extension/conformance"
	v1 "sigs.k8s.io/gateway-api/conformance/apis/v1"
	"sigs.k8s.io/gateway-api/conformance/utils/config"
)

func TestGatewayAPIInferenceExtension(t *testing.T) {
	const manifest = "testdata/conformance.yaml"
	require.NoError(t, kubectlApplyManifest(t.Context(), manifest))

	options := gie.DefaultOptions(t)
	options.ReportOutputPath = "./conformance/envoy-ai-gateway-report.yaml"
	options.Debug = false
	options.CleanupBaseResources = true
	options.Implementation = v1.Implementation{
		Organization: "EnvoyProxy",
		Project:      "Envoy AI Gateway",
		URL:          "https://github.com/envoyproxy/ai-gateway",
		Contact:      []string{"@envoy-ai-gateway/maintainers"},
		Version:      "latest",
	}
	options.ConformanceProfiles.Insert(gie.GatewayLayerProfileName)
	defaultTimeoutConfig := config.DefaultTimeoutConfig()
	defaultTimeoutConfig.HTTPRouteMustHaveCondition = 10 * time.Second
	defaultTimeoutConfig.HTTPRouteMustNotHaveParents = 10 * time.Second
	defaultTimeoutConfig.GatewayMustHaveCondition = 10 * time.Second
	config.SetupTimeoutConfig(&defaultTimeoutConfig)
	options.TimeoutConfig = defaultTimeoutConfig
	options.GatewayClassName = "inference-pool"
	options.SkipTests = []string{
		"EppUnAvailableFailOpen",
	}

	gie.RunConformanceWithOptions(t, options)
}
