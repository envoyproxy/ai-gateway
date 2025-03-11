// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"embed"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/envoyproxy/gateway/cmd/envoy-gateway/root"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
	"github.com/envoyproxy/ai-gateway/filterapi"
)

// docker run --rm --volume ${root of repo}/cmd/aigw/:/tmp/envoy-gateway envoyproxy/gateway:v1.3.0 certgen --local
//
//go:embed certs/*
var certs embed.FS

//go:embed default.yaml
var defaultYAML string

//go:embed envoy-gateway-config.yaml
var envoyGatewayConfig string

type runCmdContext struct {
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

func run(ctx context.Context, _ cmdRun, output, stderr io.Writer) error {
	stderrLogger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{}))

	// Currently, this is not configurable:
	// https://github.com/envoyproxy/gateway/blob/779c0a6bbdf7dacbf25a730140a112f99c239f0e/internal/infrastructure/host/infra.go#L22-L23
	const certsPath = "/tmp/envoy-gateway/certs"
	mustRecreateDir(certsPath)

	// Copy the entire certs directory to the temp directory recursively.
	stderrLogger.Info("copying self-signed certs", "dst", certsPath)
	dirs, err := certs.ReadDir("certs")
	if err != nil {
		return fmt.Errorf("failed to read directory certs: %w", err)
	}
	for _, d := range dirs {
		// Create the directory.
		err = os.Mkdir(filepath.Join(certsPath, d.Name()), 0o755)
		if err != nil {
			return fmt.Errorf("failed to create directory %s: %w", filepath.Join(certsPath, d.Name()), err)
		}
		stderrLogger.Info("copying certs", "directory", d.Name())

		// Copy the files.
		var files []os.DirEntry
		files, err = certs.ReadDir(filepath.Join("certs", d.Name()))
		if err != nil {
			return fmt.Errorf("failed to read directory certs/%s: %w", d.Name(), err)
		}
		for _, f := range files {
			src := filepath.Join("certs", d.Name(), f.Name())
			dst := filepath.Join(certsPath, d.Name(), f.Name())
			stderrLogger.Info("copying file", "source", src, "destination", dst)
			var data []byte
			data, err = certs.ReadFile(src)
			if err != nil {
				return fmt.Errorf("failed to read file certs/%s/%s: %w", d.Name(), f.Name(), err)
			}
			err = os.WriteFile(dst, data, 0o600)
			if err != nil {
				return fmt.Errorf("failed to write file %s: %w", dst, err)
			}
		}
	}

	// Write the config to a file.
	tmpdir := os.TempDir()
	resourcesTmpdir := filepath.Join(tmpdir, "/envoy-ai-gateway-resources")
	mustRecreateDir(resourcesTmpdir)
	egConfigDir := filepath.Join(tmpdir, "/envoy-gateway-config")
	mustRecreateDir(egConfigDir)
	stderrLogger.Info("writing envoy gateway config", "dst", egConfigDir)
	egConfigPath := filepath.Join(egConfigDir, "envoy-gateway-config.yaml")
	err = os.WriteFile(egConfigPath, []byte(strings.ReplaceAll(
		envoyGatewayConfig, "PLACEHOLDER_TMPDIR", resourcesTmpdir),
	), 0o600)
	if err != nil {
		return fmt.Errorf("failed to write file %s: %w", egConfigPath, err)
	}

	// Write the config.yaml containing the resources.
	resourceYamlPath := filepath.Join(resourcesTmpdir, "config.yaml")
	stderrLogger.Info("Resource YAML path", "path", resourceYamlPath)
	f, err := os.Create(resourceYamlPath)
	defer func() {
		_ = f.Close()
		content, err := os.ReadFile(resourceYamlPath)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(content))
	}()
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", resourceYamlPath, err)
	}
	runCtx := &runCmdContext{envoyGatewayResourcesOut: f, stderrLogger: stderrLogger, tmpdir: tmpdir}
	err = runCtx.writeEnvoyResourcesAndRunExtProc(ctx, defaultYAML)
	if err != nil {
		return err
	}

	c := root.GetRootCommand()
	c.SetOut(output)
	c.SetArgs([]string{"server", "--config-path", egConfigPath})
	if err := c.ExecuteContext(ctx); err != nil {
		return fmt.Errorf("failed to execute server: %w", err)
	}
	// Even after the context is done, the goroutine managing the Envoy process might be still trying to shut it down.
	// Give it some time to do so, otherwise the process might become an orphan.
	time.Sleep(5 * time.Second)
	return nil
}

// mustRecreateDir removes the directory at the given path and creates a new one.
func mustRecreateDir(path string) {
	err := os.RemoveAll(path)
	if err != nil {
		panic(fmt.Errorf("failed to remove directory %s: %w", path, err))
	}
	err = os.MkdirAll(path, 0o755)
	if err != nil {
		panic(fmt.Errorf("failed to create directory %s: %w", path, err))
	}
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

	httpRoutes, extensionPolicies, httpRouteFilter, configMaps, _, deployments, _, err :=
		translateCustomResourceObjects(ctx, aigwRoutes, aigwBackends, backendSecurityPolicies, runCtx.stderrLogger)
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
		for i := range hr.Spec.Rules {
			for j := range hr.Spec.Rules[i].BackendRefs {
				backendRef := &hr.Spec.Rules[i].BackendRefs[j]
				if backendRef.Namespace == nil {
					backendRef.Namespace = ptr.To(gwapiv1.Namespace(hr.Namespace))
				}
			}
		}
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
		mustStartExtProc(ctx, wd, port, filterCfg)
	}
	return nil
}

// mustWriteExtensionPolicy modifies the given EnvoyExtensionPolicy to run an external process locally, writes the
// modified policy to the output file, and returns the working directory, the port the external process is supposed to
// listen on, and the filter configuration.
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
	mustRecreateDir(wd)

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

func mustStartExtProc(
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
		"--logLevel", "debug",
		"--configPath", configPath,
		"--extProcAddr", fmt.Sprintf(":%d", port),
		"--metricsAddr", fmt.Sprintf(":%d", mustGetAvailablePort()),
	}
	go func() {
		mainlib.Main(ctx, args)
	}()
}

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
	_, err = runCtx.envoyGatewayResourcesOut.Write(append([]byte{'-', '-', '-', '\n'}, marshaled...))
	if err != nil {
		panic(err)
	}
}
