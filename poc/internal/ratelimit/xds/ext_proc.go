package xds

import (
	"fmt"
	"regexp"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	rlcommonv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	rlsv3 "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"google.golang.org/protobuf/types/known/structpb"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit"
)

func BuildRateLimitModelNameDynamicMetadata(modelName string) *structpb.Struct {
	return &structpb.Struct{
		Fields: map[string]*structpb.Value{
			ratelimit.LLMRateLimitMetadataNamespace: {
				Kind: &structpb.Value_StructValue{
					StructValue: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							ratelimit.ModelNameKey: {
								Kind: &structpb.Value_StringValue{
									StringValue: modelName,
								},
							},
						},
					},
				},
			},
		},
	}
}

const modelNameHeaderPrefix = aigv1a1.LLMModelNameHeaderKey + "/"

func BuildLLMRatelimitDynamicMetadata(md *corev3.Metadata, reqHeaders map[string]string, rl *aigv1a1.LLMTrafficPolicyRateLimit) *structpb.Struct {
	if rl == nil || len(rl.Rules) == 0 {
		return nil
	}

	matches := make([]*structpb.Value, 0)
	for _, rule := range rl.Rules {
		fields := make([]*structpb.Value, 0, 1)
		for idx, header := range rule.Headers {
			key := ratelimit.HeaderMatchKey(header.Type, idx)

			// ModelName is a special header, may not exist in the request
			// so we use a special placeholder and will rebuild it from Metadata later.
			// TODO: we may need to change upstream RateLimit filter to simply this by making it save RL request to metadata.
			headerVal := ""
			if header.Value != nil {
				headerVal = *header.Value
			}
			if header.Name == aigv1a1.LLMModelNameHeaderKey {
				fields = append(fields, stringValue(modelNameHeaderPrefix+key, headerVal))
				continue
			}

			val, exists := reqHeaders[strings.ToLower(header.Name)]
			switch header.Type {
			case aigv1a1.HeaderMatchDistinct:
				if exists {
					fields = append(fields, stringValue(key, val))
				}
			case aigv1a1.HeaderMatchExact:
				if header.Value == nil {
					// should not happen
					continue
				}
				if val == *header.Value {
					fields = append(fields, stringValue(key, ratelimit.HeaderMatchedVal))
				}
			case aigv1a1.HeaderMatchRegularExpression:
				if header.Value == nil {
					// should not happen
					continue
				}
				matched, err := regexp.MatchString(*header.Value, val)
				if err != nil {
					// should not happen
					continue
				}
				if matched {
					fields = append(fields, stringValue(key, ratelimit.HeaderMatchedVal))
				}
			}
		}

		for idx, match := range rule.Metadata {
			key := ratelimit.DynamicMetadataMatchKey(idx)
			val := extractDynamicMetadata(md, match.Name, match.Paths)
			fields = append(fields, stringValue(key, val))
		}

		matches = append(matches, &structpb.Value{
			Kind: &structpb.Value_StructValue{
				StructValue: &structpb.Struct{
					Fields: map[string]*structpb.Value{
						"matches": {
							Kind: &structpb.Value_ListValue{
								ListValue: &structpb.ListValue{
									Values: fields,
								},
							},
						},
					},
				},
			},
		})
	}

	return &structpb.Struct{
		Fields: map[string]*structpb.Value{
			ratelimit.LLMRateLimitMetadataNamespace: {
				Kind: &structpb.Value_StructValue{
					StructValue: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							ratelimit.DescriptorsKey: {
								Kind: &structpb.Value_ListValue{
									ListValue: &structpb.ListValue{
										Values: matches,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func extractDynamicMetadata(md *corev3.Metadata, namespace string, paths []string) string {
	var result *structpb.Value
	if data, ok := md.FilterMetadata[namespace]; ok {
		for _, p := range paths {
			if result == nil {
				if val, ok := data.Fields[p]; !ok {
					return ratelimit.MetadataNotFoundVal
				} else {
					result = val
				}
				continue
			}

			if result.GetStructValue() != nil {
				if val, ok := result.GetStructValue().Fields[p]; !ok {
					return ratelimit.MetadataNotFoundVal
				} else {
					result = val
				}
			} else {
				return ratelimit.MetadataNotFoundVal
			}
		}
	}
	if result == nil {
		return ratelimit.MetadataNotFoundVal
	}

	switch k := result.Kind.(type) {
	case *structpb.Value_StringValue:
		return k.StringValue
	case *structpb.Value_NumberValue:
		return fmt.Sprintf("%f", k.NumberValue)
	case *structpb.Value_BoolValue:
		return fmt.Sprintf("%t", k.BoolValue)
	case *structpb.Value_StructValue:
		return k.StructValue.String()
	case *structpb.Value_ListValue:
		return k.ListValue.String()
	case *structpb.Value_NullValue:
	default:
		return ratelimit.MetadataNotFoundVal
	}

	return ratelimit.MetadataNotFoundVal
}

func stringValue(key, value string) *structpb.Value {
	return &structpb.Value{
		Kind: &structpb.Value_StructValue{
			StructValue: &structpb.Struct{
				Fields: map[string]*structpb.Value{
					key: {
						Kind: &structpb.Value_StringValue{
							StringValue: value,
						},
					},
				},
			},
		},
	}
}

func ExtractRatelimitDynamicMetadata(md *corev3.Metadata, ruleIdx int) []*rlcommonv3.RateLimitDescriptor_Entry {
	entities := make([]*rlcommonv3.RateLimitDescriptor_Entry, 0)

	if data, ok := md.FilterMetadata[ratelimit.LLMRateLimitMetadataNamespace]; ok {
		modelNameField, exists := data.Fields[ratelimit.ModelNameKey]
		modelName := ""
		if exists {
			modelName = modelNameField.GetStringValue()
		}

		fields := data.Fields[ratelimit.DescriptorsKey]
		if fields == nil || len(fields.GetListValue().Values) <= ruleIdx {
			return nil
		}
		matches := fields.GetListValue().Values[ruleIdx].GetStructValue().Fields["matches"]
		for _, item := range matches.GetListValue().Values {
			for k, v := range item.GetStructValue().Fields {
				val := v.GetStringValue()
				if strings.HasPrefix(k, modelNameHeaderPrefix) { // e.g. x-ai-gateway-llm-model-name/header-Distinct-0
					// let's rebuild the model name from the Metadata
					trimmed := strings.TrimPrefix(k, modelNameHeaderPrefix) // e.g. header-Distinct-0
					split := strings.Split(trimmed, "-")
					if len(split) != 3 {
						// should not happen
						continue
					}
					entityVal := ""
					matchType, matchVal := split[1], val
					switch matchType {
					case string(aigv1a1.HeaderMatchDistinct):
						entityVal = modelName
					case string(aigv1a1.HeaderMatchExact):
						if matchVal == modelName {
							entityVal = ratelimit.HeaderMatchedVal
						}
					case string(aigv1a1.HeaderMatchRegularExpression):
						matched, err := regexp.MatchString(matchVal, modelName)
						if err != nil {
							// should not happen
							continue
						}
						if matched {
							entityVal = ratelimit.HeaderMatchedVal
						}
					default:
						// should not happen
						continue
					}
					if entityVal != "" {
						entities = append(entities, &rlcommonv3.RateLimitDescriptor_Entry{
							Key:   trimmed,
							Value: entityVal,
						})
					}
				} else {
					entities = append(entities, &rlcommonv3.RateLimitDescriptor_Entry{
						Key:   k,
						Value: val,
					})
				}
			}
		}
	}

	return entities
}

func BuildRateLimitRequest(backendName string, ruleIdx, limitIdx int,
	ruleEntities []*rlcommonv3.RateLimitDescriptor_Entry,
	domain string, hitsAddend uint32,
) *rlsv3.RateLimitRequest {
	entities := make([]*rlcommonv3.RateLimitDescriptor_Entry, 0, 2+len(ruleEntities))
	// always add token rate limit type to the descriptors
	entities = append(entities,
		// Backend Name
		&rlcommonv3.RateLimitDescriptor_Entry{
			Key:   ratelimit.BackendNameDescriptorKey,
			Value: backendName,
		},
		// LLM-RateLimit-Type
		&rlcommonv3.RateLimitDescriptor_Entry{
			Key:   ratelimit.LimitTypeDescriptorKey,
			Value: ratelimit.LimitKey(ruleIdx, aigv1a1.RateLimitTypeToken, limitIdx),
		})
	entities = append(entities, ruleEntities...)

	return &rlsv3.RateLimitRequest{
		Domain:      domain,
		Descriptors: []*rlcommonv3.RateLimitDescriptor{{Entries: entities}},
		HitsAddend:  hitsAddend,
	}
}
