package xds

import (
	"fmt"
	"os"
	"testing"

	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit"
)

func TestTranslateRateLimitActions(t *testing.T) {
	cases := []struct {
		name     string
		expected []*routev3.RateLimit
	}{
		{
			name:     "empty",
			expected: nil,
		},
		{
			name: "quickstart",
			expected: []*routev3.RateLimit{
				{
					Actions: []*routev3.RateLimit_Action{
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   ratelimit.BackendNameDescriptorKey,
									DescriptorValue: "backend-ratelimit",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMRoutingHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "backend-ratelimit",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
								GenericKey: &routev3.RateLimit_Action_GenericKey{
									DescriptorKey:   ratelimit.LimitTypeDescriptorKey,
									DescriptorValue: "rule-0-Token-0",
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   "header-Exact-0",
									DescriptorValue: "true",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMModelNameHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "gpt-4o-mini",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_RequestHeaders_{
								RequestHeaders: &routev3.RateLimit_Action_RequestHeaders{
									HeaderName:    "x-user-id",
									DescriptorKey: "header-Distinct-1",
									SkipIfAbsent:  true,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multipleBackends",
			expected: []*routev3.RateLimit{
				{
					Actions: []*routev3.RateLimit_Action{
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   ratelimit.BackendNameDescriptorKey,
									DescriptorValue: "backend-testupstream",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMRoutingHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "backend-testupstream",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
								GenericKey: &routev3.RateLimit_Action_GenericKey{
									DescriptorKey:   ratelimit.LimitTypeDescriptorKey,
									DescriptorValue: "rule-0-Token-0",
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   "header-Exact-0",
									DescriptorValue: "true",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMModelNameHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "gpt-4o-mini",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_RequestHeaders_{
								RequestHeaders: &routev3.RateLimit_Action_RequestHeaders{
									HeaderName:    "x-user-id",
									DescriptorKey: "header-Distinct-1",
									SkipIfAbsent:  true,
								},
							},
						},
					},
				},
				{
					Actions: []*routev3.RateLimit_Action{
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   ratelimit.BackendNameDescriptorKey,
									DescriptorValue: "backend-testupstream-canary",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMRoutingHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "backend-testupstream-canary",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
								GenericKey: &routev3.RateLimit_Action_GenericKey{
									DescriptorKey:   ratelimit.LimitTypeDescriptorKey,
									DescriptorValue: "rule-0-Token-0",
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   "header-Exact-0",
									DescriptorValue: "true",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMModelNameHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "gpt-4o-turbo",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_RequestHeaders_{
								RequestHeaders: &routev3.RateLimit_Action_RequestHeaders{
									HeaderName:    "x-user-id",
									DescriptorKey: "header-Distinct-1",
									SkipIfAbsent:  true,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "modelNameDistinct",
			expected: []*routev3.RateLimit{
				{
					Actions: []*routev3.RateLimit_Action{
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   ratelimit.BackendNameDescriptorKey,
									DescriptorValue: "backend-ratelimit",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMRoutingHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "backend-ratelimit",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
								GenericKey: &routev3.RateLimit_Action_GenericKey{
									DescriptorKey:   ratelimit.LimitTypeDescriptorKey,
									DescriptorValue: "rule-0-Token-0",
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_RequestHeaders_{
								RequestHeaders: &routev3.RateLimit_Action_RequestHeaders{
									HeaderName:    aigv1a1.LLMModelNameHeaderKey,
									DescriptorKey: "header-Distinct-0",
									SkipIfAbsent:  true,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "limits",
			expected: []*routev3.RateLimit{
				{
					Actions: []*routev3.RateLimit_Action{
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   ratelimit.BackendNameDescriptorKey,
									DescriptorValue: "backend-ratelimit",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMRoutingHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "backend-ratelimit",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
								GenericKey: &routev3.RateLimit_Action_GenericKey{
									DescriptorKey:   ratelimit.LimitTypeDescriptorKey,
									DescriptorValue: "rule-0-Request-0",
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   "header-Exact-0",
									DescriptorValue: "true",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMModelNameHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "gpt-4o-mini",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_RequestHeaders_{
								RequestHeaders: &routev3.RateLimit_Action_RequestHeaders{
									HeaderName:    "x-user-id",
									DescriptorKey: "header-Distinct-1",
									SkipIfAbsent:  true,
								},
							},
						},
					},
				},
				{
					Actions: []*routev3.RateLimit_Action{
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   ratelimit.BackendNameDescriptorKey,
									DescriptorValue: "backend-ratelimit",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMRoutingHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "backend-ratelimit",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
								GenericKey: &routev3.RateLimit_Action_GenericKey{
									DescriptorKey:   ratelimit.LimitTypeDescriptorKey,
									DescriptorValue: "rule-0-Token-1",
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   "header-Exact-0",
									DescriptorValue: "true",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMModelNameHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "gpt-4o-mini",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_RequestHeaders_{
								RequestHeaders: &routev3.RateLimit_Action_RequestHeaders{
									HeaderName:    "x-user-id",
									DescriptorKey: "header-Distinct-1",
									SkipIfAbsent:  true,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "headerMatchExact",
			expected: []*routev3.RateLimit{
				{
					Actions: []*routev3.RateLimit_Action{
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   ratelimit.BackendNameDescriptorKey,
									DescriptorValue: "backend-ratelimit",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMRoutingHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "backend-ratelimit",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
								GenericKey: &routev3.RateLimit_Action_GenericKey{
									DescriptorKey:   ratelimit.LimitTypeDescriptorKey,
									DescriptorValue: "rule-0-Token-0",
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   "header-Exact-0",
									DescriptorValue: "true",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMModelNameHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "gpt-4o-mini",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   "header-Exact-1",
									DescriptorValue: ratelimit.HeaderMatchedVal,
									Headers: []*routev3.HeaderMatcher{
										{
											Name: "x-user-id",
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "user1",
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
		{
			name: "blockUnknown",
			expected: []*routev3.RateLimit{
				{
					Actions: []*routev3.RateLimit_Action{
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   ratelimit.BackendNameDescriptorKey,
									DescriptorValue: "backend-ratelimit",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMRoutingHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "backend-ratelimit",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
								GenericKey: &routev3.RateLimit_Action_GenericKey{
									DescriptorKey:   ratelimit.LimitTypeDescriptorKey,
									DescriptorValue: "rule-0-Token-0",
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   "header-Exact-0",
									DescriptorValue: "true",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMModelNameHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "gpt-4o-mini",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_RequestHeaders_{
								RequestHeaders: &routev3.RateLimit_Action_RequestHeaders{
									HeaderName:    "x-user-id",
									DescriptorKey: "header-Distinct-1",
									SkipIfAbsent:  true,
								},
							},
						},
					},
				},
				{
					Actions: []*routev3.RateLimit_Action{
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   ratelimit.BackendNameDescriptorKey,
									DescriptorValue: "backend-ratelimit",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMRoutingHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "backend-ratelimit",
													},
												},
											},
										},
									},
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
								GenericKey: &routev3.RateLimit_Action_GenericKey{
									DescriptorKey:   ratelimit.LimitTypeDescriptorKey,
									DescriptorValue: "rule-1-Token-0",
								},
							},
						},
						{
							ActionSpecifier: &routev3.RateLimit_Action_HeaderValueMatch_{
								HeaderValueMatch: &routev3.RateLimit_Action_HeaderValueMatch{
									DescriptorKey:   "header-Exact-0",
									DescriptorValue: "true",
									Headers: []*routev3.HeaderMatcher{
										{
											Name: aigv1a1.LLMModelNameHeaderKey,
											HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
												StringMatch: &matcherv3.StringMatcher{
													MatchPattern: &matcherv3.StringMatcher_Exact{
														Exact: "unknown",
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(fmt.Sprintf("testdata/%s.yaml", tc.name))
			require.NoError(t, err)
			route := &aigv1a1.LLMRoute{}
			require.NoError(t, yaml.Unmarshal(data, route))
			actual := TranslateRateLimitActions(route)
			if len(actual) != len(tc.expected) {
				t.Errorf("expected %d rate limits, got %d", len(tc.expected), len(actual))
			}
			require.Equal(t, tc.expected, actual)
		})
	}
}
