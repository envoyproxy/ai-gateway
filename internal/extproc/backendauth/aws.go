package backendauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unsafe"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
)

// awsHandler implements [Handler] for AWS Bedrock authz.
type awsHandler struct {
	envCfg config.EnvConfig
	signer *v4.Signer
	region string
}

func newAWSHandler(_ *extprocconfig.AWSAuth) (*awsHandler, error) {
	cfg, err := config.NewEnvConfig()
	if err != nil {
		return nil, fmt.Errorf("cannot create AWS config: %w", err)
	}
	signer := v4.NewSigner()

	// TODO: configurable region during the implementation of https://github.com/envoyproxy/ai-gateway/pull/43.
	const region = "us-east-1"
	return &awsHandler{envCfg: cfg, signer: signer, region: region}, nil
}

// Do implements [Handler.Do].
//
// This assumes that during the transformation, the path is set in the header mutation as well as
// the body in the body mutation.
func (a *awsHandler) Do(requestHeaders map[string]string, headerMut *extprocv3.HeaderMutation, bodyMut *extprocv3.BodyMutation) error {
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

	err = a.signer.SignHTTP(context.Background(), a.envCfg.Credentials, req,
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
