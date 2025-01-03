//go:build !ignore_autogenerated

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/gateway-api/apis/v1"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AWSCredentials) DeepCopyInto(out *AWSCredentials) {
	*out = *in
	if in.CredentialsFile != nil {
		in, out := &in.CredentialsFile, &out.CredentialsFile
		*out = new(AWSCredentialsFile)
		**out = **in
	}
	if in.OIDCFederation != nil {
		in, out := &in.OIDCFederation, &out.OIDCFederation
		*out = new(AWSOIDCFederation)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AWSCredentials.
func (in *AWSCredentials) DeepCopy() *AWSCredentials {
	if in == nil {
		return nil
	}
	out := new(AWSCredentials)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AWSCredentialsFile) DeepCopyInto(out *AWSCredentialsFile) {
	*out = *in
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
func (in *AWSOIDCFederation) DeepCopyInto(out *AWSOIDCFederation) {
	*out = *in
	in.OIDC.DeepCopyInto(&out.OIDC)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AWSOIDCFederation.
func (in *AWSOIDCFederation) DeepCopy() *AWSOIDCFederation {
	if in == nil {
		return nil
	}
	out := new(AWSOIDCFederation)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AuthenticationAPIKey) DeepCopyInto(out *AuthenticationAPIKey) {
	*out = *in
	if in.SecretRef != nil {
		in, out := &in.SecretRef, &out.SecretRef
		*out = new(v1.SecretObjectReference)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AuthenticationAPIKey.
func (in *AuthenticationAPIKey) DeepCopy() *AuthenticationAPIKey {
	if in == nil {
		return nil
	}
	out := new(AuthenticationAPIKey)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AuthenticationCloudProviderCredentials) DeepCopyInto(out *AuthenticationCloudProviderCredentials) {
	*out = *in
	in.AWSCredentials.DeepCopyInto(&out.AWSCredentials)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AuthenticationCloudProviderCredentials.
func (in *AuthenticationCloudProviderCredentials) DeepCopy() *AuthenticationCloudProviderCredentials {
	if in == nil {
		return nil
	}
	out := new(AuthenticationCloudProviderCredentials)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BackendSecurityPolicy) DeepCopyInto(out *BackendSecurityPolicy) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
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
		*out = new(AuthenticationAPIKey)
		(*in).DeepCopyInto(*out)
	}
	if in.CloudProviderCredentials != nil {
		in, out := &in.CloudProviderCredentials, &out.CloudProviderCredentials
		*out = new(AuthenticationCloudProviderCredentials)
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
func (in *LLMAPISchema) DeepCopyInto(out *LLMAPISchema) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LLMAPISchema.
func (in *LLMAPISchema) DeepCopy() *LLMAPISchema {
	if in == nil {
		return nil
	}
	out := new(LLMAPISchema)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LLMBackend) DeepCopyInto(out *LLMBackend) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LLMBackend.
func (in *LLMBackend) DeepCopy() *LLMBackend {
	if in == nil {
		return nil
	}
	out := new(LLMBackend)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *LLMBackend) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LLMBackendList) DeepCopyInto(out *LLMBackendList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]LLMBackend, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LLMBackendList.
func (in *LLMBackendList) DeepCopy() *LLMBackendList {
	if in == nil {
		return nil
	}
	out := new(LLMBackendList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *LLMBackendList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LLMBackendSpec) DeepCopyInto(out *LLMBackendSpec) {
	*out = *in
	out.APISchema = in.APISchema
	in.BackendRef.DeepCopyInto(&out.BackendRef)
	if in.BackendSecurityPolicyRef != nil {
		in, out := &in.BackendSecurityPolicyRef, &out.BackendSecurityPolicyRef
		*out = new(v1.LocalObjectReference)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LLMBackendSpec.
func (in *LLMBackendSpec) DeepCopy() *LLMBackendSpec {
	if in == nil {
		return nil
	}
	out := new(LLMBackendSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LLMRoute) DeepCopyInto(out *LLMRoute) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LLMRoute.
func (in *LLMRoute) DeepCopy() *LLMRoute {
	if in == nil {
		return nil
	}
	out := new(LLMRoute)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *LLMRoute) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LLMRouteList) DeepCopyInto(out *LLMRouteList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]LLMRoute, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LLMRouteList.
func (in *LLMRouteList) DeepCopy() *LLMRouteList {
	if in == nil {
		return nil
	}
	out := new(LLMRouteList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *LLMRouteList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LLMRouteSpec) DeepCopyInto(out *LLMRouteSpec) {
	*out = *in
	out.APISchema = in.APISchema
	in.HTTPRoute.DeepCopyInto(&out.HTTPRoute)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LLMRouteSpec.
func (in *LLMRouteSpec) DeepCopy() *LLMRouteSpec {
	if in == nil {
		return nil
	}
	out := new(LLMRouteSpec)
	in.DeepCopyInto(out)
	return out
}
