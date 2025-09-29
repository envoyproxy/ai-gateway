// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// healthcheck performs an HTTP GET request to the admin server health endpoint.
// This is used by Docker HEALTHCHECK to verify the aigw admin server is responsive.
// It exits with code 0 on success (healthy) or 1 on failure (unhealthy).
func healthcheck(ctx context.Context, port int, stdout, _ io.Writer) error {
	url := fmt.Sprintf("http://localhost:%d/health", port)

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to admin server")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unhealthy: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Optionally read and print the response for debugging
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	_, _ = fmt.Fprintf(stdout, "%s", body)
	return nil
}
