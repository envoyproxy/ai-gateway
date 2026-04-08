// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package gcpauth provides shared GCP authentication utilities.
package gcpauth

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
)

// ProxyEnvVar is the environment variable name for configuring the GCP auth proxy URL.
const ProxyEnvVar = "AI_GATEWAY_GCP_AUTH_PROXY_URL"

// proxyURL returns the parsed proxy URL from the environment variable, or nil if not set.
func proxyURL() (*url.URL, error) {
	proxyURLStr := os.Getenv(ProxyEnvVar)
	if proxyURLStr == "" {
		return nil, nil
	}

	parsedURL, err := url.Parse(proxyURLStr)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", ProxyEnvVar, err)
	}
	return parsedURL, nil
}

// NewTransport returns an http.RoundTripper configured with proxy support
// if AI_GATEWAY_GCP_AUTH_PROXY_URL is set. If the environment variable is not set,
// it returns a default http.Transport.
func NewTransport() (http.RoundTripper, error) {
	u, err := proxyURL()
	if err != nil {
		return nil, err
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	if u != nil {
		t.Proxy = http.ProxyURL(u)
	}
	return t, nil
}

// MustNewTransport is like NewTransport but panics on error.
// This is useful for package-level variable initialization.
func MustNewTransport() http.RoundTripper {
	t, err := NewTransport()
	if err != nil {
		panic(fmt.Errorf("failed to create GCP transport: %w", err))
	}
	return t
}
