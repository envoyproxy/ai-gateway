// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gie "sigs.k8s.io/gateway-api-inference-extension/conformance"
	v1 "sigs.k8s.io/gateway-api/conformance/apis/v1"
	"sigs.k8s.io/gateway-api/conformance/utils/config"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

func TestGatewayAPIInferenceExtension(t *testing.T) {
	const manifest = "testdata/inference-extension-conformance.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))

	options := gie.DefaultOptions(t)
	options.ReportOutputPath = "./inference-extension-conformance-test-report.yaml"
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
	options.SkipTests = []string{}

	// Setup cleanup to print report even if test fails
	t.Cleanup(func() {
		if content, err := os.ReadFile(options.ReportOutputPath); err != nil {
			t.Logf("Failed to read conformance report file %s: %v", options.ReportOutputPath, err)
		} else {
			fmt.Printf("\n=== CONFORMANCE TEST REPORT (CLEANUP) ===\n")
			fmt.Printf("Report file: %s\n", options.ReportOutputPath)
			fmt.Printf("Content:\n%s\n", string(content))
			fmt.Printf("=== END OF REPORT (CLEANUP) ===\n\n")
		}
	})

	gie.RunConformanceWithOptions(t, options)
}
