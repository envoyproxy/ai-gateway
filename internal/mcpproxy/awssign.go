// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/envoyproxy/ai-gateway/internal/awsauth"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

// awsBackendSigner signs outbound MCP requests to a single AWS-authenticated backend
// using AWS SigV4. It is built once per backend when configuration is loaded and reused
// across requests; the underlying signer retrieves fresh credentials on every call.
type awsBackendSigner struct {
	signer  *awsauth.Signer
	region  string
	service string
	host    string
	path    string
}

// newAwsBackendSigner builds a signer from the resolved per-backend AWS auth config.
func newAWSBackendSigner(ctx context.Context, awsAuth *filterapi.MCPBackendAWSAuth) (*awsBackendSigner, error) {
	signer, err := awsauth.NewSigner(ctx, awsauth.Config{
		Region:                awsAuth.Region,
		CredentialFileLiteral: awsAuth.CredentialFileLiteral,
		Profile:               awsAuth.Profile,
	})
	if err != nil {
		return nil, err
	}

	return &awsBackendSigner{
		signer:  signer,
		region:  awsAuth.Region,
		service: awsAuth.Service,
		host:    awsAuth.Host,
		path:    awsAuth.Path,
	}, nil
}

// sign computes SigV4 auth headers over body and applies the resulting Authorization
// and X-Amz-* headers to req.
func (a *awsBackendSigner) sign(ctx context.Context, req *http.Request, body []byte) error {
	signURL := fmt.Sprintf("https://%s%s", a.host, a.path)
	signReq, err := http.NewRequestWithContext(ctx, req.Method, signURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build AWS signing request: %w", err)
	}

	// Avoid signing Content-Length: like the Bedrock path, Envoy may switch the upstream
	// request to chunked transfer-encoding, which would otherwise invalidate the signature.
	signReq.ContentLength = -1

	if err = a.signer.Sign(ctx, signReq, body, a.service, a.region, time.Now()); err != nil {
		return err
	}

	for key, vals := range signReq.Header {
		if key == "Authorization" || strings.HasPrefix(key, "X-Amz-") {
			req.Header.Set(key, vals[0])
		}
	}

	return nil
}
