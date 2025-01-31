package oidc

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const OidcAwsPrefix = "oidc-aws-"

func getSTSCredentials(region, roleArn, proxyURL, accessToken string) (aws.Credentials, error) {
	// create sts client
	stsCfg := aws.Config{
		Region: region,
	}
	if proxyURL != "" {
		stsCfg.HTTPClient = &http.Client{
			Transport: &http.Transport{
				Proxy: func(*http.Request) (*url.URL, error) {
					return url.Parse(proxyURL)
				},
			},
		}
	}
	stsClient := sts.NewFromConfig(stsCfg)
	credentialsCache := aws.NewCredentialsCache(stscreds.NewWebIdentityRoleProvider(
		stsClient,
		roleArn,
		IdentityTokenValue(accessToken),
	))
	return credentialsCache.Retrieve(context.TODO())
}

func updateAWSSecret(k8sClient client.Client, credentials aws.Credentials, namespace, bspKey string) error {
	namespaceName := types.NamespacedName{
		Namespace: namespace,
		Name:      fmt.Sprintf("%s%s", OidcAwsPrefix, bspKey),
	}
	credentialSecret := corev1.Secret{}
	err := k8sClient.Get(context.TODO(), namespaceName, &credentialSecret)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("fail to get secret for backend security policy %w", err)
		}
		err = k8sClient.Create(context.Background(), &corev1.Secret{
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
		credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken)

	err = k8sClient.Update(context.TODO(), &credentialSecret)
	if err != nil {
		return fmt.Errorf("fail to refresh find secret for backend security policy %w", err)
	}
	return nil
}
