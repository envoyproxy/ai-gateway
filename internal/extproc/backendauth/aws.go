package backendauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unsafe"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

// awsHandler implements [Handler] for AWS Bedrock authz.
type awsHandler struct {
	credentials     aws.Credentials
	signer          *v4.Signer
	region          string
	roleArn         string
	oidcHandler     *oidcHandler
	credentialCache *time.Time
	proxyURL        string
}

func newAWSHandler(awsAuth *filterapi.AWSAuth) (*awsHandler, error) {
	var credentials aws.Credentials
	var region string
	var oidcHandler *oidcHandler
	var err error

	if awsAuth != nil {
		region = awsAuth.Region
		if len(awsAuth.CredentialFileName) != 0 {
			cfg, err := config.LoadDefaultConfig(
				context.Background(),
				config.WithSharedCredentialsFiles([]string{awsAuth.CredentialFileName}),
				config.WithRegion(awsAuth.Region),
			)
			if err != nil {
				return nil, fmt.Errorf("cannot load from credentials file: %w", err)
			}
			credentials, err = cfg.Credentials.Retrieve(context.Background())
			if err != nil {
				return nil, fmt.Errorf("cannot retrieve AWS credentials: %w", err)
			}
		} else if awsAuth.OIDC != nil {
			oidcHandler, err = newOIDCHandler(*awsAuth.OIDC, awsAuth.SecretKeyFileName)
			if err != nil {
				return nil, fmt.Errorf("cannot create OIDC handler: %w", err)
			}
		}
	} else {
		return nil, fmt.Errorf("aws auth configuration is required")
	}

	signer := v4.NewSigner()

	handler := &awsHandler{credentials: credentials, signer: signer, region: region, oidcHandler: oidcHandler, proxyURL: awsAuth.ProxyURL}

	go handler.updateCredentialsIfExpired()
	return handler, nil
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

	err = a.signer.SignHTTP(context.Background(), a.credentials, req,
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

func (a *awsHandler) updateSTSCredentialsIfExpired() error {
	if a.credentialCache == nil || time.Now().Before(a.credentialCache.Add(-5*time.Minute)) {
		// create sts client
		stsCfg := aws.Config{
			Region: a.region,
		}
		if a.proxyURL != "" {
			stsCfg.HTTPClient = &http.Client{
				Transport: &http.Transport{
					Proxy: func(*http.Request) (*url.URL, error) {
						return url.Parse(a.proxyURL)
					},
				},
			}
		}
		stsClient := sts.NewFromConfig(stsCfg)
		credentialsCache := aws.NewCredentialsCache(stscreds.NewWebIdentityRoleProvider(
			stsClient,
			a.roleArn,
			IdentityTokenValue(a.oidcHandler.oidcCredCache.token.AccessToken),
		))
		credentials, err := credentialsCache.Retrieve(context.TODO())
		if err != nil {
			return err
		}
		a.credentials = credentials
	}
	return nil
}

func (a *awsHandler) updateCredentialsIfExpired() {
	for {
		err := a.updateSTSCredentialsIfExpired()
		if err != nil {
			return
		}
		time.Sleep(1 * time.Minute)
	}
}
