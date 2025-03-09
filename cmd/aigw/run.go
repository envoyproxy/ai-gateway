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
	"os"
	"path"
	"strings"
	"time"

	"github.com/envoyproxy/gateway/cmd/envoy-gateway/root"
)

// docker run --rm --volume ${root of repo}/cmd/aigw/:/tmp/envoy-gateway envoyproxy/gateway:v1.3.0 certgen --local
//
//go:embed certs/*
var certs embed.FS

//go:embed default.yaml
var defaultYAML []byte

const envoyGatewayConfig = `
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyGateway
gateway:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
provider:
  type: Custom
  custom:
    resource:
      type: File
      file:
        paths: ["PLACEHOLDER"]
    infrastructure:
      type: Host
      host: {}
logging:
  level:
    default: info
extensionApis:
  enableBackend: true
`

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
		err = os.Mkdir(path.Join(certsPath, d.Name()), 0o755)
		if err != nil {
			return fmt.Errorf("failed to create directory %s: %w", path.Join(certsPath, d.Name()), err)
		}
		stderrLogger.Info("copying certs", "directory", d.Name())

		// Copy the files.
		var files []os.DirEntry
		files, err = certs.ReadDir(path.Join("certs", d.Name()))
		if err != nil {
			return fmt.Errorf("failed to read directory certs/%s: %w", d.Name(), err)
		}
		for _, f := range files {
			src := path.Join("certs", d.Name(), f.Name())
			dst := path.Join(certsPath, d.Name(), f.Name())
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
	resourcesTmpdir := path.Join(tmpdir, "/envoy-ai-gateway-resources")
	mustRecreateDir(resourcesTmpdir)
	egConfigDir := path.Join(tmpdir, "/envoy-gateway-config")
	mustRecreateDir(egConfigDir)
	stderrLogger.Info("writing envoy gateway config", "dst", egConfigDir)
	egConfigPath := path.Join(egConfigDir, "envoy-gateway-config.yaml")
	err = os.WriteFile(egConfigPath, []byte(strings.ReplaceAll(
		envoyGatewayConfig, "PLACEHOLDER", resourcesTmpdir),
	), 0o600)
	if err != nil {
		return fmt.Errorf("failed to write file %s: %w", egConfigPath, err)
	}

	// Write the default.yaml to a file.
	defaultResourcePath := path.Join(resourcesTmpdir, "default.yaml")
	stderrLogger.Info("copying resources", "dst", defaultResourcePath)
	err = os.WriteFile(defaultResourcePath, defaultYAML, 0o600)
	if err != nil {
		return fmt.Errorf("failed to write file %s: %w", defaultResourcePath, err)
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
