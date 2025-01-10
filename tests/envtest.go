package tests

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

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
	var crds []string
	for _, crd := range []string{
		"aigateway.envoyproxy.io_llmroutes.yaml",
		"aigateway.envoyproxy.io_llmbackends.yaml",
		"aigateway.envoyproxy.io_backendsecuritypolicies.yaml",
	} {
		crds = append(crds, filepath.Join("..", "..", "manifests", "charts", "ai-gateway-helm", "crds", crd))
	}
	const (
		extensionPolicyURL = "https://raw.githubusercontent.com/envoyproxy/gateway/refs/tags/v1.2.4/charts/gateway-helm/crds/generated/gateway.envoyproxy.io_envoyextensionpolicies.yaml"
		httpRouteURL       = "https://raw.githubusercontent.com/kubernetes-sigs/gateway-api/refs/tags/v1.2.1/config/crd/standard/gateway.networking.k8s.io_httproutes.yaml"
	)
	crds = append(crds, ensureThirdPartyCRDDownloaded("envoyextensionpolicies_crd_for_tests.yaml", extensionPolicyURL))
	crds = append(crds, ensureThirdPartyCRDDownloaded("httproutes_crd_for_tests.yaml", httpRouteURL))

	env := &envtest.Environment{CRDDirectoryPaths: crds}
	cfg, err := env.Start()
	if err != nil {
		panic(fmt.Sprintf("Failed to start testenv: %v", err))
	}

	c, err = client.New(cfg, client.Options{})
	if err != nil {
		panic(fmt.Sprintf("Error initializing client: %v", err))
	}
	k = kubernetes.NewForConfigOrDie(cfg)

	controller.MustInitializeScheme(c.Scheme())
	t.Cleanup(func() {
		defer func() {
			if err := env.Stop(); err != nil {
				panic(fmt.Sprintf("Failed to stop testenv: %v", err))
			}
		}()
	})
	return c, cfg, k
}

// ensureThirdPartyCRDDownloaded downloads the CRD from the given URL if it does not exist at the given path.
// It returns the path to the CRD as-is to make it easier to use in the caller.
func ensureThirdPartyCRDDownloaded(path, url string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		crd, err := http.DefaultClient.Get(url)
		if err != nil {
			panic(fmt.Sprintf("Failed to download CRD: %v", err))
		}
		body, err := os.Create(path)
		defer func() {
			_ = crd.Body.Close()
		}()
		if err != nil {
			panic(fmt.Sprintf("Failed to create CRD file: %v", err))
		}
		if _, err := body.ReadFrom(crd.Body); err != nil {
			panic(fmt.Sprintf("Failed to write CRD file: %v", err))
		}
	} else if err != nil {
		panic(fmt.Sprintf("Failed to check if CRD exists: %v", err))
	}
	return path
}
