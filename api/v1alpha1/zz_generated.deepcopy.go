// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build !ignore_autogenerated

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIGatewayFilterConfig) DeepCopyInto(out *AIGatewayFilterConfig) {
	*out = *in
	if in.ExternalProcessor != nil {
		in, out := &in.ExternalProcessor, &out.ExternalProcessor
		*out = new(AIGatewayFilterConfigExternalProcessor)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIGatewayFilterConfig.
func (in *AIGatewayFilterConfig) DeepCopy() *AIGatewayFilterConfig {
	if in == nil {
		return nil
	}
	out := new(AIGatewayFilterConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIGatewayFilterConfigExternalProcessor) DeepCopyInto(out *AIGatewayFilterConfigExternalProcessor) {
	*out = *in
	if in.Replicas != nil {
		in, out := &in.Replicas, &out.Replicas
		*out = new(int32)
		**out = **in
	}
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = new(corev1.ResourceRequirements)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIGatewayFilterConfigExternalProcessor.
func (in *AIGatewayFilterConfigExternalProcessor) DeepCopy() *AIGatewayFilterConfigExternalProcessor {
	if in == nil {
		return nil
	}
	out := new(AIGatewayFilterConfigExternalProcessor)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIGatewayRoute) DeepCopyInto(out *AIGatewayRoute) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIGatewayRoute.
func (in *AIGatewayRoute) DeepCopy() *AIGatewayRoute {
	if in == nil {
		return nil
	}
	out := new(AIGatewayRoute)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *AIGatewayRoute) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIGatewayRouteList) DeepCopyInto(out *AIGatewayRouteList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]AIGatewayRoute, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIGatewayRouteList.
func (in *AIGatewayRouteList) DeepCopy() *AIGatewayRouteList {
	if in == nil {
		return nil
	}
	out := new(AIGatewayRouteList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *AIGatewayRouteList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIGatewayRouteRule) DeepCopyInto(out *AIGatewayRouteRule) {
	*out = *in
	if in.BackendRefs != nil {
		in, out := &in.BackendRefs, &out.BackendRefs
		*out = make([]AIGatewayRouteRuleBackendRef, len(*in))
		copy(*out, *in)
	}
	if in.Matches != nil {
		in, out := &in.Matches, &out.Matches
		*out = make([]AIGatewayRouteRuleMatch, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIGatewayRouteRule.
func (in *AIGatewayRouteRule) DeepCopy() *AIGatewayRouteRule {
	if in == nil {
		return nil
	}
	out := new(AIGatewayRouteRule)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIGatewayRouteRuleBackendRef) DeepCopyInto(out *AIGatewayRouteRuleBackendRef) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIGatewayRouteRuleBackendRef.
func (in *AIGatewayRouteRuleBackendRef) DeepCopy() *AIGatewayRouteRuleBackendRef {
	if in == nil {
		return nil
	}
	out := new(AIGatewayRouteRuleBackendRef)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIGatewayRouteRuleMatch) DeepCopyInto(out *AIGatewayRouteRuleMatch) {
	*out = *in
	if in.Headers != nil {
		in, out := &in.Headers, &out.Headers
		*out = make([]v1.HTTPHeaderMatch, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIGatewayRouteRuleMatch.
func (in *AIGatewayRouteRuleMatch) DeepCopy() *AIGatewayRouteRuleMatch {
	if in == nil {
		return nil
	}
	out := new(AIGatewayRouteRuleMatch)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIGatewayRouteSpec) DeepCopyInto(out *AIGatewayRouteSpec) {
	*out = *in
	if in.TargetRefs != nil {
		in, out := &in.TargetRefs, &out.TargetRefs
		*out = make([]v1alpha2.LocalPolicyTargetReferenceWithSectionName, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	out.APISchema = in.APISchema
	if in.Rules != nil {
		in, out := &in.Rules, &out.Rules
		*out = make([]AIGatewayRouteRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.FilterConfig != nil {
		in, out := &in.FilterConfig, &out.FilterConfig
		*out = new(AIGatewayFilterConfig)
		(*in).DeepCopyInto(*out)
	}
	if in.LLMRequestCosts != nil {
		in, out := &in.LLMRequestCosts, &out.LLMRequestCosts
		*out = make([]LLMRequestCost, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIGatewayRouteSpec.
func (in *AIGatewayRouteSpec) DeepCopy() *AIGatewayRouteSpec {
	if in == nil {
		return nil
	}
	out := new(AIGatewayRouteSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIGatewayRouteStatus) DeepCopyInto(out *AIGatewayRouteStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIGatewayRouteStatus.
func (in *AIGatewayRouteStatus) DeepCopy() *AIGatewayRouteStatus {
	if in == nil {
		return nil
	}
	out := new(AIGatewayRouteStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIServiceBackend) DeepCopyInto(out *AIServiceBackend) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIServiceBackend.
func (in *AIServiceBackend) DeepCopy() *AIServiceBackend {
	if in == nil {
		return nil
	}
	out := new(AIServiceBackend)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *AIServiceBackend) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIServiceBackendList) DeepCopyInto(out *AIServiceBackendList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]AIServiceBackend, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIServiceBackendList.
func (in *AIServiceBackendList) DeepCopy() *AIServiceBackendList {
	if in == nil {
		return nil
	}
	out := new(AIServiceBackendList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *AIServiceBackendList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIServiceBackendSpec) DeepCopyInto(out *AIServiceBackendSpec) {
	*out = *in
	out.APISchema = in.APISchema
	in.BackendRef.DeepCopyInto(&out.BackendRef)
	if in.BackendSecurityPolicyRef != nil {
		in, out := &in.BackendSecurityPolicyRef, &out.BackendSecurityPolicyRef
		*out = new(v1.LocalObjectReference)
		**out = **in
	}
	if in.Timeouts != nil {
		in, out := &in.Timeouts, &out.Timeouts
		*out = new(v1.HTTPRouteTimeouts)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIServiceBackendSpec.
func (in *AIServiceBackendSpec) DeepCopy() *AIServiceBackendSpec {
	if in == nil {
		return nil
	}
	out := new(AIServiceBackendSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AIServiceBackendStatus) DeepCopyInto(out *AIServiceBackendStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AIServiceBackendStatus.
func (in *AIServiceBackendStatus) DeepCopy() *AIServiceBackendStatus {
	if in == nil {
		return nil
	}
	out := new(AIServiceBackendStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AWSCredentialsFile) DeepCopyInto(out *AWSCredentialsFile) {
	*out = *in
	if in.SecretRef != nil {
		in, out := &in.SecretRef, &out.SecretRef
		*out = new(v1.SecretObjectReference)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AWSCredentialsFile.
func (in *AWSCredentialsFile) DeepCopy() *AWSCredentialsFile {
	if in == nil {
		return nil
	}
	out := new(AWSCredentialsFile)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AWSOIDCExchangeToken) DeepCopyInto(out *AWSOIDCExchangeToken) {
	*out = *in
	in.OIDC.DeepCopyInto(&out.OIDC)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AWSOIDCExchangeToken.
func (in *AWSOIDCExchangeToken) DeepCopy() *AWSOIDCExchangeToken {
	if in == nil {
		return nil
	}
	out := new(AWSOIDCExchangeToken)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BackendSecurityPolicy) DeepCopyInto(out *BackendSecurityPolicy) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BackendSecurityPolicy.
func (in *BackendSecurityPolicy) DeepCopy() *BackendSecurityPolicy {
	if in == nil {
		return nil
	}
	out := new(BackendSecurityPolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *BackendSecurityPolicy) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BackendSecurityPolicyAPIKey) DeepCopyInto(out *BackendSecurityPolicyAPIKey) {
	*out = *in
	if in.SecretRef != nil {
		in, out := &in.SecretRef, &out.SecretRef
		*out = new(v1.SecretObjectReference)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BackendSecurityPolicyAPIKey.
func (in *BackendSecurityPolicyAPIKey) DeepCopy() *BackendSecurityPolicyAPIKey {
	if in == nil {
		return nil
	}
	out := new(BackendSecurityPolicyAPIKey)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BackendSecurityPolicyAWSCredentials) DeepCopyInto(out *BackendSecurityPolicyAWSCredentials) {
	*out = *in
	if in.CredentialsFile != nil {
		in, out := &in.CredentialsFile, &out.CredentialsFile
		*out = new(AWSCredentialsFile)
		(*in).DeepCopyInto(*out)
	}
	if in.OIDCExchangeToken != nil {
		in, out := &in.OIDCExchangeToken, &out.OIDCExchangeToken
		*out = new(AWSOIDCExchangeToken)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BackendSecurityPolicyAWSCredentials.
func (in *BackendSecurityPolicyAWSCredentials) DeepCopy() *BackendSecurityPolicyAWSCredentials {
	if in == nil {
		return nil
	}
	out := new(BackendSecurityPolicyAWSCredentials)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BackendSecurityPolicyAzureCredentials) DeepCopyInto(out *BackendSecurityPolicyAzureCredentials) {
	*out = *in
	if in.ClientSecretRef != nil {
		in, out := &in.ClientSecretRef, &out.ClientSecretRef
		*out = new(apisv1.SecretObjectReference)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BackendSecurityPolicyAzureCredentials.
func (in *BackendSecurityPolicyAzureCredentials) DeepCopy() *BackendSecurityPolicyAzureCredentials {
	if in == nil {
		return nil
	}
	out := new(BackendSecurityPolicyAzureCredentials)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BackendSecurityPolicyList) DeepCopyInto(out *BackendSecurityPolicyList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]BackendSecurityPolicy, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BackendSecurityPolicyList.
func (in *BackendSecurityPolicyList) DeepCopy() *BackendSecurityPolicyList {
	if in == nil {
		return nil
	}
	out := new(BackendSecurityPolicyList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *BackendSecurityPolicyList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BackendSecurityPolicySpec) DeepCopyInto(out *BackendSecurityPolicySpec) {
	*out = *in
	if in.APIKey != nil {
		in, out := &in.APIKey, &out.APIKey
		*out = new(BackendSecurityPolicyAPIKey)
		(*in).DeepCopyInto(*out)
	}
	if in.AWSCredentials != nil {
		in, out := &in.AWSCredentials, &out.AWSCredentials
		*out = new(BackendSecurityPolicyAWSCredentials)
		(*in).DeepCopyInto(*out)
	}
	if in.AzureCredentials != nil {
		in, out := &in.AzureCredentials, &out.AzureCredentials
		*out = new(BackendSecurityPolicyAzureCredentials)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BackendSecurityPolicySpec.
func (in *BackendSecurityPolicySpec) DeepCopy() *BackendSecurityPolicySpec {
	if in == nil {
		return nil
	}
	out := new(BackendSecurityPolicySpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BackendSecurityPolicyStatus) DeepCopyInto(out *BackendSecurityPolicyStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BackendSecurityPolicyStatus.
func (in *BackendSecurityPolicyStatus) DeepCopy() *BackendSecurityPolicyStatus {
	if in == nil {
		return nil
	}
	out := new(BackendSecurityPolicyStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LLMRequestCost) DeepCopyInto(out *LLMRequestCost) {
	*out = *in
	if in.CEL != nil {
		in, out := &in.CEL, &out.CEL
		*out = new(string)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LLMRequestCost.
func (in *LLMRequestCost) DeepCopy() *LLMRequestCost {
	if in == nil {
		return nil
	}
	out := new(LLMRequestCost)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *VersionedAPISchema) DeepCopyInto(out *VersionedAPISchema) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new VersionedAPISchema.
func (in *VersionedAPISchema) DeepCopy() *VersionedAPISchema {
	if in == nil {
		return nil
	}
	out := new(VersionedAPISchema)
	in.DeepCopyInto(out)
	return out
}
