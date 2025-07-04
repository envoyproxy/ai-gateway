// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1alpha1

import (
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// BackendSecurityPolicyType specifies the type of auth mechanism used to access a backend.
type BackendSecurityPolicyType string

const (
	BackendSecurityPolicyTypeAPIKey           BackendSecurityPolicyType = "APIKey"
	BackendSecurityPolicyTypeAWSCredentials   BackendSecurityPolicyType = "AWSCredentials"
	BackendSecurityPolicyTypeAzureCredentials BackendSecurityPolicyType = "AzureCredentials"
	BackendSecurityPolicyTypeGCPCredentials   BackendSecurityPolicyType = "GCPCredentials"
)

// BackendSecurityPolicy specifies configuration for authentication and authorization rules on the traffic
// exiting the gateway to the backend.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[-1:].type`
type BackendSecurityPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackendSecurityPolicySpec `json:"spec,omitempty"`
	// Status defines the status details of the BackendSecurityPolicy.
	Status BackendSecurityPolicyStatus `json:"status,omitempty"`
}

// BackendSecurityPolicySpec specifies authentication rules on access the provider from the Gateway.
// Only one mechanism to access a backend(s) can be specified.
//
// Only one type of BackendSecurityPolicy can be defined.
// +kubebuilder:validation:MaxProperties=2
// +kubebuilder:validation:XValidation:rule="self.type == 'APIKey' ? (has(self.apiKey) && !has(self.awsCredentials) && !has(self.azureCredentials) && !has(self.gcpCredentials)) : true",message="When type is APIKey, only apiKey field should be set"
// +kubebuilder:validation:XValidation:rule="self.type == 'AWSCredentials' ? (has(self.awsCredentials) && !has(self.apiKey) && !has(self.azureCredentials) && !has(self.gcpCredentials)) : true",message="When type is AWSCredentials, only awsCredentials field should be set"
// +kubebuilder:validation:XValidation:rule="self.type == 'AzureCredentials' ? (has(self.azureCredentials) && !has(self.apiKey) && !has(self.awsCredentials) && !has(self.gcpCredentials)) : true",message="When type is AzureCredentials, only azureCredentials field should be set"
// +kubebuilder:validation:XValidation:rule="self.type == 'GCPCredentials' ? (has(self.gcpCredentials) && !has(self.apiKey) && !has(self.awsCredentials) && !has(self.azureCredentials)) : true",message="When type is GCPCredentials, only gcpCredentials field should be set"
type BackendSecurityPolicySpec struct {
	// Type specifies the type of the backend security policy.
	//
	// +kubebuilder:validation:Enum=APIKey;AWSCredentials;AzureCredentials;GCPCredentials
	Type BackendSecurityPolicyType `json:"type"`

	// APIKey is a mechanism to access a backend(s). The API key will be injected into the Authorization header.
	//
	// +optional
	APIKey *BackendSecurityPolicyAPIKey `json:"apiKey,omitempty"`

	// AWSCredentials is a mechanism to access a backend(s). AWS specific logic will be applied.
	//
	// +optional
	AWSCredentials *BackendSecurityPolicyAWSCredentials `json:"awsCredentials,omitempty"`

	// AzureCredentials is a mechanism to access a backend(s). Azure OpenAI specific logic will be applied.
	//
	// +optional
	AzureCredentials *BackendSecurityPolicyAzureCredentials `json:"azureCredentials,omitempty"`
	// GCPCredentials is a mechanism to access a backend(s). GCP specific logic will be applied.
	//
	// +optional
	GCPCredentials *BackendSecurityPolicyGCPCredentials `json:"gcpCredentials,omitempty"`
}

// BackendSecurityPolicyList contains a list of BackendSecurityPolicy
//
// +kubebuilder:object:root=true
type BackendSecurityPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackendSecurityPolicy `json:"items"`
}

// BackendSecurityPolicyAPIKey specifies the API key.
type BackendSecurityPolicyAPIKey struct {
	// SecretRef is the reference to the secret containing the API key.
	// ai-gateway must be given the permission to read this secret.
	// The key of the secret should be "apiKey".
	SecretRef *gwapiv1.SecretObjectReference `json:"secretRef"`
}

// BackendSecurityPolicyOIDC specifies OIDC related fields.
type BackendSecurityPolicyOIDC struct {
	// OIDC is used to obtain oidc tokens via an SSO server which will be used to exchange for provider credentials.
	//
	// +kubebuilder:validation:Required
	OIDC egv1a1.OIDC `json:"oidc"`

	// GrantType is the method application gets access token.
	//
	// +optional
	GrantType string `json:"grantType,omitempty"`

	// Aud defines the audience that this ID Token is intended for.
	//
	// +optional
	Aud string `json:"aud,omitempty"`
}

type GCPWorkLoadIdentityFederationConfig struct {
	// ProjectID is the GCP project ID.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ProjectID string `json:"projectID"`

	// WorkloadIdentityProvider is the external auth provider to be used to authenticate against GCP.
	// https://cloud.google.com/iam/docs/workload-identity-federation?hl=en
	// Currently only OIDC is supported.
	//
	// +kubebuilder:validation:Required
	WorkloadIdentityProvider GCPWorkloadIdentityProvider `json:"workloadIdentityProvider"`

	// WorkloadIdentityPoolName is the name of the workload identity pool defined in GCP.
	// https://cloud.google.com/iam/docs/workload-identity-federation?hl=en
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	WorkloadIdentityPoolName string `json:"workloadIdentityPoolName"`

	// ServiceAccountImpersonation is the service account impersonation configuration.
	// This is used to impersonate a service account when getting access token.
	//
	// +optional
	ServiceAccountImpersonation *GCPServiceAccountImpersonationConfig `json:"serviceAccountImpersonation,omitempty"`
}

// GCPWorkloadIdentityProvider specifies the external identity provider to be used to authenticate against GCP.
// The external identity provider can be AWS, Microsoft, etc but must be pre-registered in the GCP project
//
// https://cloud.google.com/iam/docs/workload-identity-federation
type GCPWorkloadIdentityProvider struct {
	// Name of the external identity provider as registered on Google Cloud Platform.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// OIDCProvider is the generic OIDCProvider fields.
	//
	// +kubebuilder:validation:Required
	OIDCProvider BackendSecurityPolicyOIDC `json:"OIDCProvider"`
}

type GCPServiceAccountImpersonationConfig struct {
	// ServiceAccountName is the name of the service account to impersonate.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ServiceAccountName string `json:"serviceAccountName"`
	// ServiceAccountProjectName is the project name in which the service account is registered.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ServiceAccountProjectName string `json:"serviceAccountProjectName"`
}

// BackendSecurityPolicyGCPCredentials contains the supported authentication mechanisms to access GCP.
type BackendSecurityPolicyGCPCredentials struct {
	// ProjectName is the GCP project name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ProjectName string `json:"projectName"`
	// Region is the GCP region associated with the policy.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`
	// WorkLoadIdentityFederationConfig is the configuration for the GCP Workload Identity Federation.
	//
	// +kubebuilder:validation:Required
	WorkLoadIdentityFederationConfig GCPWorkLoadIdentityFederationConfig `json:"workLoadIdentityFederationConfig"`
}

// BackendSecurityPolicyAzureCredentials contains the supported authentication mechanisms to access Azure.
// Only one of ClientSecretRef or OIDCExchangeToken must be specified. Credentials will not be generated if
// neither are set.
//
// +kubebuilder:validation:XValidation:rule="(has(self.clientSecretRef) && !has(self.oidcExchangeToken)) || (!has(self.clientSecretRef) && has(self.oidcExchangeToken))",message="Exactly one of clientSecretRef or oidcExchangeToken must be specified"
type BackendSecurityPolicyAzureCredentials struct {
	// ClientID is a unique identifier for an application in Azure.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ClientID string `json:"clientID"`

	// TenantId is a unique identifier for an Azure Active Directory instance.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	TenantID string `json:"tenantID"`

	// ClientSecretRef is the reference to the secret containing the Azure client secret.
	// ai-gateway must be given the permission to read this secret.
	// The key of secret should be "client-secret".
	//
	// +optional
	ClientSecretRef *gwapiv1.SecretObjectReference `json:"clientSecretRef,omitempty"`

	// OIDCExchangeToken specifies the oidc configurations used to obtain an oidc token. The oidc token will be
	// used to obtain temporary credentials to access Azure.
	//
	// +optional
	OIDCExchangeToken *AzureOIDCExchangeToken `json:"oidcExchangeToken,omitempty"`
}

// AzureOIDCExchangeToken specifies credentials to obtain oidc token from a sso server.
// For Azure, the controller will query Azure Entra ID to get an Azure Access Token,
// and store them in a secret.
type AzureOIDCExchangeToken struct {
	// BackendSecurityPolicyOIDC is the generic OIDC fields.
	BackendSecurityPolicyOIDC `json:",inline"`
}

// BackendSecurityPolicyAWSCredentials contains the supported authentication mechanisms to access aws.
type BackendSecurityPolicyAWSCredentials struct {
	// Region specifies the AWS region associated with the policy.
	//
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`

	// CredentialsFile specifies the credentials file to use for the AWS provider.
	//
	// +optional
	CredentialsFile *AWSCredentialsFile `json:"credentialsFile,omitempty"`

	// OIDCExchangeToken specifies the oidc configurations used to obtain an oidc token. The oidc token will be
	// used to obtain temporary credentials to access AWS.
	//
	// +optional
	OIDCExchangeToken *AWSOIDCExchangeToken `json:"oidcExchangeToken,omitempty"`
}

// AWSCredentialsFile specifies the credentials file to use for the AWS provider.
// Envoy reads the secret file, and the profile to use is specified by the Profile field.
type AWSCredentialsFile struct {
	// SecretRef is the reference to the credential file.
	//
	// The secret should contain the AWS credentials file keyed on "credentials".
	SecretRef *gwapiv1.SecretObjectReference `json:"secretRef"`

	// Profile is the profile to use in the credentials file.
	//
	// +kubebuilder:default=default
	Profile string `json:"profile,omitempty"`
}

// AWSOIDCExchangeToken specifies credentials to obtain oidc token from a sso server.
// For AWS, the controller will query STS to obtain AWS AccessKeyId, SecretAccessKey, and SessionToken,
// and store them in a temporary credentials file.
type AWSOIDCExchangeToken struct {
	// BackendSecurityPolicyOIDC is the generic OIDC fields.
	BackendSecurityPolicyOIDC `json:",inline"`

	// AwsRoleArn is the AWS IAM Role with the permission to use specific resources in AWS account
	// which maps to the temporary AWS security credentials exchanged using the authentication token issued by OIDC provider.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	AwsRoleArn string `json:"awsRoleArn"`
}
