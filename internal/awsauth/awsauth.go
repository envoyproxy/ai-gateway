// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package awsauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

type Config struct {
	Region                string
	CredentialFileLiteral string
	Profile               string
}

// Signer signs HTTP requests using AWS SigV4. It is safe for concurrent use.
type Signer struct {
	credentialsProvider aws.CredentialsProvider
	signer              *v4.Signer
}

// NewSigner creates a Signer using the given Config.
func NewSigner(ctx context.Context, cfg Config) (*Signer, error) {
	awsCfg, err := loadConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return &Signer{
		credentialsProvider: awsCfg.Credentials,
		signer:              v4.NewSigner(),
	}, nil
}

// NewSignerWithProvider constructs a Signer that uses the given credentials provider.
// It is primarily intended for unit tests that need to exercise signing failure paths.
func NewSignerWithProvider(provider aws.CredentialsProvider) *Signer {
	return &Signer{
		credentialsProvider: provider,
		signer:              v4.NewSigner(),
	}
}

func loadConfig(ctx context.Context, cfg Config) (aws.Config, error) {
	if len(cfg.CredentialFileLiteral) != 0 {
		return loadConfigFromCredentialsFile(ctx, cfg)
	}

	// use default credential chain (supports IRSA, EKS Pod Identity, etc.)
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return aws.Config{}, fmt.Errorf("cannot load AWS config: %w", err)
	}

	return awsCfg, nil
}

func loadConfigFromCredentialsFile(ctx context.Context, cfg Config) (aws.Config, error) {
	tmpfile, err := os.CreateTemp("", "aws-credentials")
	if err != nil {
		return aws.Config{}, fmt.Errorf("cannot create temp file for AWS credentials: %w", err)
	}

	defer func() {
		_ = os.Remove(tmpfile.Name())
	}()

	if _, err = tmpfile.WriteString(cfg.CredentialFileLiteral); err != nil {
		return aws.Config{}, fmt.Errorf("cannot write AWS credentials to temp file: %w", err)
	}

	profile := cfg.Profile
	if profile == "" {
		profile = "default"
	}

	sharedCfg, err := config.LoadSharedConfigProfile(ctx, profile, func(o *config.LoadSharedConfigOptions) {
		o.CredentialsFiles = []string{tmpfile.Name()}
		// Intentionally leave ConfigFiles empty so only the provided credentials file is read.
	})
	if err != nil {
		return aws.Config{}, fmt.Errorf("cannot load shared config profile %q: %w", profile, err)
	}

	if !sharedCfg.Credentials.HasKeys() {
		return aws.Config{}, fmt.Errorf("shared config profile %q does not contain any credentials", profile)
	}
	return aws.Config{
		Region:      cfg.Region,
		Credentials: credentials.StaticCredentialsProvider{Value: sharedCfg.Credentials},
	}, nil
}

// Retrieve returns the current AWS credentials from the underlying provider.
// It is primarily useful for diagnostics and tests; signing retrieves credentials
// internally on each call so they are always fresh.
func (s *Signer) Retrieve(ctx context.Context) (aws.Credentials, error) {
	return s.credentialsProvider.Retrieve(ctx)
}

// SignHTTP signs req in place using SigV4 for the given service and region.
//
// payloadHash must be the hex-encoded SHA-256 of the request body. Callers that
// already computed it (e.g. to avoid re-reading the body) can pass it directly;
// otherwise use Sign which computes it from body.
func (s *Signer) SignHTTP(ctx context.Context, req *http.Request, payloadHash, service, region string, signingTime time.Time) error {
	creds, err := s.credentialsProvider.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("cannot retrieve AWS credentials: %w", err)
	}

	if err = s.signer.SignHTTP(ctx, creds, req, payloadHash, service, region, signingTime); err != nil {
		return fmt.Errorf("cannot sign request: %w", err)
	}
	return nil
}

// Sign signs req in place using SigV4 for the given service and region, computing
// the payload hash from body. body may be nil for an empty payload.
func (s *Signer) Sign(ctx context.Context, req *http.Request, body []byte, service, region string, signingTime time.Time) error {
	payloadHash := sha256.Sum256(body)
	return s.SignHTTP(ctx, req, hex.EncodeToString(payloadHash[:]), service, region, signingTime)
}
