// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/envoyproxy/gateway/cmd/envoy-gateway/root"
)

// This contains the self-signed certificates that are used for communication between the Envoy Gateway and Envoy.
// https://gateway.envoyproxy.io/docs/tasks/operations/standalone-deployment-mode/#running-locally-on-the-host-machine
// This can be regenerated by running the following command (though there's no need to do so):
//
//	docker run --rm --volume ${root of repo}/cmd/aigw/:/tmp/envoy-gateway envoyproxy/gateway:v1.3.0 certgen --local
//
//go:embed certs/*
var selfSignedCerts embed.FS

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
	// envoyGatewayResourcesOut is the output file for the envoy gateway resources.
	envoyGatewayResourcesOut io.Writer
	// stderrLogger is the logger for stderr.
	stderrLogger *slog.Logger
	// tmpdir is the temporary directory for the resources.
	tmpdir string
}

// run starts the AI Gateway locally for a given configuration.
//
// This will create three temporary directories and files:
//  1. /tmp/envoy-gateway/certs: Contains the self-signed certificates. Currently, this is not configurable:
//     https://github.com/envoyproxy/gateway/blob/779c0a6bbdf7dacbf25a730140a112f99c239f0e/internal/infrastructure/host/infra.go#L22-L23
//  2. ${os.TempDir}/envoy-gateway-config.yaml: This contains the configuration for the Envoy Gateway agent to run, derived from envoyGatewayConfig.
//  3. ${os.TempDir}/envoy-ai-gateway-resources: This will contain the EG resource generated by the translation and deployed by EG.
func run(ctx context.Context, c cmdRun, _, stderr io.Writer) error {
	if !c.Debug {
		stderr = io.Discard
	}
	stderrLogger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{}))

	// 1. Create the self-signed certificates used for communication between the EG and Envoy.
	const selfSignedCertsDst = "/tmp/envoy-gateway/certs"
	stderrLogger.Info("Setting up self-signed certificates", "path", selfSignedCertsDst)
	if err := mustSetUpSelfSignedCerts(selfSignedCertsDst); err != nil {
		return err
	}

	tmpdir := os.TempDir()
	egConfigPath := filepath.Join(tmpdir, "envoy-gateway-config.yaml")      // 2. The path to the Envoy Gateway config.
	resourcesTmpdir := filepath.Join(tmpdir, "/envoy-ai-gateway-resources") // 3. The path to the resources.
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
	runCtx := &runCmdContext{envoyGatewayResourcesOut: f, stderrLogger: stderrLogger, tmpdir: tmpdir}
	// Use the default configuration if the path is not given.
	aiGatewayReourcesYaml := aiGatewayDefaultConfig
	if c.Path != "" {
		var yamlBytes []byte
		yamlBytes, err = os.ReadFile(c.Path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", c.Path, err)
		}
		aiGatewayReourcesYaml = string(yamlBytes)
	}
	err = runCtx.writeEnvoyResourcesAndRunExtProc(ctx, aiGatewayReourcesYaml)
	if err != nil {
		return err
	}

	// At this point, we have three things prepared:
	//  1. The self-signed certificates in /tmp/envoy-gateway/certs.
	//  2. The Envoy Gateway config in egConfigPath.
	//  3. The Envoy Gateway resources in resourceYamlPath pointed by the config at egConfigPath.
	//
	// Now running the `envoy-gateway` CLI alternative below by passing `--config-path` to `egConfigPath`.
	// Then the agent will read the resources from the file pointed inside the config and start the Envoy process.

	server := root.GetRootCommand()
	egOut := &bytes.Buffer{}
	server.SetOut(egOut)
	server.SetErr(egOut)
	server.SetArgs([]string{"server", "--config-path", egConfigPath})
	if err := server.ExecuteContext(ctx); err != nil {
		return fmt.Errorf("failed to execute server: %w", err)
	}
	if c.Debug {
		stderrLogger.Info("Envoy Gateway output", "output", egOut.String())
	}
	// Even after the context is done, the goroutine managing the Envoy process might be still trying to shut it down.
	// Give it some time to do so, otherwise the process might become an orphan. This is the limitation of the current
	// API of func-e library that is used by Envoy Gateway to run the Envoy process.
	// TODO: actually fix the library and the EG accordingly.
	time.Sleep(2 * time.Second)
	return nil
}

func mustSetUpSelfSignedCerts(certsPath string) error {
	if err := recreateDir(certsPath); err != nil {
		return err
	}

	// Copy the entire certs directory to the temp directory recursively.
	dirs, err := selfSignedCerts.ReadDir("certs")
	if err != nil {
		return fmt.Errorf("failed to read directory certs: %w", err)
	}
	for _, d := range dirs {
		// Create the directory.
		err = os.Mkdir(filepath.Join(certsPath, d.Name()), 0o755)
		if err != nil {
			return fmt.Errorf("failed to create directory %s: %w", d.Name(), err)
		}

		// Copy the files.
		var files []os.DirEntry
		files, err = selfSignedCerts.ReadDir(filepath.Join("certs", d.Name()))
		if err != nil {
			return fmt.Errorf("failed to read directory %s: %w", d.Name(), err)
		}
		for _, f := range files {
			src := filepath.Join("certs", d.Name(), f.Name())
			dst := filepath.Join(certsPath, d.Name(), f.Name())
			var data []byte
			data, err = selfSignedCerts.ReadFile(src)
			if err != nil {
				return fmt.Errorf("failed to read file %s: %w", src, err)
			}
			err = os.WriteFile(dst, data, 0o600)
			if err != nil {
				return fmt.Errorf("failed to write file %s: %w", dst, err)
			}
		}
	}
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

// writeEnvoyResourcesAndRunExtProc reads all resources from the given string, writes them to runCtx.envoyGatewayResourcesOut.
// Then, this runs external processes for EnvoyExtensionPolicy resources.
func (runCtx *runCmdContext) writeEnvoyResourcesAndRunExtProc(context.Context, string) error {
	// TODO.
	return nil
}
