// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testsinternal

import (
	"fmt"
	"net/http"
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
)

// NewEnvTest creates a new environment for testing the controller package.
func NewEnvTest(t *testing.T) (c client.Client, cfg *rest.Config, k kubernetes.Interface) {
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	crdPath := filepath.Join("..", "..", "manifests", "charts", "ai-gateway-helm", "crds")
	files, err := os.ReadDir(crdPath)
	require.NoError(t, err)
	var crds []string
	for _, file := range files {
		crds = append(crds, filepath.Join(crdPath, file.Name()))
	}

	for _, url := range []string{
		"https://raw.githubusercontent.com/envoyproxy/gateway/refs/tags/v1.2.4/charts/gateway-helm/crds/generated/gateway.envoyproxy.io_envoyextensionpolicies.yaml",
		"https://raw.githubusercontent.com/envoyproxy/gateway/refs/tags/v1.2.5/charts/gateway-helm/crds/generated/gateway.envoyproxy.io_httproutefilters.yaml",
		"https://raw.githubusercontent.com/kubernetes-sigs/gateway-api/refs/tags/v1.2.1/config/crd/standard/gateway.networking.k8s.io_httproutes.yaml",
	} {
		path := filepath.Base(url) + "_for_tests.yaml"
		crds = append(crds, requireThirdPartyCRDDownloaded(t, path, url))
	}

	env := &envtest.Environment{CRDDirectoryPaths: crds}
	cfg, err = env.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		if err = env.Stop(); err != nil {
			panic(fmt.Sprintf("Failed to stop testenv: %v", err))
		}
	})

	c, err = client.New(cfg, client.Options{})
	require.NoError(t, err)

	controller.MustInitializeScheme(c.Scheme())
	k = kubernetes.NewForConfigOrDie(cfg)
	return c, cfg, k
}

// requireThirdPartyCRDDownloaded downloads the CRD from the given URL if it does not exist at the given path.
// It returns the path to the CRD as-is to make it easier to use in the caller.
func requireThirdPartyCRDDownloaded(t *testing.T, path, url string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		var crd *http.Response
		crd, err = http.DefaultClient.Get(url)
		require.NoError(t, err)
		var body *os.File
		body, err = os.Create(path)
		defer func() {
			_ = crd.Body.Close()
		}()
		require.NoError(t, err)
		_, err = body.ReadFrom(crd.Body)
		require.NoError(t, err)
	} else if err != nil {
		panic(fmt.Sprintf("Failed to check if CRD exists: %v", err))
	}
	return path
}
