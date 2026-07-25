// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package usageevent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPSink publishes UsageEvents to a configured HTTP endpoint. It is the initial
// UsageEventSink implementation described in the proposal; additional transports can
// implement the same Sink interface without changing the gateway API.
type HTTPSink struct {
	url     string
	timeout time.Duration
	client  *http.Client
}

// NewHTTPSink creates an HTTPSink that POSTs UsageEvents as JSON to url, bounding each
// publish attempt with timeout. A trailing slash on url is trimmed so callers may configure
// either form.
func NewHTTPSink(url string, timeout time.Duration) *HTTPSink {
	return &HTTPSink{
		url:     strings.TrimSuffix(url, "/"),
		timeout: timeout,
		client:  &http.Client{},
	}
}

// Publish sends event as a JSON POST body and returns nil only once the receiver has
// responded with a 2xx status within the configured timeout. Any other outcome, including a
// timeout, connection failure, or non-2xx response, is returned as an error; callers are
// expected to treat that as a dropped event and continue request processing, per the
// proposal's stateless, no-retry design.
func (s *HTTPSink) Publish(ctx context.Context, event UsageEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("usageevent: marshal event: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("usageevent: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("usageevent: publish request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("usageevent: sink returned status %d", resp.StatusCode)
	}
	return nil
}
