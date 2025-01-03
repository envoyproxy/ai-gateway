//go:build celvalidation

package celvalidation

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

var c client.Client

func TestMain(m *testing.M) {
	os.Exit(runTest(m))
}

func runTest(m *testing.M) int {
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	base := filepath.Join("..", "..", "manifests", "charts", "ai-gateway-helm", "crds")

	crds := make([]string, 0, 2)
	for _, crd := range []string{
		"aigateway.envoyproxy.io_llmroutes.yaml",
		"aigateway.envoyproxy.io_llmbackends.yaml",
	} {
		crds = append(crds, filepath.Join(base, crd))
	}

	env := &envtest.Environment{CRDDirectoryPaths: crds}
	cfg, err := env.Start()
	if err != nil {
		panic(fmt.Sprintf("Failed to start testenv: %v", err))
	}

	_, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	defer func() {
		cancel()
		if err := env.Stop(); err != nil {
			panic(fmt.Sprintf("Failed to stop testenv: %v", err))
		}
	}()

	c, err = client.New(cfg, client.Options{})
	if err != nil {
		panic(fmt.Sprintf("Error initializing client: %v", err))
	}
	_ = aigv1a1.AddToScheme(c.Scheme())
	return m.Run()
}

//go:embed testdata
var tests embed.FS

func TestLLMRoutes(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(30*time.Second))
	defer cancel()

	for _, tc := range []struct {
		name   string
		expErr string
	}{
		{name: "basic.yaml"},
		{
			name:   "non_openai_schema.yaml",
			expErr: `spec.inputSchema: Invalid value: "object": failed rule: self.schema == 'OpenAI'`,
		},
		{
			name:   "unknown_schema.yaml",
			expErr: "spec.inputSchema.schema: Unsupported value: \"SomeRandomVendor\": supported values: \"OpenAI\", \"AWSBedrock\"",
		},
		{
			name:   "unsupported_match.yaml",
			expErr: "spec.rules[0].matches[0].headers: Invalid value: \"array\": currently only exact match is supported",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tests.ReadFile(path.Join("testdata/llmroutes", tc.name))
			require.NoError(t, err)

			llmRoute := &aigv1a1.LLMRoute{}
			err = yaml.UnmarshalStrict(data, llmRoute)
			require.NoError(t, err)

			if tc.expErr != "" {
				require.ErrorContains(t, c.Create(ctx, llmRoute), tc.expErr)
			} else {
				require.NoError(t, c.Create(ctx, llmRoute))
				require.NoError(t, c.Delete(ctx, llmRoute))
			}
		})
	}
}

func TestLLMBackends(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(30*time.Second))
	defer cancel()

	for _, tc := range []struct {
		name   string
		expErr string
	}{
		{name: "basic.yaml"},
		{name: "basic-eg-backend.yaml"},
		{
			name:   "unknown_schema.yaml",
			expErr: "spec.outputSchema.schema: Unsupported value: \"SomeRandomVendor\": supported values: \"OpenAI\", \"AWSBedrock\"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tests.ReadFile(path.Join("testdata/llmbackends", tc.name))
			require.NoError(t, err)

			llmBackend := &aigv1a1.LLMBackend{}
			err = yaml.UnmarshalStrict(data, llmBackend)
			require.NoError(t, err)

			if tc.expErr != "" {
				require.ErrorContains(t, c.Create(ctx, llmBackend), tc.expErr)
			} else {
				require.NoError(t, c.Create(ctx, llmBackend))
				require.NoError(t, c.Delete(ctx, llmBackend))
			}
		})
	}
}
