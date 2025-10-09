// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package aigw

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

const adminAddressPathFlag = `--admin-address-path`

// EnvoyAdminClient provides methods to check if Envoy is ready.
type EnvoyAdminClient interface {
	Port() int
	// IsReady returns true if Envoy is ready to accept requests.
	// This method has a 1-second timeout for the readiness check.
	IsReady(ctx context.Context) error
}

// NewEnvoyAdminClient creates an EnvoyAdminClient based on the provided parameters.
// If envoyAdminPort > 0, it creates an admin API client using 127.0.0.1:{envoyAdminPort}.
// If envoyAdminPort == 0, it attempts to discover the admin port from the Envoy subprocess.
// The aigwPid parameter specifies which process to check for Envoy child processes.
func NewEnvoyAdminClient(ctx context.Context, aigwPid int, envoyAdminPort int) (EnvoyAdminClient, error) {
	if envoyAdminPort > 0 {
		return &envoyAdminAPIClient{port: envoyAdminPort}, nil
	}

	// Discover the Envoy subprocess and extract the admin address path
	envoyAdminAddressPath, err := pollEnvoyAdminAddressPathFromArgs(ctx, int32(aigwPid)) // #nosec G115 -- PID fits in int32
	if err != nil {
		return nil, err
	}

	// Attempt to discover the admin port from the admin address path
	envoyAdminPort, err = pollPortFromEnvoyAddressPath(ctx, envoyAdminAddressPath)
	if err != nil {
		return nil, err
	}

	return &envoyAdminAPIClient{port: envoyAdminPort}, nil
}

// envoyAdminAPIClient checks Envoy readiness via the admin API /ready endpoint.
type envoyAdminAPIClient struct {
	port int
}

func (c *envoyAdminAPIClient) Port() int {
	return c.port
}

func (c *envoyAdminAPIClient) IsReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/ready", c.port), nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if body := strings.ToLower(strings.TrimSpace(string(body))); body != "live" {
		return fmt.Errorf("unexpected response body: %q", body)
	}
	return nil
}

// extractAdminAddressPath parses the adminAddressPathFlag flag from command
// line arguments.
func extractAdminAddressPath(cmdline []string) (string, error) {
	// Join cmdline into a single string and split by spaces to handle sh -c
	// cases (these cases are only used in tests).
	fullCmd := strings.Join(cmdline, " ")
	parts := strings.Fields(fullCmd)

	for i, arg := range parts {
		if arg == adminAddressPathFlag && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}
	return "", fmt.Errorf("%s not found in command line", adminAddressPathFlag)
}

// pollEnvoyAdminAddressPathFromArgs finds the Envoy child process and extracts
// the admin address path from its command line. This polls as the current
// goroutine happens before the Envoy subprocess is started.
func pollEnvoyAdminAddressPathFromArgs(ctx context.Context, currentPID int32) (string, error) {
	currentProc, err := process.NewProcessWithContext(ctx, currentPID)
	if err != nil {
		return "", fmt.Errorf("failed to get aigw process: %w", err)
	}

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	var envoyProc *process.Process
	var lastErr error
LOOP:
	for {
		select {
		case <-ctx.Done():
			if lastErr == nil {
				return "", errors.New("timeout waiting for Envoy process")
			}
			return "", fmt.Errorf("timeout waiting for Envoy process: %w", lastErr)
		case <-ticker.C:
			children, childErr := currentProc.ChildrenWithContext(ctx)
			if childErr != nil {
				lastErr = childErr
				continue
			}

			if len(children) == 0 {
				lastErr = errors.New("no Envoy process found")
				continue
			}

			// Assume the first child is the Envoy process
			envoyProc = children[0]
			break LOOP
		}
	}

	// Get command line args
	envoyCmdline, err := envoyProc.CmdlineSlice()
	if err != nil {
		return "", fmt.Errorf("failed to get command line of Envoy: %w", err)
	}

	// Extract admin address path
	path, err := extractAdminAddressPath(envoyCmdline)
	if err != nil {
		return "", err
	}

	// Loop until the file is valid
	for {
		select {
		case <-ctx.Done():
			if lastErr == nil {
				return "", fmt.Errorf("timeout waiting for admin address file %s", path)
			}
			return "", fmt.Errorf("timeout waiting for admin address file %s: %w", path, lastErr)
		case <-ticker.C:
			// Verify it's a file
			if info, err := os.Stat(path); err != nil {
				lastErr = err
				continue // try later
			} else if info.IsDir() {
				return "", fmt.Errorf("envoy admin address path %q is not a file", path)
			}
			return path, nil
		}
	}
}

// pollPortFromEnvoyAddressPath polls for the admin-address.txt.
// It returns the admin port number or an error if the timeout is reached.
func pollPortFromEnvoyAddressPath(ctx context.Context, envoyAdminAddressPath string) (int, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	var adminAddr string
	var lastErr error
LOOP:
	for {
		select {
		case <-ctx.Done():
			if lastErr == nil {
				return 0, fmt.Errorf("timeout waiting for Envoy admin address file %s", envoyAdminAddressPath)
			}
			return 0, fmt.Errorf("timeout waiting for Envoy admin address file %s: %w", envoyAdminAddressPath, lastErr)
		case <-ticker.C:
			data, err := os.ReadFile(envoyAdminAddressPath)
			if err != nil {
				lastErr = err
				continue
			}

			adminAddr = strings.TrimSpace(string(data))
			if adminAddr == "" {
				lastErr = fmt.Errorf("envoy admin address file %s was empty", envoyAdminAddressPath)
				continue
			}
			break LOOP
		}
	}

	// Parse as URL to extract port
	u, err := url.Parse("http://" + adminAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse Envoy's admin address %q from %s: %w", adminAddr, envoyAdminAddressPath, err)
	}

	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return 0, fmt.Errorf("failed to parse Envoy's admin port from %q: %w", adminAddr, err)
	}

	return port, nil
}
