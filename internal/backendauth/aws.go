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
	"net/http"
	"os"
	"regexp"
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
	// hostname is the upstream endpoint host to sign over. Empty falls back to the
	// region-default bedrock-runtime.<region>.amazonaws.com.
	hostname string
}

func newAWSHandler(ctx context.Context, awsAuth *filterapi.AWSAuth) (filterapi.BackendAuthHandler, error) {
	if awsAuth == nil {
		return nil, fmt.Errorf("aws auth configuration is required")
	}

	var cfg aws.Config
	var err error

	// The SigV4 credential scope and the STS region must be the region the request
	// actually reaches. When the endpoint is a Bedrock host (canonical or an
	// interface VPC endpoint), take the region from it so a wrong/typo'd configured
	// region (e.g. "us-east1") can't break signing or STS.
	region := awsAuth.Region
	if r := regionFromBedrockHost(awsAuth.Hostname); r != "" {
		region = r
	}

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
			config.WithRegion(region),
		)
		if err != nil {
			return nil, fmt.Errorf("cannot load from credentials file: %w", err)
		}
	} else {
		// Use default credential chain (supports IRSA, EKS Pod Identity, etc.)
		cfg, err = config.LoadDefaultConfig(
			ctx,
			config.WithRegion(region),
		)
		if err != nil {
			return nil, fmt.Errorf("cannot load AWS config: %w", err)
		}
	}

	signer := v4.NewSigner()

	return &awsHandler{credentialsProvider: cfg.Credentials, signer: signer, region: region, hostname: awsAuth.Hostname}, nil
}

// bedrockHostRE matches a Bedrock endpoint host and captures its region label:
//
//	bedrock-runtime.<region>.amazonaws.com                         (canonical)
//	vpce-<id>-<hash>.bedrock-runtime.<region>.vpce.amazonaws.com   (interface VPC endpoint)
var bedrockHostRE = regexp.MustCompile(`(?:^|\.)bedrock-runtime\.([a-z0-9-]+)\.(?:vpce\.)?amazonaws\.com$`)

// regionFromBedrockHost returns the AWS region embedded in a Bedrock endpoint
// host, or "" for any non-Bedrock host (e.g. a private proxy). The endpoint's
// region is authoritative for SigV4 -- the request physically goes there -- so it
// is preferred over the configured region, which also guards against an invalid
// configured region string.
func regionFromBedrockHost(hostname string) string {
	if m := bedrockHostRE.FindStringSubmatch(hostname); m != nil {
		return m[1]
	}
	return ""
}

// bedrockHost returns the host SigV4 is signed over: the configured upstream
// endpoint hostname when set (e.g. a Bedrock interface VPC endpoint FQDN), else
// the region-default public Bedrock host. Signing over the actual upstream host
// is required because Bedrock recomputes the signature over the Host it receives
// (which Envoy's Backend host-rewrite sets to the endpoint FQDN).
func (a *awsHandler) bedrockHost() string {
	if a.hostname != "" {
		return a.hostname
	}
	return fmt.Sprintf("bedrock-runtime.%s.amazonaws.com", a.region)
}

// Do implements [Handler.Do].
//
// This assumes that during the transformation, the path is set in the header mutation as well as
// the body in the body mutation.
func (a *awsHandler) Do(ctx context.Context, requestHeaders map[string]string, mutatedBody []byte) ([]internalapi.Header, error) {
	method := requestHeaders[":method"]
	path := requestHeaders[":path"]

	var body []byte
	if len(mutatedBody) > 0 {
		body = mutatedBody
	}

	payloadHash := sha256.Sum256(body)
	req, err := http.NewRequest(method,
		fmt.Sprintf("https://%s%s", a.bedrockHost(), path),
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cannot create request: %w", err)
	}
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
