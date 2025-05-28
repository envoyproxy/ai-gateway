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
	"strings"
	"time"
	"unsafe"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

// awsHandler implements [Handler] for AWS Bedrock authz.
type awsHandler struct {
	credentials aws.Credentials
	signer      *v4.Signer
	region      string
}

func newAWSHandler(ctx context.Context, awsAuth *filterapi.AWSAuth) (Handler, error) {
	var credentials aws.Credentials
	var region string

	if awsAuth != nil {
		region = awsAuth.Region
		if len(awsAuth.CredentialFileLiteral) != 0 {
			tmpfile, err := os.CreateTemp("", "aws-credentials")
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
			cfg, err := config.LoadDefaultConfig(
				ctx,
				config.WithSharedCredentialsFiles([]string{name}),
				config.WithRegion(awsAuth.Region),
			)
			if err != nil {
				return nil, fmt.Errorf("cannot load from credentials file: %w", err)
			}
			credentials, err = cfg.Credentials.Retrieve(ctx)
			if err != nil {
				return nil, fmt.Errorf("cannot retrieve AWS credentials: %w", err)
			}
		}
	} else {
		return nil, fmt.Errorf("aws auth configuration is required")
	}

	signer := v4.NewSigner()

	return &awsHandler{credentials: credentials, signer: signer, region: region}, nil
}

// Do implements [Handler.Do].
//
// This assumes that during the transformation, the path is set in the header mutation as well as
// the body in the body mutation.
func (a *awsHandler) Do(ctx context.Context, requestHeaders map[string]string, headerMut *extprocv3.HeaderMutation, bodyMut *extprocv3.BodyMutation) error {
	method := requestHeaders[":method"]
	path := ""
	if headerMut.SetHeaders != nil {
		for _, h := range headerMut.SetHeaders {
			if h.Header.Key == ":path" {
				if len(h.Header.Value) > 0 {
					path = h.Header.Value
				} else {
					rv := h.Header.RawValue
					path = unsafe.String(&rv[0], len(rv))
				}
				break
			}
		}
	}

	var body []byte
	if _body := bodyMut.GetBody(); len(_body) > 0 {
		body = _body
	}

	payloadHash := sha256.Sum256(body)
	req, err := http.NewRequest(method,
		fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com%s", a.region, path),
		bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cannot create request: %w", err)
	}

	err = a.signer.SignHTTP(ctx, a.credentials, req,
		hex.EncodeToString(payloadHash[:]), "bedrock", a.region, time.Now())
	if err != nil {
		return fmt.Errorf("cannot sign request: %w", err)
	}

	for key, hdr := range req.Header {
		if key == "Authorization" || strings.HasPrefix(key, "X-Amz-") {
			headerMut.SetHeaders = append(headerMut.SetHeaders, &corev3.HeaderValueOption{
				Header: &corev3.HeaderValue{Key: key, RawValue: []byte(hdr[0])}, // Assume aws-go-sdk always returns a single value.
			})
		}
	}
	return nil
}
