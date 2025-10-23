// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"google.golang.org/genai"
)

// get the supported field names
func jsonSchemaGetAllowedFields() map[string]struct{} {
	fieldSet := make(map[string]struct{})
	var schema *genai.Schema
	t := reflect.TypeOf(schema).Elem()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldSet[field.Name] = struct{}{}
	}

	return fieldSet
}

func jsonSchemaDeepCopyMapStringAny(original map[string]any) map[string]any {
	if original == nil {
		return nil
	}

	copied := make(map[string]any, len(original))
	for key, value := range original {
		copied[key] = jsonSchemaDeepCopyAny(value)
	}
	return copied
}

func jsonSchemaDeepCopyAny(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return jsonSchemaDeepCopyMapStringAny(v)
	case []any:
		copiedSlice := make([]any, len(v))
		for i, elem := range v {
			copiedSlice[i] = jsonSchemaDeepCopyAny(elem)
		}
		return copiedSlice
	default:
		// For primitive types (int, string, bool, etc.) and other value types,
		// direct assignment performs a copy.
		return value
	}
}

// jsonSchemaRetrieveRef fetches a deeply-nested reference from a schema map.
func jsonSchemaRetrieveRef(path string, schema map[string]any) (any, error) {
	components := strings.Split(path, "/")
	if components[0] != "#" {
		msg := "ref paths are expected to be URI fragments, meaning they should start with #."
		return nil, fmt.Errorf("invalid JSON schema: %s", msg)
	}

	out := schema
	for _, component := range components[1:] {
		// Type assertion to ensure `out` is a map.
		val, ok := out[component]
		if ok {
			out = val.(map[string]any)
		} else {
			msg := fmt.Sprintf("Reference '%s' not found: intermediate component is not a map", path)
			return nil, fmt.Errorf("invalid JSON schema: %s", msg)
		}
	}

	// Create and return a deep copy to prevent mutation of the original schema.
	deepCopy := jsonSchemaDeepCopyAny(out)
	return deepCopy, nil
}

// jsonSchemaDereferenceHelper recursively dereferences JSON schema references.
// Note: `processedRefs` is a pointer to a set (map[string]struct{})
// to ensure state is shared across recursive calls.
func jsonSchemaDereferenceHelper(
	obj any,
	fullSchema map[string]any,
	skipKeys []string,
	processedRefs *map[string]struct{},
) (any, error) {
	if *processedRefs == nil {
		*processedRefs = make(map[string]struct{})
	}

	// Handle dictionaries (maps).
	if dict, ok := obj.(map[string]any); ok {
		objOut := make(map[string]any)
		for k, v := range dict {
			// Check if key should be skipped.
			var shouldSkip bool
			for _, skipKey := range skipKeys {
				if k == skipKey {
					shouldSkip = true
					break
				}
			}
			if shouldSkip {
				objOut[k] = v
				continue
			}

			// Handle reference key "$ref".
			if k == "$ref" {
				refPath, isString := v.(string)
				if !isString {
					return nil, fmt.Errorf("'$ref' value must be a string")
				}
				if _, ok := (*processedRefs)[refPath]; ok {
					continue // Already processed, skip to avoid circular references.
				}
				(*processedRefs)[refPath] = struct{}{}

				ref, err := jsonSchemaRetrieveRef(refPath, fullSchema)
				if err != nil {
					return nil, err
				}
				fullRef, err := jsonSchemaDereferenceHelper(ref, fullSchema, skipKeys, processedRefs)
				if err != nil {
					return nil, err
				}
				delete(*processedRefs, refPath) // Remove from set on function exit.
				return fullRef, nil
			}

			// Recurse on nested dictionaries and lists.
			if _, isDict := v.(map[string]any); isDict {
				res, err := jsonSchemaDereferenceHelper(v, fullSchema, skipKeys, processedRefs)
				if err != nil {
					return nil, err
				}
				objOut[k] = res
			} else if _, isList := v.([]any); isList {
				res, err := jsonSchemaDereferenceHelper(v, fullSchema, skipKeys, processedRefs)
				if err != nil {
					return nil, err
				}
				objOut[k] = res
			} else {
				objOut[k] = v
			}
		}
		return objOut, nil
	}

	// Handle lists (slices).
	if list, ok := obj.([]any); ok {
		listOut := make([]any, len(list))
		for i, el := range list {
			res, err := jsonSchemaDereferenceHelper(el, fullSchema, skipKeys, processedRefs)
			if err != nil {
				return nil, err
			}
			listOut[i] = res
		}
		return listOut, nil
	}

	// Return non-dictionary and non-list types as is.
	return obj, nil
}

// jsonSchemaSkipKeys recursively traverses a schema to find keys that should be skipped., which is modified from https://github.com/langchain-ai/langchain/blob/fce8caca16121024547fb0e8eb2d289c8f96396a/libs/core/langchain_core/utils/json_schema.py#L71
func jsonSchemaSkipKeys(
	obj any,
	fullSchema map[string]any,
	processedRefs map[string]struct{},
) ([]string, error) {
	// Initialize the processedRefs set on the first call.
	if processedRefs == nil {
		processedRefs = make(map[string]struct{})
	}

	keys := []string{}

	// Handle dictionaries (maps).
	if dict, ok := obj.(map[string]any); ok {
		for k, v := range dict {
			if k == "$ref" {
				refPath, isString := v.(string)
				if !isString {
					return nil, fmt.Errorf("'$ref' value must be a string")
				}
				// Skip if reference has already been processed.
				if _, ok := processedRefs[refPath]; ok {
					continue
				}
				processedRefs[refPath] = struct{}{}

				ref, err := jsonSchemaRetrieveRef(refPath, fullSchema)
				if err != nil {
					return nil, err
				}

				// Add the top-level key of the reference to the list.
				// This relies on the reference path format, e.g., "#/components/..."
				components := strings.Split(refPath, "/")
				if len(components) > 1 {
					keys = append(keys, components[1])
				}

				// Recurse on the referenced schema.
				nestedKeys, err := jsonSchemaSkipKeys(ref, fullSchema, processedRefs)
				if err != nil {
					return nil, err
				}
				keys = append(keys, nestedKeys...)
			} else if _, isDict := v.(map[string]any); isDict {
				nestedKeys, err := jsonSchemaSkipKeys(v, fullSchema, processedRefs)
				if err != nil {
					return nil, err
				}
				keys = append(keys, nestedKeys...)
			} else if _, isList := v.([]any); isList {
				nestedKeys, err := jsonSchemaSkipKeys(v, fullSchema, processedRefs)
				if err != nil {
					return nil, err
				}
				keys = append(keys, nestedKeys...)
			}
		}
	} else if list, ok := obj.([]any); ok {
		// Handle lists (slices).
		for _, el := range list {
			nestedKeys, err := jsonSchemaSkipKeys(el, fullSchema, processedRefs)
			if err != nil {
				return nil, err
			}
			keys = append(keys, nestedKeys...)
		}
	}

	return keys, nil
}

// jsonSchemaDereference substitutes $refs in a JSON Schema object., this is adapted from https://github.com/langchain-ai/langchain/blob/fce8caca16121024547fb0e8eb2d289c8f96396a/libs/core/langchain_core/utils/json_schema.py#L95
func jsonSchemaDereference(
	schemaObj map[string]any,
) (any, error) {
	skipKeys, err := jsonSchemaSkipKeys(schemaObj, schemaObj, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid JSON schema: %w", err)
	}

	// Call the recursive helper function to perform the dereferencing.
	processedRefs := make(map[string]struct{})
	return jsonSchemaDereferenceHelper(schemaObj, schemaObj, skipKeys, &processedRefs)
}

// jsonSchemaToGapic formats a JSON schema for a gapic request, which is modified from https://github.com/langchain-ai/langchain-google/blob/06a5857841675461ae282648a155e1cd90d1e5d5/libs/vertexai/langchain_google_vertexai/functions_utils.py#L109
func jsonSchemaToGapic(schema map[string]any, allowedSchemaFieldsSet map[string]struct{}) (map[string]any, error) {
	convertedSchema := make(map[string]any)

	for key, value := range schema {
		switch key {
		case "$defs":
			continue
		case "items":
			if subSchema, ok := value.(map[string]any); ok {
				var err error
				convertedSchema["items"], err = jsonSchemaToGapic(subSchema, allowedSchemaFieldsSet)
				if err != nil {
					return nil, err
				}
			}
		case "properties":
			if properties, ok := value.(map[string]any); ok {
				convertedSchema["properties"] = make(map[string]any)
				for pkey, pvalue := range properties {
					if pSubSchema, ok := pvalue.(map[string]any); ok {
						var err error
						convertedSchema["properties"].(map[string]any)[pkey], err = jsonSchemaToGapic(pSubSchema, allowedSchemaFieldsSet)
						if err != nil {
							return nil, err
						}
					}
				}
			}
		case "type":
			if typeList, ok := value.([]any); ok {
				if len(typeList) == 2 {
					hasNull := false
					var nonNullType any
					for _, t := range typeList {
						if t == "null" {
							hasNull = true
						} else {
							nonNullType = t
						}
					}
					if hasNull && nonNullType != nil {
						if nonNullTypeMap, ok := nonNullType.(map[string]any); ok {
							res, err := jsonSchemaToGapic(nonNullTypeMap, allowedSchemaFieldsSet)
							if err != nil {
								return nil, err
							}
							for resKey, resVal := range res {
								convertedSchema[resKey] = resVal
							}
						} else {
							convertedSchema["type"] = fmt.Sprintf("%v", nonNullType)
						}
						convertedSchema["nullable"] = true
					} else {
						msg := "If type is a list, it must contain one non-null type and 'null'."
						return nil, fmt.Errorf("invalid JSON schema: %s", msg)
					}
				} else {
					msg := fmt.Sprintf("If the value of type is a list, the length of the list must be 2. Got %d.", len(typeList))
					return nil, fmt.Errorf("invalid JSON schema: %s", msg)
				}
			} else {
				convertedSchema["type"] = fmt.Sprintf("%v", value)
			}
		case "allOf":
			if allOfList, ok := value.([]any); ok {
				if len(allOfList) > 1 {
					msg := fmt.Sprintf("Only one value for 'allOf' key is supported. Got %d.", len(allOfList))
					return nil, fmt.Errorf("invalid JSON schema: %s", msg)
				}
				if subSchema, ok := allOfList[0].(map[string]any); ok {
					return jsonSchemaToGapic(subSchema, allowedSchemaFieldsSet)
				}
			}
		case "anyOf":
			if anyOfList, ok := value.([]any); ok {
				anyOfResults := make([]any, 0)
				nullable := false
				for _, v := range anyOfList {
					if subSchema, ok := v.(map[string]any); ok {
						if t, exists := subSchema["type"]; exists && t == "null" {
							nullable = true
						} else {
							res, err := jsonSchemaToGapic(subSchema, allowedSchemaFieldsSet)
							if err != nil {
								return nil, err
							}
							anyOfResults = append(anyOfResults, res)
						}
					}
				}
				if nullable {
					convertedSchema["nullable"] = true
				}
				convertedSchema["anyOf"] = anyOfResults
			}
		default:
			// Check if the key is in the allowed set.
			if _, allowed := allowedSchemaFieldsSet[key]; allowed {
				convertedSchema[key] = value
			}
		}
	}
	return convertedSchema, nil
}

// jsonSchemaMapToSchema converts a map[string]any to a Schema struct.
func jsonSchemaMapToSchema(schemaMap map[string]any) (*genai.Schema, error) {
	jsonBytes, err := json.Marshal(schemaMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal map to JSON: %w", err)
	}

	var genSchema genai.Schema
	if err := json.Unmarshal(jsonBytes, &genSchema); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON to Schema: %w", err)
	}

	return &genSchema, nil
}

// sanitizeJSONSchema first dereferences the schema and then formats it for GCP.
func jsonSchemaToGemini(schema map[string]any) (*genai.Schema, error) {
	dereferencedSchema, err := jsonSchemaDereference(schema)
	if err != nil {
		return nil, err
	}

	dereferencedMap, ok := dereferencedSchema.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("dereferenced schema was not a map[string]any")
	}
	allowedSchemaFieldsSet := jsonSchemaGetAllowedFields()

	schemaMap, err := jsonSchemaToGapic(dereferencedMap, allowedSchemaFieldsSet)
	if err != nil {
		return nil, err
	}

	var retSchema *genai.Schema
	retSchema, err = jsonSchemaMapToSchema(schemaMap)
	if err != nil {
		return nil, err
	}
	return retSchema, nil
}
