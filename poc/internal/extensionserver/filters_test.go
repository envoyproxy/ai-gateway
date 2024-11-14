package extensionserver

import (
	"testing"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	"github.com/stretchr/testify/require"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit"
)

func TestBuildExtProcMetadataOptions(t *testing.T) {
	cases := []struct {
		name     string
		route    *aigv1a1.LLMRoute
		expected *extprocv3.MetadataOptions
	}{
		{
			name:  "empty",
			route: &aigv1a1.LLMRoute{},
			expected: &extprocv3.MetadataOptions{
				ForwardingNamespaces: &extprocv3.MetadataOptions_MetadataNamespaces{
					Untyped: []string{ratelimit.LLMRateLimitMetadataNamespace},
				},
				ReceivingNamespaces: &extprocv3.MetadataOptions_MetadataNamespaces{
					Untyped: []string{ratelimit.LLMRateLimitMetadataNamespace},
				},
			},
		},
		{
			name: "standard",
			route: &aigv1a1.LLMRoute{
				Spec: aigv1a1.LLMRouteSpec{
					Backends: []aigv1a1.LLMBackend{
						{
							TrafficPolicy: &aigv1a1.LLMTrafficPolicy{
								RateLimit: &aigv1a1.LLMTrafficPolicyRateLimit{
									Rules: []aigv1a1.LLMTrafficPolicyRateLimitRule{
										{
											Metadata: []aigv1a1.LLMPolicyRateLimitMetadataMatch{
												{
													Name: "ns1",
												},
											},
										},
									},
								},
							},
						},
						{
							TrafficPolicy: &aigv1a1.LLMTrafficPolicy{
								RateLimit: &aigv1a1.LLMTrafficPolicyRateLimit{
									Rules: []aigv1a1.LLMTrafficPolicyRateLimitRule{
										{
											Metadata: []aigv1a1.LLMPolicyRateLimitMetadataMatch{
												{
													Name: "ns2",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: &extprocv3.MetadataOptions{
				ForwardingNamespaces: &extprocv3.MetadataOptions_MetadataNamespaces{
					Untyped: []string{ratelimit.LLMRateLimitMetadataNamespace, "ns1", "ns2"},
				},
				ReceivingNamespaces: &extprocv3.MetadataOptions_MetadataNamespaces{
					Untyped: []string{ratelimit.LLMRateLimitMetadataNamespace},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildExtProcMetadataOptions(tc.route)
			require.Equal(t, tc.expected, got)
		})
	}
}
