// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

func TestAWSHandler_bedrockHost(t *testing.T) {
	t.Run("defaults to region host when hostname empty", func(t *testing.T) {
		a := &awsHandler{region: "us-east-1"}
		require.Equal(t, "bedrock-runtime.us-east-1.amazonaws.com", a.bedrockHost())
	})
	t.Run("uses configured hostname (vpc endpoint)", func(t *testing.T) {
		vpce := "vpce-0123456789abcdef0-1a2b3c4d.bedrock-runtime.us-east-1.vpce.amazonaws.com"
		a := &awsHandler{region: "us-east-1", hostname: vpce}
		require.Equal(t, vpce, a.bedrockHost())
	})
}

// TestAWSHandler_Do_signsOverConfiguredHost proves the SigV4 signature is computed
// over the configured endpoint host (a Bedrock interface VPC endpoint FQDN), not the
// region-default public host. Envoy's Backend host-rewrite puts the vpce FQDN on the
// wire as Host, and Bedrock recomputes the signature over that Host, so the signer
// must sign over it too. This is the assertion that would have caught the earlier
// hardcoded-host regression.
func TestAWSHandler_Do_signsOverConfiguredHost(t *testing.T) {
	const (
		region = "us-east-1"
		path   = "/model/us.anthropic.claude-haiku-4-5-20251001-v1%3A0/converse-stream"
		vpce   = "vpce-0123456789abcdef0-1a2b3c4d.bedrock-runtime.us-east-1.vpce.amazonaws.com"
	)
	body := []byte(`{"messages":[{"role":"user","content":[{"text":"hi"}]}]}`)
	awsFileBody := "[default]\naws_access_key_id=AKIDEXAMPLE\naws_secret_access_key=SECRETEXAMPLE\n"

	handler, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{
		CredentialFileLiteral: awsFileBody,
		Region:                region,
		Hostname:              vpce,
	})
	require.NoError(t, err)
	awsH := handler.(*awsHandler)
	require.Equal(t, vpce, awsH.hostname)

	hdrs, err := handler.Do(t.Context(), map[string]string{":method": "POST", ":path": path}, body)
	require.NoError(t, err)
	got := stringPairsToMap(hdrs)
	require.Contains(t, got, "Authorization")
	require.Contains(t, got, "X-Amz-Date")

	// Recompute the expected Authorization independently, at the exact timestamp the
	// handler emitted, over the vpce host vs the region-default host.
	signTime, err := time.Parse("20060102T150405Z", got["X-Amz-Date"])
	require.NoError(t, err)
	creds, err := awsH.credentialsProvider.Retrieve(t.Context())
	require.NoError(t, err)

	sign := func(host string) string {
		payloadHash := sha256.Sum256(body)
		r, err := http.NewRequest("POST", "https://"+host+path, bytes.NewReader(body))
		require.NoError(t, err)
		r.ContentLength = -1
		require.NoError(t, v4.NewSigner().SignHTTP(t.Context(), creds, r,
			hex.EncodeToString(payloadHash[:]), "bedrock", region, signTime))
		return r.Header.Get("Authorization")
	}

	require.Equal(t, sign(vpce), got["Authorization"],
		"handler must sign over the configured vpce host")
	require.NotEqual(t, sign("bedrock-runtime."+region+".amazonaws.com"), got["Authorization"],
		"handler must NOT sign over the region-default host when a vpce endpoint is configured")
}

func TestRegionFromBedrockHost(t *testing.T) {
	for _, tc := range []struct{ host, want string }{
		{"vpce-0123456789abcdef0-1a2b3c4d.bedrock-runtime.us-east-1.vpce.amazonaws.com", "us-east-1"},
		{"bedrock-runtime.us-west-2.amazonaws.com", "us-west-2"},
		{"bedrock-runtime.ap-southeast-1.amazonaws.com", "ap-southeast-1"},
		{"bedrock-runtime.us-gov-west-1.amazonaws.com", "us-gov-west-1"},
		{"my-proxy.internal.corp", ""},                       // non-Bedrock host -> use configured region
		{"", ""},                                             // empty -> use configured region
		{"bedrock.us-east-1.amazonaws.com", ""},              // control-plane host, not bedrock-runtime
		{"bedrock-runtime-fips.us-east-1.amazonaws.com", ""}, // FIPS host -> use configured region
		{"bedrock-runtime.cn-north-1.amazonaws.com.cn", ""},  // China partition -> use configured region
	} {
		require.Equal(t, tc.want, regionFromBedrockHost(tc.host), tc.host)
	}
}

// TestAWSHandler_signsWithRegionDerivedFromHost proves that a malformed/mismatched
// configured region (here GCP-style "us-east1") is overridden by the region embedded
// in the Bedrock endpoint host, so the SigV4 credential scope is valid.
func TestAWSHandler_signsWithRegionDerivedFromHost(t *testing.T) {
	const vpce = "vpce-0123456789abcdef0-1a2b3c4d.bedrock-runtime.us-east-1.vpce.amazonaws.com"
	h, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{
		CredentialFileLiteral: "[default]\naws_access_key_id=AKIDEXAMPLE\naws_secret_access_key=SECRETEXAMPLE\n",
		Region:                "us-east1", // malformed -- must be ignored in favor of the host's region
		Hostname:              vpce,
	})
	require.NoError(t, err)
	require.Equal(t, "us-east-1", h.(*awsHandler).region, "region must be derived from the vpce host")

	hdrs, err := h.Do(t.Context(), map[string]string{":method": "POST", ":path": "/model/m/converse"}, []byte(`{}`))
	require.NoError(t, err)
	auth := stringPairsToMap(hdrs)["Authorization"]
	require.Contains(t, auth, "/us-east-1/bedrock/aws4_request", "credential scope must use the derived region")
	require.NotContains(t, auth, "/us-east1/bedrock/", "malformed configured region must not reach the scope")
}
