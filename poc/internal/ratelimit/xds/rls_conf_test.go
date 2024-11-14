package xds

import (
	"fmt"
	"os"
	"testing"

	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit"
)

func TestBuildRateLimitDescriptor(t *testing.T) {
	cases := []struct {
		rl       *aigv1a1.LLMTrafficPolicyRateLimitRule
		limit    aigv1a1.LLMPolicyRateLimitValue
		excepted *rlsconfv3.RateLimitDescriptor
	}{
		{
			rl: &aigv1a1.LLMTrafficPolicyRateLimitRule{
				Headers: []aigv1a1.LLMPolicyRateLimitHeaderMatch{
					{
						Name: "fake-header1",
						Type: aigv1a1.HeaderMatchDistinct,
					},
				},
				Metadata: []aigv1a1.LLMPolicyRateLimitMetadataMatch{
					{
						Name: "fake-metadata1",
						Type: aigv1a1.MetadataTypeDynamic,
					},
				},
			},
			limit: aigv1a1.LLMPolicyRateLimitValue{
				Type:     aigv1a1.RateLimitTypeToken,
				Unit:     aigv1a1.RateLimitUnitMinute,
				Quantity: 100,
			},
			excepted: &rlsconfv3.RateLimitDescriptor{
				Key:   "LLM-RateLimit-Type",
				Value: "rule-0-Token-0",
				RateLimit: &rlsconfv3.RateLimitPolicy{
					Unit:            rlsconfv3.RateLimitUnit_MINUTE,
					RequestsPerUnit: 100,
				},
				Descriptors: []*rlsconfv3.RateLimitDescriptor{
					{
						Key: "header-Distinct-0",
						RateLimit: &rlsconfv3.RateLimitPolicy{
							Unit:            rlsconfv3.RateLimitUnit_MINUTE,
							RequestsPerUnit: 100,
						},
						Descriptors: []*rlsconfv3.RateLimitDescriptor{
							{
								Key: "dynamic-metadata-0",
								RateLimit: &rlsconfv3.RateLimitPolicy{
									Unit:            rlsconfv3.RateLimitUnit_MINUTE,
									RequestsPerUnit: 100,
								},
							},
						},
					},
				},
			},
		},
		{
			rl: &aigv1a1.LLMTrafficPolicyRateLimitRule{
				Headers: []aigv1a1.LLMPolicyRateLimitHeaderMatch{
					{
						Name:  "fake-header1",
						Type:  aigv1a1.HeaderMatchExact,
						Value: ptr.To("fake-value1"),
					},
				},
				Metadata: []aigv1a1.LLMPolicyRateLimitMetadataMatch{
					{
						Name: "fake-metadata1",
						Type: aigv1a1.MetadataTypeDynamic,
					},
				},
			},
			limit: aigv1a1.LLMPolicyRateLimitValue{
				Type:     aigv1a1.RateLimitTypeToken,
				Unit:     aigv1a1.RateLimitUnitMinute,
				Quantity: 100,
			},
			excepted: &rlsconfv3.RateLimitDescriptor{
				Key:   "LLM-RateLimit-Type",
				Value: "rule-0-Token-0",
				RateLimit: &rlsconfv3.RateLimitPolicy{
					Unit:            rlsconfv3.RateLimitUnit_MINUTE,
					RequestsPerUnit: 100,
				},
				Descriptors: []*rlsconfv3.RateLimitDescriptor{
					{
						Key:   "header-Exact-0",
						Value: "true",
						RateLimit: &rlsconfv3.RateLimitPolicy{
							Unit:            rlsconfv3.RateLimitUnit_MINUTE,
							RequestsPerUnit: 100,
						},
						Descriptors: []*rlsconfv3.RateLimitDescriptor{
							{
								Key: "dynamic-metadata-0",
								RateLimit: &rlsconfv3.RateLimitPolicy{
									Unit:            rlsconfv3.RateLimitUnit_MINUTE,
									RequestsPerUnit: 100,
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			got := buildRateLimitDescriptor(0, tc.rl, 0, tc.limit)
			require.Equal(t, tc.excepted, got)
		})
	}
}

func TestBuildRateLimitConfigDescriptors(t *testing.T) {
	cases := []struct {
		name     string
		llmRoute aigv1a1.LLMRoute
		excepted []*rlsconfv3.RateLimitDescriptor
	}{
		{
			name: "quickstart",
			excepted: []*rlsconfv3.RateLimitDescriptor{
				{
					Key:   ratelimit.BackendNameDescriptorKey,
					Value: "backend-ratelimit",
					Descriptors: []*rlsconfv3.RateLimitDescriptor{
						{
							Key:   "LLM-RateLimit-Type",
							Value: "rule-0-Token-0",
							RateLimit: &rlsconfv3.RateLimitPolicy{
								Unit:            rlsconfv3.RateLimitUnit_HOUR,
								RequestsPerUnit: 10,
							},
							Descriptors: []*rlsconfv3.RateLimitDescriptor{
								{
									Key:   "header-Exact-0",
									Value: "true",
									RateLimit: &rlsconfv3.RateLimitPolicy{
										Unit:            rlsconfv3.RateLimitUnit_HOUR,
										RequestsPerUnit: 10,
									},
									Descriptors: []*rlsconfv3.RateLimitDescriptor{
										{
											Key: "header-Distinct-1",
											RateLimit: &rlsconfv3.RateLimitPolicy{
												Unit:            rlsconfv3.RateLimitUnit_HOUR,
												RequestsPerUnit: 10,
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
		{
			name: "limits",
			excepted: []*rlsconfv3.RateLimitDescriptor{
				{
					Key:   ratelimit.BackendNameDescriptorKey,
					Value: "backend-ratelimit",
					Descriptors: []*rlsconfv3.RateLimitDescriptor{
						{
							Key:   "LLM-RateLimit-Type",
							Value: "rule-0-Request-0",
							RateLimit: &rlsconfv3.RateLimitPolicy{
								Unit:            rlsconfv3.RateLimitUnit_MINUTE,
								RequestsPerUnit: 10,
							},
							Descriptors: []*rlsconfv3.RateLimitDescriptor{
								{
									Key:   "header-Exact-0",
									Value: "true",
									RateLimit: &rlsconfv3.RateLimitPolicy{
										Unit:            rlsconfv3.RateLimitUnit_MINUTE,
										RequestsPerUnit: 10,
									},
									Descriptors: []*rlsconfv3.RateLimitDescriptor{
										{
											Key: "header-Distinct-1",
											RateLimit: &rlsconfv3.RateLimitPolicy{
												Unit:            rlsconfv3.RateLimitUnit_MINUTE,
												RequestsPerUnit: 10,
											},
										},
									},
								},
							},
						},
						{
							Key:   "LLM-RateLimit-Type",
							Value: "rule-0-Token-1",
							RateLimit: &rlsconfv3.RateLimitPolicy{
								Unit:            rlsconfv3.RateLimitUnit_MINUTE,
								RequestsPerUnit: 500,
							},
							Descriptors: []*rlsconfv3.RateLimitDescriptor{
								{
									Key:   "header-Exact-0",
									Value: "true",
									RateLimit: &rlsconfv3.RateLimitPolicy{
										Unit:            rlsconfv3.RateLimitUnit_MINUTE,
										RequestsPerUnit: 500,
									},
									Descriptors: []*rlsconfv3.RateLimitDescriptor{
										{
											Key: "header-Distinct-1",
											RateLimit: &rlsconfv3.RateLimitPolicy{
												Unit:            rlsconfv3.RateLimitUnit_MINUTE,
												RequestsPerUnit: 500,
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
		{
			name: "blockUnknown",
			excepted: []*rlsconfv3.RateLimitDescriptor{
				{
					Key:   ratelimit.BackendNameDescriptorKey,
					Value: "backend-ratelimit",
					Descriptors: []*rlsconfv3.RateLimitDescriptor{
						{
							Key:   "LLM-RateLimit-Type",
							Value: "rule-0-Token-0",
							RateLimit: &rlsconfv3.RateLimitPolicy{
								Unit:            rlsconfv3.RateLimitUnit_HOUR,
								RequestsPerUnit: 10,
							},
							Descriptors: []*rlsconfv3.RateLimitDescriptor{
								{
									Key:   "header-Exact-0",
									Value: "true",
									RateLimit: &rlsconfv3.RateLimitPolicy{
										Unit:            rlsconfv3.RateLimitUnit_HOUR,
										RequestsPerUnit: 10,
									},
									Descriptors: []*rlsconfv3.RateLimitDescriptor{
										{
											Key: "header-Distinct-1",
											RateLimit: &rlsconfv3.RateLimitPolicy{
												Unit:            rlsconfv3.RateLimitUnit_HOUR,
												RequestsPerUnit: 10,
											},
										},
									},
								},
							},
						},
						{
							Key:   "LLM-RateLimit-Type",
							Value: "rule-1-Token-0",
							RateLimit: &rlsconfv3.RateLimitPolicy{
								Unit:            rlsconfv3.RateLimitUnit_HOUR,
								RequestsPerUnit: 0,
							},
							Descriptors: []*rlsconfv3.RateLimitDescriptor{
								{
									Key:   "header-Exact-0",
									Value: "true",
									RateLimit: &rlsconfv3.RateLimitPolicy{
										Unit:            rlsconfv3.RateLimitUnit_HOUR,
										RequestsPerUnit: 0,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(fmt.Sprintf("testdata/%s.yaml", tc.name))
			require.NoError(t, err)
			route := &aigv1a1.LLMRoute{}
			require.NoError(t, yaml.Unmarshal(data, route))
			got := buildRateLimitConfigDescriptors(route)
			require.Equal(t, tc.excepted, got)
		})
	}
}
