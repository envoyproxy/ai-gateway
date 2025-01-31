package oidc

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

const OidcAwsPrefix = "oidc-aws-"

type AWSSpec struct {
	region      string
	expiredTime time.Time
	proxyURL    string
	roleArn     string
	namespace   string
	credentials aws.Credentials
	oidc        egv1a1.OIDC
	aud         string
}

type AWSCredentialExchange struct {
	logger    *logr.Logger
	k8sClient client.Client
	awsSpecs  map[string]*AWSSpec
}

func newAWSCredentialExchange(logger *logr.Logger, k8sClient client.Client) *AWSCredentialExchange {
	return &AWSCredentialExchange{
		logger:    logger,
		k8sClient: k8sClient,
		awsSpecs:  make(map[string]*AWSSpec),
	}
}

func (a *AWSCredentialExchange) isOIDCBackendSecurityPolicy(policy aigv1a1.BackendSecurityPolicy) bool {
	if policy.Spec.Type != aigv1a1.BackendSecurityPolicyTypeAWSCredentials {
		a.logger.Info(fmt.Sprintf("Skipping credentials refresh for type %s", policy.Spec.Type))
		return false
	}
	if policy.Spec.AWSCredentials.CredentialsFile != nil {
		a.logger.Info(fmt.Sprintf("Skiping due to credential file being set for %s", policy.Name))
		return false
	}
	return true
}

func (a *AWSCredentialExchange) createSpecIfNew(policy aigv1a1.BackendSecurityPolicy) {
	_, ok := a.awsSpecs[fmt.Sprintf("%s.%s", policy.Name, policy.Namespace)]
	if !ok {
		a.awsSpecs[fmt.Sprintf("%s.%s", policy.Name, policy.Namespace)] = &AWSSpec{
			region:      policy.Spec.AWSCredentials.Region,
			expiredTime: time.Time{},
			proxyURL:    policy.Spec.AWSCredentials.OIDCExchangeToken.ProxyURL,
			roleArn:     policy.Spec.AWSCredentials.OIDCExchangeToken.AwsRoleArn,
			namespace:   policy.Namespace,
			credentials: aws.Credentials{},
			oidc:        policy.Spec.AWSCredentials.OIDCExchangeToken.OIDC,
			aud:         policy.Spec.AWSCredentials.OIDCExchangeToken.Aud,
		}
	}
}

func (a *AWSCredentialExchange) getAud(cacheKey string) string {
	return a.awsSpecs[cacheKey].aud
}

func (a *AWSCredentialExchange) getOIDC(cacheKey string) egv1a1.OIDC {
	return a.awsSpecs[cacheKey].oidc
}

func (a *AWSCredentialExchange) updateCredentials(accessToken, cacheKey string) error {
	awsSpec, ok := a.awsSpecs[cacheKey]
	if !ok {
		return fmt.Errorf("no AWS spec found for %s", cacheKey)
	}

	// create sts client
	stsCfg := aws.Config{
		Region: awsSpec.region,
	}
	if awsSpec.proxyURL != "" {
		stsCfg.HTTPClient = &http.Client{
			Transport: &http.Transport{
				Proxy: func(*http.Request) (*url.URL, error) {
					return url.Parse(awsSpec.proxyURL)
				},
			},
		}
	}
	stsClient := sts.NewFromConfig(stsCfg)
	credentialsCache := aws.NewCredentialsCache(stscreds.NewWebIdentityRoleProvider(
		stsClient,
		awsSpec.roleArn,
		IdentityTokenValue(accessToken),
	))
	credentials, err := credentialsCache.Retrieve(context.TODO())
	awsSpec.credentials = credentials
	return err
}

func (a *AWSCredentialExchange) updateSecret(cacheKey string) error {
	namespaceName := types.NamespacedName{
		Namespace: a.awsSpecs[cacheKey].namespace,
		Name:      fmt.Sprintf("%s%s", OidcAwsPrefix, cacheKey),
	}
	credentialSecret := corev1.Secret{}
	err := a.k8sClient.Get(context.TODO(), namespaceName, &credentialSecret)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("fail to get secret for backend security policy %w", err)
		}
		err = a.k8sClient.Create(context.Background(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      namespaceName.Name,
				Namespace: namespaceName.Namespace,
			},
		})
		if err != nil {
			return err
		}
	}
	if credentialSecret.StringData == nil {
		credentialSecret.StringData = make(map[string]string)
	}
	credentialSecret.StringData["credentials"] = fmt.Sprintf("[default]\n"+
		"aws_access_key_id = %s\n"+
		"aws_secret_access_key = %s\n"+
		"aws_session_token = %s\n",
		a.awsSpecs[cacheKey].credentials.AccessKeyID, a.awsSpecs[cacheKey].credentials.SecretAccessKey, a.awsSpecs[cacheKey].credentials.SessionToken)

	err = a.k8sClient.Update(context.TODO(), &credentialSecret)
	if err != nil {
		return fmt.Errorf("fail to refresh find secret for backend security policy %w", err)
	}
	return nil
}

func (a *AWSCredentialExchange) needsCredentialRefresh(cacheKey string) bool {
	return time.Now().After(a.awsSpecs[cacheKey].expiredTime.Add(timeBeforeExpired))
}
