package xds

import (
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	rlcommonv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	rlsv3 "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
	"k8s.io/utils/ptr"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit"
)

func TestRatelimitDynamicMetadata(t *testing.T) {
	cases := []struct {
		name       string
		md         *corev3.Metadata
		reqHeaders map[string]string
		rl         *aigv1a1.LLMTrafficPolicyRateLimit

		expectedMetadata *structpb.Struct
		expectedEntities []*rlcommonv3.RateLimitDescriptor_Entry
		expectedRequest  *rlsv3.RateLimitRequest
	}{
		{
			name: "single rule",
			md: &corev3.Metadata{
				FilterMetadata: map[string]*structpb.Struct{
					"test1": {
						Fields: map[string]*structpb.Value{
							"key1": {
								Kind: &structpb.Value_StringValue{
									StringValue: "value1",
								},
							},
						},
					},
				},
			},
			reqHeaders: map[string]string{
				"header1": "value1",
				"header2": "value2",
			},
			rl: &aigv1a1.LLMTrafficPolicyRateLimit{
				Rules: []aigv1a1.LLMTrafficPolicyRateLimitRule{
					{
						Headers: []aigv1a1.LLMPolicyRateLimitHeaderMatch{
							{
								Type:  aigv1a1.HeaderMatchExact,
								Value: ptr.To("value1"),
								Name:  "header1",
							},
						},
						Metadata: []aigv1a1.LLMPolicyRateLimitMetadataMatch{
							{
								Name:  "test1",
								Paths: []string{"key1"},
							},
						},
						Limits: []aigv1a1.LLMPolicyRateLimitValue{
							{
								Type: aigv1a1.RateLimitTypeToken,
							},
						},
					},
				},
			},
			expectedMetadata: &structpb.Struct{
				Fields: map[string]*structpb.Value{
					"llm.ratelimit": {
						Kind: &structpb.Value_StructValue{
							StructValue: &structpb.Struct{
								Fields: map[string]*structpb.Value{
									"descriptors": {
										Kind: &structpb.Value_ListValue{
											ListValue: &structpb.ListValue{
												Values: []*structpb.Value{
													{
														Kind: &structpb.Value_StructValue{
															StructValue: &structpb.Struct{
																Fields: map[string]*structpb.Value{
																	"matches": {
																		Kind: &structpb.Value_ListValue{
																			ListValue: &structpb.ListValue{
																				Values: []*structpb.Value{
																					stringValue("header-Exact-0", "true"),
																					stringValue("dynamic-metadata-0", "value1"),
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
										},
									},
								},
							},
						},
					},
				},
			},
			expectedEntities: []*rlcommonv3.RateLimitDescriptor_Entry{
				{
					Key:   "header-Exact-0",
					Value: "true",
				},
				{
					Key:   "dynamic-metadata-0",
					Value: "value1",
				},
			},
			expectedRequest: &rlsv3.RateLimitRequest{
				Domain: "fake-domain",
				Descriptors: []*rlcommonv3.RateLimitDescriptor{
					{
						Entries: []*rlcommonv3.RateLimitDescriptor_Entry{
							{
								Key:   "LLM-Backend",
								Value: "fake-backend",
							},
							{
								Key:   "LLM-RateLimit-Type",
								Value: "rule-0-Token-1",
							},
							{
								Key:   "header-Exact-0",
								Value: "true",
							},
							{
								Key:   "dynamic-metadata-0",
								Value: "value1",
							},
						},
					},
				},
				HitsAddend: 1,
			},
		},
		{
			name: "model name exact",
			md: &corev3.Metadata{
				FilterMetadata: map[string]*structpb.Struct{
					"test1": {
						Fields: map[string]*structpb.Value{
							"key1": {
								Kind: &structpb.Value_StringValue{
									StringValue: "value1",
								},
							},
						},
					},
				},
			},
			reqHeaders: map[string]string{
				"header1": "value1",
				"header2": "value2",
			},
			rl: &aigv1a1.LLMTrafficPolicyRateLimit{
				Rules: []aigv1a1.LLMTrafficPolicyRateLimitRule{
					{
						Headers: []aigv1a1.LLMPolicyRateLimitHeaderMatch{
							{
								Type:  aigv1a1.HeaderMatchExact,
								Value: ptr.To("fake-model-name"),
								Name:  aigv1a1.LLMModelNameHeaderKey,
							},
						},
						Metadata: []aigv1a1.LLMPolicyRateLimitMetadataMatch{
							{
								Name:  "test1",
								Paths: []string{"key1"},
							},
						},
						Limits: []aigv1a1.LLMPolicyRateLimitValue{
							{
								Type: aigv1a1.RateLimitTypeToken,
							},
						},
					},
				},
			},
			expectedMetadata: &structpb.Struct{
				Fields: map[string]*structpb.Value{
					"llm.ratelimit": {
						Kind: &structpb.Value_StructValue{
							StructValue: &structpb.Struct{
								Fields: map[string]*structpb.Value{
									"descriptors": {
										Kind: &structpb.Value_ListValue{
											ListValue: &structpb.ListValue{
												Values: []*structpb.Value{
													{
														Kind: &structpb.Value_StructValue{
															StructValue: &structpb.Struct{
																Fields: map[string]*structpb.Value{
																	"matches": {
																		Kind: &structpb.Value_ListValue{
																			ListValue: &structpb.ListValue{
																				Values: []*structpb.Value{
																					stringValue("x-ai-gateway-llm-model-name/header-Exact-0", "fake-model-name"),
																					stringValue("dynamic-metadata-0", "value1"),
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
										},
									},
								},
							},
						},
					},
				},
			},
			expectedEntities: []*rlcommonv3.RateLimitDescriptor_Entry{
				{
					Key:   "header-Exact-0",
					Value: "true",
				},
				{
					Key:   "dynamic-metadata-0",
					Value: "value1",
				},
			},
			expectedRequest: &rlsv3.RateLimitRequest{
				Domain: "fake-domain",
				Descriptors: []*rlcommonv3.RateLimitDescriptor{
					{
						Entries: []*rlcommonv3.RateLimitDescriptor_Entry{
							{
								Key:   "LLM-Backend",
								Value: "fake-backend",
							},
							{
								Key:   "LLM-RateLimit-Type",
								Value: "rule-0-Token-1",
							},
							{
								Key:   "header-Exact-0",
								Value: "true",
							},
							{
								Key:   "dynamic-metadata-0",
								Value: "value1",
							},
						},
					},
				},
				HitsAddend: 1,
			},
		},
		{
			name: "model name distinct",
			md: &corev3.Metadata{
				FilterMetadata: map[string]*structpb.Struct{
					"test1": {
						Fields: map[string]*structpb.Value{
							"key1": {
								Kind: &structpb.Value_StringValue{
									StringValue: "value1",
								},
							},
						},
					},
				},
			},
			reqHeaders: map[string]string{
				"header1": "value1",
				"header2": "value2",
			},
			rl: &aigv1a1.LLMTrafficPolicyRateLimit{
				Rules: []aigv1a1.LLMTrafficPolicyRateLimitRule{
					{
						Headers: []aigv1a1.LLMPolicyRateLimitHeaderMatch{
							{
								Type: aigv1a1.HeaderMatchDistinct,
								Name: aigv1a1.LLMModelNameHeaderKey,
							},
						},
						Metadata: []aigv1a1.LLMPolicyRateLimitMetadataMatch{
							{
								Name:  "test1",
								Paths: []string{"key1"},
							},
						},
						Limits: []aigv1a1.LLMPolicyRateLimitValue{
							{
								Type: aigv1a1.RateLimitTypeToken,
							},
						},
					},
				},
			},
			expectedMetadata: &structpb.Struct{
				Fields: map[string]*structpb.Value{
					"llm.ratelimit": {
						Kind: &structpb.Value_StructValue{
							StructValue: &structpb.Struct{
								Fields: map[string]*structpb.Value{
									"descriptors": {
										Kind: &structpb.Value_ListValue{
											ListValue: &structpb.ListValue{
												Values: []*structpb.Value{
													{
														Kind: &structpb.Value_StructValue{
															StructValue: &structpb.Struct{
																Fields: map[string]*structpb.Value{
																	"matches": {
																		Kind: &structpb.Value_ListValue{
																			ListValue: &structpb.ListValue{
																				Values: []*structpb.Value{
																					stringValue("x-ai-gateway-llm-model-name/header-Distinct-0", ""),
																					stringValue("dynamic-metadata-0", "value1"),
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
										},
									},
								},
							},
						},
					},
				},
			},
			expectedEntities: []*rlcommonv3.RateLimitDescriptor_Entry{
				{
					Key:   "header-Distinct-0",
					Value: "fake-model-name",
				},
				{
					Key:   "dynamic-metadata-0",
					Value: "value1",
				},
			},
			expectedRequest: &rlsv3.RateLimitRequest{
				Domain: "fake-domain",
				Descriptors: []*rlcommonv3.RateLimitDescriptor{
					{
						Entries: []*rlcommonv3.RateLimitDescriptor_Entry{
							{
								Key:   "LLM-Backend",
								Value: "fake-backend",
							},
							{
								Key:   "LLM-RateLimit-Type",
								Value: "rule-0-Token-1",
							},
							{
								Key:   "header-Distinct-0",
								Value: "fake-model-name",
							},
							{
								Key:   "dynamic-metadata-0",
								Value: "value1",
							},
						},
					},
				},
				HitsAddend: 1,
			},
		},
		{
			name: "model name regex",
			md: &corev3.Metadata{
				FilterMetadata: map[string]*structpb.Struct{
					"test1": {
						Fields: map[string]*structpb.Value{
							"key1": {
								Kind: &structpb.Value_StringValue{
									StringValue: "value1",
								},
							},
						},
					},
				},
			},
			reqHeaders: map[string]string{
				"header1": "value1",
				"header2": "value2",
			},
			rl: &aigv1a1.LLMTrafficPolicyRateLimit{
				Rules: []aigv1a1.LLMTrafficPolicyRateLimitRule{
					{
						Headers: []aigv1a1.LLMPolicyRateLimitHeaderMatch{
							{
								Type:  aigv1a1.HeaderMatchRegularExpression,
								Value: ptr.To("fake-model.*"),
								Name:  aigv1a1.LLMModelNameHeaderKey,
							},
						},
						Metadata: []aigv1a1.LLMPolicyRateLimitMetadataMatch{
							{
								Name:  "test1",
								Paths: []string{"key1"},
							},
						},
						Limits: []aigv1a1.LLMPolicyRateLimitValue{
							{
								Type: aigv1a1.RateLimitTypeToken,
							},
						},
					},
				},
			},
			expectedMetadata: &structpb.Struct{
				Fields: map[string]*structpb.Value{
					"llm.ratelimit": {
						Kind: &structpb.Value_StructValue{
							StructValue: &structpb.Struct{
								Fields: map[string]*structpb.Value{
									"descriptors": {
										Kind: &structpb.Value_ListValue{
											ListValue: &structpb.ListValue{
												Values: []*structpb.Value{
													{
														Kind: &structpb.Value_StructValue{
															StructValue: &structpb.Struct{
																Fields: map[string]*structpb.Value{
																	"matches": {
																		Kind: &structpb.Value_ListValue{
																			ListValue: &structpb.ListValue{
																				Values: []*structpb.Value{
																					stringValue("x-ai-gateway-llm-model-name/header-RegularExpression-0", "fake-model.*"),
																					stringValue("dynamic-metadata-0", "value1"),
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
										},
									},
								},
							},
						},
					},
				},
			},
			expectedEntities: []*rlcommonv3.RateLimitDescriptor_Entry{
				{
					Key:   "header-RegularExpression-0",
					Value: "true",
				},
				{
					Key:   "dynamic-metadata-0",
					Value: "value1",
				},
			},
			expectedRequest: &rlsv3.RateLimitRequest{
				Domain: "fake-domain",
				Descriptors: []*rlcommonv3.RateLimitDescriptor{
					{
						Entries: []*rlcommonv3.RateLimitDescriptor_Entry{
							{
								Key:   "LLM-Backend",
								Value: "fake-backend",
							},
							{
								Key:   "LLM-RateLimit-Type",
								Value: "rule-0-Token-1",
							},
							{
								Key:   "header-RegularExpression-0",
								Value: "true",
							},
							{
								Key:   "dynamic-metadata-0",
								Value: "value1",
							},
						},
					},
				},
				HitsAddend: 1,
			},
		},
		{
			name: "multiple rules",
			md: &corev3.Metadata{
				FilterMetadata: map[string]*structpb.Struct{
					"test1": {
						Fields: map[string]*structpb.Value{
							"key1": {
								Kind: &structpb.Value_StringValue{
									StringValue: "value1",
								},
							},
							"key2": {
								Kind: &structpb.Value_StringValue{
									StringValue: "value2",
								},
							},
						},
					},
				},
			},
			reqHeaders: map[string]string{
				"header1": "value1",
				"header2": "value2",
			},
			rl: &aigv1a1.LLMTrafficPolicyRateLimit{
				Rules: []aigv1a1.LLMTrafficPolicyRateLimitRule{
					{
						Headers: []aigv1a1.LLMPolicyRateLimitHeaderMatch{
							{
								Type:  aigv1a1.HeaderMatchExact,
								Value: ptr.To("value1"),
								Name:  "header1",
							},
						},
						Metadata: []aigv1a1.LLMPolicyRateLimitMetadataMatch{
							{
								Name:  "test1",
								Paths: []string{"key1"},
							},
						},
						Limits: []aigv1a1.LLMPolicyRateLimitValue{
							{
								Type: aigv1a1.RateLimitTypeToken,
							},
						},
					},
					{
						Headers: []aigv1a1.LLMPolicyRateLimitHeaderMatch{
							{
								Type:  aigv1a1.HeaderMatchExact,
								Value: ptr.To("value2"),
								Name:  "header2",
							},
						},
						Metadata: []aigv1a1.LLMPolicyRateLimitMetadataMatch{
							{
								Name:  "test1",
								Paths: []string{"key2"},
							},
						},
						Limits: []aigv1a1.LLMPolicyRateLimitValue{
							{
								Type: aigv1a1.RateLimitTypeToken,
							},
						},
					},
				},
			},
			expectedMetadata: &structpb.Struct{
				Fields: map[string]*structpb.Value{
					"llm.ratelimit": {
						Kind: &structpb.Value_StructValue{
							StructValue: &structpb.Struct{
								Fields: map[string]*structpb.Value{
									"descriptors": {
										Kind: &structpb.Value_ListValue{
											ListValue: &structpb.ListValue{
												Values: []*structpb.Value{
													{
														Kind: &structpb.Value_StructValue{
															StructValue: &structpb.Struct{
																Fields: map[string]*structpb.Value{
																	"matches": {
																		Kind: &structpb.Value_ListValue{
																			ListValue: &structpb.ListValue{
																				Values: []*structpb.Value{
																					stringValue("header-Exact-0", "true"),
																					stringValue("dynamic-metadata-0", "value1"),
																				},
																			},
																		},
																	},
																},
															},
														},
													},
													{
														Kind: &structpb.Value_StructValue{
															StructValue: &structpb.Struct{
																Fields: map[string]*structpb.Value{
																	"matches": {
																		Kind: &structpb.Value_ListValue{
																			ListValue: &structpb.ListValue{
																				Values: []*structpb.Value{
																					stringValue("header-Exact-0", "true"),
																					stringValue("dynamic-metadata-0", "value2"),
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
										},
									},
								},
							},
						},
					},
				},
			},
			expectedEntities: []*rlcommonv3.RateLimitDescriptor_Entry{
				{
					Key:   "header-Exact-0",
					Value: "true",
				},
				{
					Key:   "dynamic-metadata-0",
					Value: "value2",
				},
			},
			expectedRequest: &rlsv3.RateLimitRequest{
				Domain: "fake-domain",
				Descriptors: []*rlcommonv3.RateLimitDescriptor{
					{
						Entries: []*rlcommonv3.RateLimitDescriptor_Entry{
							{
								Key:   "LLM-Backend",
								Value: "fake-backend",
							},
							{
								Key:   "LLM-RateLimit-Type",
								Value: "rule-0-Token-1",
							},
							{
								Key:   "header-Exact-0",
								Value: "true",
							},
							{
								Key:   "dynamic-metadata-0",
								Value: "value2",
							},
						},
					},
				},
				HitsAddend: 1,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildLLMRatelimitDynamicMetadata(tc.md, tc.reqHeaders, tc.rl)
			require.Equal(t, tc.expectedMetadata.String(), got.String())
			rLMetadata := got.Fields["llm.ratelimit"].GetStructValue()
			rLMetadata.Fields[ratelimit.ModelNameKey] = &structpb.Value{
				Kind: &structpb.Value_StringValue{
					StringValue: "fake-model-name",
				},
			}
			md := &corev3.Metadata{
				FilterMetadata: map[string]*structpb.Struct{
					"llm.ratelimit": rLMetadata,
				},
			}
			gotEntities := ExtractRatelimitDynamicMetadata(md, len(tc.rl.Rules)-1)
			require.Equal(t, tc.expectedEntities, gotEntities)

			gotRequest := BuildRateLimitRequest("fake-backend", 0, 1, gotEntities, "fake-domain", 1)
			require.Equal(t, tc.expectedRequest, gotRequest)
		})
	}
}

func TestExtractDynamicMetadata(t *testing.T) {
	cases := []struct {
		md       *corev3.Metadata
		ns       string
		paths    []string
		expected string
	}{
		{
			md: &corev3.Metadata{
				FilterMetadata: map[string]*structpb.Struct{
					"envoy.filters.http.jwt_authn": {
						Fields: map[string]*structpb.Value{
							"testing@secure.istio.io": {
								Kind: &structpb.Value_StructValue{
									StructValue: &structpb.Struct{
										Fields: map[string]*structpb.Value{
											"foo": {
												Kind: &structpb.Value_StringValue{
													StringValue: "bar",
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
			ns:       "envoy.filters.http.jwt_authn",
			paths:    []string{"testing@secure.istio.io", "foo"},
			expected: "bar",
		},
		{
			md: &corev3.Metadata{
				FilterMetadata: map[string]*structpb.Struct{
					"envoy.filters.http.jwt_authn": {
						Fields: map[string]*structpb.Value{
							"testing@secure.istio.io": {
								Kind: &structpb.Value_StructValue{
									StructValue: &structpb.Struct{
										Fields: map[string]*structpb.Value{
											"foo1": {
												Kind: &structpb.Value_StructValue{
													StructValue: &structpb.Struct{
														Fields: map[string]*structpb.Value{
															"foo2": {
																Kind: &structpb.Value_StringValue{
																	StringValue: "bar",
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
						},
					},
				},
			},
			ns:       "envoy.filters.http.jwt_authn",
			paths:    []string{"testing@secure.istio.io", "foo1", "foo2"},
			expected: "bar",
		},
	}

	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			got := extractDynamicMetadata(tc.md, tc.ns, tc.paths)
			require.Equal(t, tc.expected, got)
		})
	}
}
