// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	func_e "github.com/tetratelabs/func-e"
	func_e_api "github.com/tetratelabs/func-e/api"
)

var envoyVersionRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// downloadEnvoy downloads the Envoy binary used by Envoy Gateway.
func downloadEnvoy(ctx context.Context, funcERun func_e_api.RunFunc, tmpDir, dataHome string, stdout, stderr io.Writer) error {
	version, err := getEnvoyVersion(egv1a1.DefaultEnvoyProxyImage)
	if err != nil {
		return err
	}

	return funcERun(ctx, []string{"--version"},
		func_e_api.ConfigHome(tmpDir),
		func_e_api.DataHome(dataHome),
		func_e_api.StateHome(tmpDir),
		func_e_api.RuntimeDir(tmpDir),
		func_e_api.RunID("0"),
		func_e_api.EnvoyVersion(version),
		func_e_api.Out(stdout),
		func_e_api.EnvoyOut(stdout),
		func_e_api.EnvoyErr(stderr),
	)
}

func downloadEnvoyCmd(ctx context.Context, c *cmdDownloadEnvoy, stdout, stderr io.Writer) error {
	return downloadEnvoy(ctx, func_e.Run, os.TempDir(), c.dataHome, stdout, stderr)
}

func getEnvoyVersion(image string) (string, error) {
	if version := os.Getenv("ENVOY_VERSION"); version != "" {
		return version, nil
	}
	parts := strings.Split(image, ":")
	if len(parts) < 2 {
		return "", fmt.Errorf("no tag in default Envoy image: %s", image)
	}
	semver := envoyVersionRe.FindString(parts[len(parts)-1])
	if semver == "" {
		return "", fmt.Errorf("no semver in tag: %s", parts[len(parts)-1])
	}
	return semver, nil
}
