// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/envoyproxy/gateway/cmd/envoy-gateway/root"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
	"github.com/envoyproxy/ai-gateway/filterapi"
)

// This is the default configuration for the AI Gateway when --path is not given.
//
//go:embed ai-gateway-default-config.yaml
var aiGatewayDefaultConfig string

// This is the template for the Envoy Gateway configuration where PLACEHOLDER_TMPDIR will be replaced with the temporary
// directory where the resources are written to.
//
//go:embed envoy-gateway-config.yaml
var envoyGatewayConfigTemplate string

type runCmdContext struct {
	isDebug bool
	// envoyGatewayResourcesOut is the output file for the envoy gateway resources.
	envoyGatewayResourcesOut io.Writer
	// stderrLogger is the logger for stderr.
	stderrLogger *slog.Logger
	// tmpdir is the temporary directory for the resources.
	tmpdir string
	// cm is the map of ConfigMaps that are generated by the translation. The key is ${namespace}-${name}.
	cm map[string]*corev1.ConfigMap
	// dm is the map of Deployments that are generated by the translation. The key is ${namespace}-${name}.
	dm map[string]*appsv1.Deployment
	// sm is the map of Secrets that are generated by the translation. The key is ${namespace}-${name}.
	sm map[string]*corev1.Secret
}

// run starts the AI Gateway locally for a given configuration.
//
// This will create a temporary directory and a file:
//  1. ${os.TempDir}/envoy-gateway-config.yaml: This contains the configuration for the Envoy Gateway agent to run, derived from envoyGatewayConfig.
//  2. ${os.TempDir}/envoy-ai-gateway-resources: This will contain the EG resource generated by the translation and deployed by EG.
func run(ctx context.Context, c cmdRun, _, stderr io.Writer) error {
	if !c.Debug {
		stderr = io.Discard
	}
	stderrLogger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{}))

	// First, we need to create the self-signed certificates used for communication between the EG and Envoy.
	// Certificates will be placed at /tmp/envoy-gateway/certs, which is currently is not configurable:
	// https://github.com/envoyproxy/gateway/blob/779c0a6bbdf7dacbf25a730140a112f99c239f0e/internal/infrastructure/host/infra.go#L22-L23
	//
	// TODO: maybe make it skip if the certs are already there, but not sure if it's worth the complexity.
	certGen := root.GetRootCommand()
	certGen.SetOut(io.Discard)
	certGen.SetErr(io.Discard)
	certGen.SetArgs([]string{"certgen", "--local"})
	if err := certGen.ExecuteContext(ctx); err != nil {
		return fmt.Errorf("failed to execute certgen: %w", err)
	}

	tmpdir := os.TempDir()
	egConfigPath := filepath.Join(tmpdir, "envoy-gateway-config.yaml")      // 1. The path to the Envoy Gateway config.
	resourcesTmpdir := filepath.Join(tmpdir, "/envoy-ai-gateway-resources") // 2. The path to the resources.
	if err := recreateDir(resourcesTmpdir); err != nil {
		return err
	}

	// Write the Envoy Gateway config which points to the resourcesTmpdir to tell Envoy Gateway where to find the resources.
	stderrLogger.Info("Writing Envoy Gateway config", "path", egConfigPath)
	err := os.WriteFile(egConfigPath, []byte(strings.ReplaceAll(
		envoyGatewayConfigTemplate, "PLACEHOLDER_TMPDIR", resourcesTmpdir),
	), 0o600)
	if err != nil {
		return fmt.Errorf("failed to write file %s: %w", egConfigPath, err)
	}

	// Write the Envoy Gateway resources into a file under resourcesTmpdir.
	resourceYamlPath := filepath.Join(resourcesTmpdir, "config.yaml")
	stderrLogger.Info("Creating Envoy Gateway resource file", "path", resourceYamlPath)
	f, err := os.Create(resourceYamlPath)
	defer func() {
		_ = f.Close()
	}()
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", resourceYamlPath, err)
	}
	// Do the translation of the given AI Gateway resources Yaml into Envoy Gateway resources and write them to the file.
	runCtx := &runCmdContext{envoyGatewayResourcesOut: f, stderrLogger: stderrLogger, tmpdir: tmpdir, isDebug: c.Debug}
	// Use the default configuration if the path is not given.
	aiGatewayResourcesYaml := aiGatewayDefaultConfig
	if c.Path != "" {
		var yamlBytes []byte
		yamlBytes, err = os.ReadFile(c.Path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", c.Path, err)
		}
		aiGatewayResourcesYaml = string(yamlBytes)
	}
	err = runCtx.writeEnvoyResourcesAndRunExtProc(ctx, aiGatewayResourcesYaml)
	if err != nil {
		return err
	}

	// At this point, we have two things prepared:
	//  1. The Envoy Gateway config in egConfigPath.
	//  2. The Envoy Gateway resources in resourceYamlPath pointed by the config at egConfigPath.
	//
	// Now running the `envoy-gateway` CLI alternative below by passing `--config-path` to `egConfigPath`.
	// Then the agent will read the resources from the file pointed inside the config and start the Envoy process.

	server := root.GetRootCommand()
	egOut := &bytes.Buffer{}
	server.SetOut(os.Stdout)
	server.SetErr(os.Stdout)
	server.SetArgs([]string{"server", "--config-path", egConfigPath})
	if err := server.ExecuteContext(ctx); err != nil {
		return fmt.Errorf("failed to execute server: %w", err)
	}
	stderrLogger.Info("Envoy Gateway output", "output", egOut.String())
	// Even after the context is done, the goroutine managing the Envoy process might be still trying to shut it down.
	// Give it some time to do so, otherwise the process might become an orphan. This is the limitation of the current
	// API of func-e library that is used by Envoy Gateway to run the Envoy process.
	// TODO: https://github.com/envoyproxy/gateway/pull/5527 will allow us to remove this.
	time.Sleep(2 * time.Second)
	return nil
}

// recreateDir removes the directory at the given path and creates a new one.
func recreateDir(path string) error {
	err := os.RemoveAll(path)
	if err != nil {
		return fmt.Errorf("failed to remove directory %s: %w", path, err)
	}
	err = os.MkdirAll(path, 0o755)
	if err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	return nil
}

// writeEnvoyResourcesAndRunExtProc reads all resources from the given string, writes them to the output file, and runs
// external processes for EnvoyExtensionPolicy resources.
func (runCtx *runCmdContext) writeEnvoyResourcesAndRunExtProc(ctx context.Context, original string) error {
	aigwRoutes, aigwBackends, backendSecurityPolicies, secrets, err := collectObjects(original, runCtx.envoyGatewayResourcesOut, runCtx.stderrLogger)
	if err != nil {
		return fmt.Errorf("error collecting: %w", err)
	}

	for _, bsp := range backendSecurityPolicies {
		spec := bsp.Spec
		if spec.AWSCredentials != nil && spec.AWSCredentials.OIDCExchangeToken != nil {
			// TODO: this is a TODO. We can make it work by generalizing the rotation logic.
			return fmt.Errorf("OIDC exchange token is not supported: %s", bsp.Name)
		}
	}

	httpRoutes, extensionPolicies, httpRouteFilter, configMaps, _, deployments, _, err := translateCustomResourceObjects(ctx, aigwRoutes, aigwBackends, backendSecurityPolicies, runCtx.stderrLogger)
	if err != nil {
		return fmt.Errorf("error translating: %w", err)
	}

	// We don't need special logic for HTTPRouteFilter, so write them now.
	for _, hrf := range httpRouteFilter.Items {
		hrf.SetOwnerReferences(nil) // We don't need owner references.
		mustWriteObj(&hrf.TypeMeta, &hrf, runCtx.envoyGatewayResourcesOut)
	}
	// Also HTTPRoutes.
	for _, hr := range httpRoutes.Items {
		runCtx.mustClearSetOwnerReferencesAndStatusAndWriteObj(&hr.TypeMeta, &hr)
	}

	// Create maps for ConfigMaps, Secrets, and Deployments for easy access.
	runCtx.dm = make(map[string]*appsv1.Deployment, len(deployments.Items))
	for i := range deployments.Items {
		d := &deployments.Items[i]
		runCtx.dm[fmt.Sprintf("%s-%s", d.Namespace, d.Name)] = d
	}
	runCtx.cm = make(map[string]*corev1.ConfigMap, len(configMaps.Items))
	for i := range configMaps.Items {
		c := &configMaps.Items[i]
		runCtx.cm[fmt.Sprintf("%s-%s", c.Namespace, c.Name)] = c
	}
	runCtx.sm = make(map[string]*corev1.Secret, len(secrets))
	for _, s := range secrets {
		runCtx.sm[fmt.Sprintf("%s-%s", s.Namespace, s.Name)] = s
	}

	for i := range extensionPolicies.Items {
		ep := &extensionPolicies.Items[i]
		if len(ep.OwnerReferences) != 1 || ep.OwnerReferences[0].Kind != "AIGatewayRoute" || ep.OwnerReferences[0].APIVersion != "aigateway.envoyproxy.io/v1alpha1" {
			runCtx.stderrLogger.Info("Ignoring non-AI Gateway managed extension policy", "policy", ep.Name)
			mustWriteObj(&ep.TypeMeta, ep, runCtx.envoyGatewayResourcesOut)
			continue
		}
		wd, port, filterCfg := runCtx.mustWriteExtensionPolicy(ep)
		runCtx.stderrLogger.Info("Running external process",
			"policy", ep.Name, "port", port,
			"working directory", wd, "config", filterCfg,
		)
		runCtx.mustStartExtProc(ctx, wd, port, filterCfg)
	}
	return nil
}

// mustWriteExtensionPolicy modifies the given EnvoyExtensionPolicy to run an external process locally, writes the
// modified policy to the output file, and returns the working directory, the port the external process is supposed to
// listen on, and the filter configuration.
//
// All the failure modes here are panics since they are the bugs of translation if they happen.
func (runCtx *runCmdContext) mustWriteExtensionPolicy(
	ep *egv1a1.EnvoyExtensionPolicy,
) (wd string, port int32, filterCfg filterapi.Config) {
	if len(ep.Spec.ExtProc) != 1 {
		panic(fmt.Sprintf("BUG: unexpected number of ext-proc items: %d", len(ep.Spec.ExtProc)))
	}

	extProc := &ep.Spec.ExtProc[0]
	if len(extProc.BackendRefs) != 1 {
		panic(fmt.Sprintf("BUG: unexpected number of backend refs: %d", len(extProc.BackendRefs)))
	}
	backendRef := &extProc.BackendRefs[0]
	ns := ep.Namespace
	if backendRef.Namespace != nil {
		ns = string(*backendRef.Namespace)
	}

	// Find the locally available port, create a Backend resource, and modify the backend ref.
	port = mustGetAvailablePort()
	backend := &egv1a1.Backend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      string(backendRef.Name),
			Namespace: ns,
		},
		Spec: egv1a1.BackendSpec{
			Endpoints: []egv1a1.BackendEndpoint{
				{IP: &egv1a1.IPEndpoint{Address: "0.0.0.0", Port: port}}, // nolint:gosec
			},
		},
	}
	mustWriteObj(&backend.TypeMeta, backend, runCtx.envoyGatewayResourcesOut)
	backendRef.Group = ptr.To[gwapiv1.Group]("gateway.envoyproxy.io")
	backendRef.Kind = ptr.To[gwapiv1.Kind]("Backend")
	backendRef.Port = nil

	// Make sure that config works locally.
	wd = filepath.Join(runCtx.tmpdir, fmt.Sprintf("envoy-ai-gateway-extproc-%s-%s", ns, string(backendRef.Name)))
	if err := recreateDir(wd); err != nil {
		panic(fmt.Sprintf("BUG: failed to create directory %s: %v", wd, err))
	}

	key := fmt.Sprintf("%s-%s", ns, backendRef.Name)
	config, ok := runCtx.cm[key]
	if !ok {
		panic(fmt.Sprintf("BUG: configmap %s not found", key))
	}
	raw, ok := config.Data["extproc-config.yaml"]
	if !ok {
		panic(fmt.Sprintf("BUG: extproc-config.yaml not found in configmap %s", key))
	}
	if err := yaml.Unmarshal([]byte(raw), &filterCfg); err != nil {
		panic(fmt.Sprintf("BUG: failed to unmarshal extproc-config.yaml: %v", err))
	}
	deployment, ok := runCtx.dm[key]
	if !ok {
		panic(fmt.Sprintf("BUG: deployment %s not found", key))
	}

	// Write the secret to the working directory.
	volumeNameToWd := make(map[string]string)
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Secret == nil {
			continue
		}
		secretKey := fmt.Sprintf("%s-%s", ns, volume.Secret.SecretName)
		s, ok := runCtx.sm[secretKey]
		if !ok {
			panic(fmt.Sprintf("BUG: secret %s not found", secretKey))
		}
		// Write the secret to the working directory.
		dir := filepath.Join(wd, secretKey)
		runCtx.stderrLogger.Info("Creating secret directory", "path", dir)
		err := os.MkdirAll(dir, 0o755)
		if err != nil {
			panic(fmt.Sprintf("BUG: failed to create directory %s: %v", dir, err))
		}
		for k, v := range s.Data {
			p := filepath.Join(dir, k)
			runCtx.stderrLogger.Info("Writing secret", "path", p)
			err := os.WriteFile(p, v, 0o600)
			if err != nil {
				panic(fmt.Sprintf("BUG: failed to write file %s: %v", p, err))
			}
		}
		for k, v := range s.StringData {
			p := filepath.Join(dir, k)
			runCtx.stderrLogger.Info("Writing secret", "path", p)
			err := os.WriteFile(p, []byte(v), 0o600)
			if err != nil {
				panic(fmt.Sprintf("BUG: failed to write file %s: %v", p, err))
			}
		}
		volumeNameToWd[volume.Name] = dir
	}

	dirMapping := make(map[string]string)
	for _, volume := range deployment.Spec.Template.Spec.Containers[0].VolumeMounts {
		dir, ok := volumeNameToWd[volume.Name]
		if !ok {
			continue
		}
		dirMapping[volume.MountPath] = dir
	}

	for i := range filterCfg.Rules {
		rule := &filterCfg.Rules[i]
		for j := range rule.Backends {
			be := &rule.Backends[j]
			if auth := be.Auth; auth != nil {
				if auth.AWSAuth != nil {
					newDir, ok := dirMapping[path.Dir(auth.AWSAuth.CredentialFileName)]
					if !ok {
						panic(fmt.Sprintf("BUG: dir %s not found in dirMapping", path.Dir(auth.AWSAuth.CredentialFileName)))
					}
					auth.AWSAuth.CredentialFileName = filepath.Join(newDir, path.Base(auth.AWSAuth.CredentialFileName))
				} else if auth.APIKey != nil {
					newDir, ok := dirMapping[path.Dir(auth.APIKey.Filename)]
					if !ok {
						panic(fmt.Sprintf("BUG: dir %s not found in dirMapping", path.Dir(auth.APIKey.Filename)))
					}
					auth.APIKey.Filename = filepath.Join(newDir, path.Base(auth.APIKey.Filename))
				}
			}
		}
	}
	runCtx.mustClearSetOwnerReferencesAndStatusAndWriteObj(&ep.TypeMeta, ep)
	return
}

// mustStartExtProc starts the external process with the given working directory, port, and filter configuration.
func (runCtx *runCmdContext) mustStartExtProc(
	ctx context.Context,
	wd string,
	port int32,
	filterCfg filterapi.Config,
) {
	configPath := filepath.Join(wd, "extproc-config.yaml")
	marshaled, err := yaml.Marshal(filterCfg)
	if err != nil {
		panic(fmt.Sprintf("BUG: failed to marshal filter config: %v", err))
	}
	err = os.WriteFile(configPath, marshaled, 0o600)
	if err != nil {
		panic(fmt.Sprintf("BUG: failed to write extension proc config: %v", err))
	}
	args := []string{
		"--configPath", configPath,
		"--extProcAddr", fmt.Sprintf(":%d", port),
		"--metricsAddr", fmt.Sprintf(":%d", mustGetAvailablePort()),
	}
	if runCtx.isDebug {
		args = append(args, "--logLevel", "debug")
	} else {
		args = append(args, "--logLevel", "warn")
	}
	go func() {
		if err := mainlib.Main(ctx, args, os.Stderr); err != nil {
			runCtx.stderrLogger.Error("Failed to run external processor", "error", err)
		}
	}()
}

// mustGetAvailablePort returns an available local port. This is used to run the external process.
//
// This function panics if it fails to find an available port. This should not happen in practice.
func mustGetAvailablePort() int32 {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		panic(fmt.Errorf("failed to lookup an available local port: %w", err))
	}
	port := l.Addr().(*net.TCPAddr).Port
	err = l.Close()
	if err != nil {
		panic(fmt.Errorf("failed to close listener: %w", err))
	}
	return int32(port) // nolint:gosec
}

// mustClearSetOwnerReferencesAndStatusAndWriteObj clears the owner references and status of the given object, marshals it
// to YAML, and writes it to the output file.
//
// The resources must not have these fields set to be run by the Envoy Gateway agent.
//
// All operation here are done in a panic if an error occurs since the error should not happen in practice.
func (runCtx *runCmdContext) mustClearSetOwnerReferencesAndStatusAndWriteObj(typedMeta *metav1.TypeMeta, obj client.Object) {
	obj.SetOwnerReferences(nil)
	mustSetGroupVersionKind(typedMeta, obj)
	marshaled, err := yaml.Marshal(obj)
	if err != nil {
		panic(err)
	}
	var raw map[string]interface{}
	err = yaml.Unmarshal(marshaled, &raw)
	if err != nil {
		panic(err)
	}
	delete(raw, "status")
	marshaled, err = yaml.Marshal(raw)
	if err != nil {
		panic(err)
	}
	_, err = runCtx.envoyGatewayResourcesOut.Write(append([]byte("---\n"), marshaled...))
	if err != nil {
		panic(err)
	}
}
