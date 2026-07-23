// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// awsHandler implements [Handler] for AWS Bedrock authz.
type awsHandler struct {
	credentialsProvider aws.CredentialsProvider
	signer              *v4.Signer
	region              string
}

func newAWSHandler(ctx context.Context, awsAuth *filterapi.AWSAuth) (filterapi.BackendAuthHandler, error) {
	if awsAuth == nil {
		return nil, fmt.Errorf("aws auth configuration is required")
	}

	var cfg aws.Config
	var err error

	// If credentials file is provided, use it; otherwise use default credential chain
	// which automatically handles IRSA, EKS Pod Identity, instance roles, etc.
	if len(awsAuth.CredentialFileLiteral) != 0 {
		var tmpfile *os.File
		tmpfile, err = os.CreateTemp("", "aws-credentials")
		if err != nil {
			return nil, fmt.Errorf("cannot create temp file for AWS credentials: %w", err)
		}
		defer func() {
			_ = os.Remove(tmpfile.Name())
		}()
		if _, err = tmpfile.WriteString(awsAuth.CredentialFileLiteral); err != nil {
			return nil, fmt.Errorf("cannot write AWS credentials to temp file: %w", err)
		}
		name := tmpfile.Name()
		cfg, err = config.LoadDefaultConfig(
			ctx,
			config.WithSharedCredentialsFiles([]string{name}),
			config.WithRegion(awsAuth.Region),
		)
		if err != nil {
			return nil, fmt.Errorf("cannot load from credentials file: %w", err)
		}
	} else {
		// Use default credential chain (supports IRSA, EKS Pod Identity, etc.)
		cfg, err = config.LoadDefaultConfig(
			ctx,
			config.WithRegion(awsAuth.Region),
		)
		if err != nil {
			return nil, fmt.Errorf("cannot load AWS config: %w", err)
		}
	}

	signer := v4.NewSigner()

	return &awsHandler{credentialsProvider: cfg.Credentials, signer: signer, region: awsAuth.Region}, nil
}

// resolveHost resolves the host for the signed AWS request in the following order:
//  1. the special AWSSigningHostHeader header, when present;
//  2. the :authority pseudo-header, when AWSSigningHostHeader is absent;
//  3. the host header, when both are absent;
//  4. the standard AWS Bedrock endpoint for the configured region
//     (bedrock-runtime.<region>.amazonaws.com), when all headers are absent or
//     the resolved host does not represent an amazonaws.com domain (this suffix check is
//     a safeguard to ensure we only sign requests targeting official AWS/VPCE endpoints).
func (a *awsHandler) resolveHost(requestHeaders map[string]string) string {
	host := requestHeaders[internalapi.AWSSigningHostHeader]
	if host == "" {
		host = requestHeaders[":authority"]
	}
	if host == "" {
		host = requestHeaders["host"]
	}
	if host != "" {
		hostname := host
		if h, _, err := net.SplitHostPort(host); err == nil {
			hostname = h
		}
		if !strings.HasSuffix(hostname, ".amazonaws.com") {
			host = ""
		}
	}
	if host == "" {
		host = fmt.Sprintf("bedrock-runtime.%s.amazonaws.com", a.region)
	}
	return host
}

// Do implements [Handler.Do].
//
// This assumes that during the transformation, the path is set in the header mutation as well as
// the body in the body mutation.
func (a *awsHandler) Do(ctx context.Context, requestHeaders map[string]string, mutatedBody []byte) ([]internalapi.Header, error) {
	method := requestHeaders[":method"]
	path := requestHeaders[":path"]
	host := a.resolveHost(requestHeaders)

	var body []byte
	if len(mutatedBody) > 0 {
		body = mutatedBody
	}

	payloadHash := sha256.Sum256(body)
	req, err := http.NewRequest(method,
		fmt.Sprintf("https://%s%s", host, path),
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cannot create request: %w", err)
	}
	req.Host = host
	// By setting the content length to -1, we can avoid the inclusion of the `Content-Length` header in the signature.
	// https://github.com/aws/aws-sdk-go-v2/blob/755839b2eebb246c7eec79b65404aee105196d5b/aws/signer/v4/v4.go#L427-L431
	//
	// The reason why we want to avoid this is that the ExtProc filter will remove the content-length header
	// from the request currently. Envoy will instead do "transfer-encoding: chunked" for the request body,
	// which should be acceptable for AWS Bedrock or any modern HTTP service.
	// https://github.com/envoyproxy/envoy/blob/60b2b5187cf99db79ecfc54675354997af4765ea/source/extensions/filters/http/ext_proc/processor_state.cc#L180-L183
	req.ContentLength = -1

	credentials, err := a.credentialsProvider.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot retrieve AWS credentials: %w", err)
	}

	err = a.signer.SignHTTP(ctx, credentials, req,
		hex.EncodeToString(payloadHash[:]), "bedrock", a.region, time.Now())
	if err != nil {
		return nil, fmt.Errorf("cannot sign request: %w", err)
	}

	var headers []internalapi.Header
	for key, hdr := range req.Header {
		if key == "Authorization" || strings.HasPrefix(key, "X-Amz-") {
			headers = append(headers, internalapi.Header{key, hdr[0]})
			requestHeaders[key] = hdr[0]
		}
	}
	return headers, nil
}
