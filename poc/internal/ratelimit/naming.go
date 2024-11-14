package ratelimit

import (
	"fmt"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
)

const (
	LLMRateLimitMetadataNamespace = "llm.ratelimit"
	DescriptorsKey                = "descriptors"
	ModelNameKey                  = "modelName"
	BackendNameDescriptorKey      = "LLM-Backend"
	LimitTypeDescriptorKey        = "LLM-RateLimit-Type"

	MetadataNotFoundVal = "unknown"
	HeaderMatchedVal    = "true"
)

func LimitKey(ruleIdx int, limitType aigv1a1.LLMPolicyRateLimitType, limitIdx int) string {
	return fmt.Sprintf("rule-%d-%s-%d", ruleIdx, limitType, limitIdx)
}

func HeaderMatchKey(t aigv1a1.LLMPolicyRateLimitStringMatchType, idx int) string {
	return fmt.Sprintf("header-%s-%d", t, idx)
}

func DynamicMetadataMatchKey(idx int) string {
	return fmt.Sprintf("dynamic-metadata-%d", idx)
}

// Domain return domain for RateLimit configuration.
// Right now, LLMRoute and Gateway should 1:1 mapping.
// So, we can use the LLMRoute namespaced name as Domain, it won't conflict.
//
// TODO: think about use different domains base on limit type(Request or Token)
func Domain(route *aigv1a1.LLMRoute) string {
	return fmt.Sprintf("%s/%s", route.Namespace, route.Name)
}
