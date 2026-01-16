// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package envtest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/envoyproxy/ai-gateway/internal/controller"
	"github.com/envoyproxy/ai-gateway/tests/testsinternal"
)

const defaultK8sVersion = "1.33.0"

// NewEnvTest creates a new environment for testing the controller package.
func NewEnvTest(t *testing.T) (c client.Client, cfg *rest.Config, k kubernetes.Interface) {
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	crdPath := filepath.Join("..", "..", "manifests", "charts", "ai-gateway-crds-helm", "templates")
	files, err := os.ReadDir(crdPath)
	require.NoError(t, err)
	var crds []string
	for _, file := range files {
		crds = append(crds, filepath.Join(crdPath, file.Name()))
	}
	k8sVersion := os.Getenv("ENVTEST_K8S_VERSION")
	if k8sVersion == "" {
		k8sVersion = defaultK8sVersion
	}
	t.Logf("Using Kubernetes version %s", k8sVersion)
	output, err := testsinternal.RunGoTool("setup-envtest", "use", k8sVersion, "-p", "path")
	require.NoError(t, err, "Failed to setup envtest: %s", err)
	t.Logf("Using envtest assets from %s", output)
	t.Setenv("KUBEBUILDER_ASSETS", output)

	crds = append(crds, requireThirdPartyCRDDownloaded(t))
	env := &envtest.Environment{CRDDirectoryPaths: crds}
	cfg, err = env.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		if err = env.Stop(); err != nil {
			panic(fmt.Sprintf("Failed to stop testenv: %v", err))
		}
	})

	c, err = client.New(cfg, client.Options{Scheme: controller.Scheme})
	require.NoError(t, err)
	k = kubernetes.NewForConfigOrDie(cfg)
	return c, cfg, k
}

// requireThirdPartyCRDDownloaded downloads the CRD from the Envoy Gateway Helm chart if it does not exist,
// including the compatible GWAPI CRDs.
func requireThirdPartyCRDDownloaded(t *testing.T) string {
	const path = "3rd_party_crds_for_tests.yaml"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		var f *os.File
		f, err = os.Create(path)
		defer func() {
			_ = f.Close()
		}()
		require.NoError(t, err, "Failed to create file for third-party CRD")

		helm := testsinternal.GoToolCmd("helm", "show", "crds", "oci://docker.io/envoyproxy/gateway-helm",
			"--version", "1.5.0",
		)
		helm.Stdout = f
		helm.Stderr = os.Stderr
		require.NoError(t, helm.Run(), "Failed to download third-party CRD")
	} else if err != nil {
		panic(fmt.Sprintf("Failed to check if CRD exists: %v", err))
	}
	return path
}
