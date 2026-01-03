// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dataplane

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/tests/internal/dataplaneenv"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

const (
	// errorServerDefaultPort is a port we need to replace in envoyConfig.
	errorServerDefaultPort = 1066
	// errorServerTLSDefaultPort is a port we need to replace in envoyConfig.
	errorServerTLSDefaultPort = 1067
	eventuallyTimeout         = 20 * time.Second
	eventuallyInterval        = 10 * time.Millisecond
	fakeGCPAuthToken          = "fake-gcp-auth-token" //nolint:gosec
)

var (
	openAISchema = filterapi.VersionedAPISchema{
		Name: filterapi.APISchemaOpenAI,
		// This is backwards compatibility for existing users. Use 'prefix' after v0.5 release.
		Version: "v1",
	}
	awsBedrockSchema     = filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}
	awsAnthropicSchema   = filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSAnthropic, Version: "bedrock-2023-05-31"}
	azureOpenAISchema    = filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI, Version: "2025-01-01-preview"}
	gcpVertexAISchema    = filterapi.VersionedAPISchema{Name: filterapi.APISchemaGCPVertexAI}
	gcpAnthropicAISchema = filterapi.VersionedAPISchema{Name: filterapi.APISchemaGCPAnthropic, Version: "vertex-2023-10-16"}
	geminiSchema         = filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Prefix: "v1beta/openai"}
	groqSchema           = filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Prefix: "openai/v1"}
	grokSchema           = filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Prefix: "v1"}
	sambaNovaSchema      = filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Prefix: "v1"}
	deepInfraSchema      = filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI, Prefix: "v1/openai"}
	anthropicSchema      = filterapi.VersionedAPISchema{Name: filterapi.APISchemaAnthropic}

	testUpstreamOpenAIBackend      = filterapi.Backend{Name: "testupstream-openai", Schema: openAISchema}
	testUpstreamModelNameOverride  = filterapi.Backend{Name: "testupstream-modelname-override", ModelNameOverride: "override-model", Schema: openAISchema}
	testUpstreamAAWSBackend        = filterapi.Backend{Name: "testupstream-aws", Schema: awsBedrockSchema}
	testUpstreamAzureBackend       = filterapi.Backend{Name: "testupstream-azure", Schema: azureOpenAISchema}
	testUpstreamGCPVertexAIBackend = filterapi.Backend{Name: "testupstream-gcp-vertexai", Schema: gcpVertexAISchema, Auth: &filterapi.BackendAuth{GCPAuth: &filterapi.GCPAuth{
		AccessToken: fakeGCPAuthToken,
		Region:      "gcp-region",
		ProjectName: "gcp-project-name",
	}}}
	testUpstreamGCPAnthropicAIBackend = filterapi.Backend{Name: "testupstream-gcp-anthropicai", Schema: gcpAnthropicAISchema, Auth: &filterapi.BackendAuth{GCPAuth: &filterapi.GCPAuth{
		AccessToken: fakeGCPAuthToken,
		Region:      "gcp-region",
		ProjectName: "gcp-project-name",
	}}}
	testUpstreamAWSAnthropicBackend = filterapi.Backend{Name: "testupstream-aws-anthropic", Schema: awsAnthropicSchema}
	alwaysFailingBackend            = filterapi.Backend{Name: "always-failing-backend", Schema: openAISchema}

	testUpstreamBodyMutationBackend = filterapi.Backend{
		Name:   "testupstream-body-mutation",
		Schema: openAISchema,
		BodyMutation: &filterapi.HTTPBodyMutation{
			Set: []filterapi.HTTPBodyField{
				{Path: "temperature", Value: "0.5"},
				{Path: "max_tokens", Value: "150"},
				{Path: "custom_field", Value: "\"route-level-value\""},
			},
			Remove: []string{"stream_options"},
		},
	}

	testUpstreamBodyMutationAnthropicBackend = filterapi.Backend{
		Name:   "testupstream-body-mutation-anthropic",
		Schema: anthropicSchema,
		BodyMutation: &filterapi.HTTPBodyMutation{
			Set: []filterapi.HTTPBodyField{
				{Path: "temperature", Value: "0.7"},
				{Path: "max_tokens", Value: "200"},
			},
		},
	}

	// envoyConfig is the embedded Envoy configuration template.
	//
	//go:embed envoy.yaml
	envoyConfig string
)

// TestMain sets up the test environment once for all tests.
func TestMain(m *testing.M) {
	// This is a fake server that returns a 500 error for all requests.
	errorServerMux := http.NewServeMux()
	errorServerMux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal Server Error"))
	})

	ctx := context.Background()
	errorServerLis, errorServerPort := listen(ctx, "error server")
	errorServerTLSLis, errorServerTLSPort := listen(ctx, "error TLS server")

	envoyConfig = strings.ReplaceAll(
		envoyConfig,
		"port_value: "+strconv.Itoa(errorServerDefaultPort),
		"port_value: "+strconv.Itoa(errorServerPort),
	)

	envoyConfig = strings.ReplaceAll(
		envoyConfig,
		"port_value: "+strconv.Itoa(errorServerTLSDefaultPort),
		"port_value: "+strconv.Itoa(errorServerTLSPort),
	)

	errorServer := &http.Server{Handler: errorServerMux, ReadHeaderTimeout: 5 * time.Second}
	errorServerTLS := &http.Server{Handler: errorServerMux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := errorServer.Serve(errorServerLis); err != nil && !strings.Contains(err.Error(), "Server closed") {
			panic(fmt.Sprintf("error starting HTTP server: %v", err))
		}
	}()
	go func() {
		if err := errorServerTLS.ServeTLS(errorServerTLSLis, "testdata/server.crt", "testdata/server.key"); err != nil &&
			!strings.Contains(err.Error(), "Server closed") {
			panic(fmt.Sprintf("error starting HTTPS server: %v", err))
		}
	}()

	// Run tests.
	res := m.Run()
	_ = errorServer.Close()
	_ = errorServerTLS.Close()
	os.Exit(res)
}

func startTestEnvironment(t testing.TB, extprocConfig string, okToDumpLogOnFailure, extProcInProcess bool) *dataplaneenv.TestEnvironment {
	return dataplaneenv.StartTestEnvironment(t,
		requireUpstream, map[string]int{"upstream": 8080},
		extprocConfig, nil, envoyConfig, okToDumpLogOnFailure, extProcInProcess, 120*time.Second,
	)
}

// requireUpstream starts the external processor with the given configuration.
func requireUpstream(t testing.TB, out io.Writer, ports map[string]int) {
	s := testupstreamlib.Server{ID: "extproc_test", Logger: log.New(out, "[testupstream] ", 0)}
	l, err := net.Listen("tcp", ":"+strconv.Itoa(ports["upstream"])) // nolint: gosec
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	t.Cleanup(func() {
		_ = l.Close()
	})
	go func() {
		s.DoMain(t.Context(), l)
	}()
}

func listen(ctx context.Context, name string) (net.Listener, int) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		panic(fmt.Errorf("failed to listen for %s: %w", name, err))
	}
	return lis, lis.Addr().(*net.TCPAddr).Port
}
