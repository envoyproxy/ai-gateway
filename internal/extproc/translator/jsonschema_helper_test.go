// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func TestJsonSchemaDeepCopyMapStringAny(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]any
	}{
		{
			name:     "nil map",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty map",
			input:    map[string]any{},
			expected: map[string]any{},
		},
		{
			name: "simple map",
			input: map[string]any{
				"key1": "value1",
				"key2": 42,
				"key3": true,
			},
			expected: map[string]any{
				"key1": "value1",
				"key2": 42,
				"key3": true,
			},
		},
		{
			name: "nested map",
			input: map[string]any{
				"outer": map[string]any{
					"inner": "value",
				},
			},
			expected: map[string]any{
				"outer": map[string]any{
					"inner": "value",
				},
			},
		},
		{
			name: "map with slice",
			input: map[string]any{
				"items": []any{"a", "b", "c"},
			},
			expected: map[string]any{
				"items": []any{"a", "b", "c"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := jsonSchemaDeepCopyMapStringAny(tc.input)
			require.Equal(t, tc.expected, got)

			// Verify it's a deep copy by modifying original
			if tc.input != nil {
				tc.input["new_key"] = "new_value"
				require.NotContains(t, got, "new_key")
			}
		})
	}
}

func TestJsonSchemaDeepCopyAny(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected any
	}{
		{
			name:     "string",
			input:    "test",
			expected: "test",
		},
		{
			name:     "int",
			input:    42,
			expected: 42,
		},
		{
			name:     "bool",
			input:    true,
			expected: true,
		},
		{
			name:     "nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "map",
			input:    map[string]any{"key": "value"},
			expected: map[string]any{"key": "value"},
		},
		{
			name:     "slice",
			input:    []any{1, 2, 3},
			expected: []any{1, 2, 3},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := jsonSchemaDeepCopyAny(tc.input)
			require.Equal(t, tc.expected, got)
		})
	}
}

func TestJsonSchemaRetrieveRef(t *testing.T) {
	schema := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"User": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	tests := []struct {
		name           string
		path           string
		schema         map[string]any
		expectedResult any
		expectedErrMsg string
	}{
		{
			name:   "valid reference",
			path:   "#/components/schemas/User",
			schema: schema,
			expectedResult: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			},
		},
		{
			name:           "empty path",
			path:           "",
			schema:         schema,
			expectedErrMsg: "ref path cannot be empty",
		},
		{
			name:           "invalid path format",
			path:           "invalid/path",
			schema:         schema,
			expectedErrMsg: "ref paths are expected to be URI fragments",
		},
		{
			name:           "path with empty component",
			path:           "#//components",
			schema:         schema,
			expectedErrMsg: "ref path contains empty component",
		},
		{
			name:           "non-existent reference",
			path:           "#/components/schemas/NonExistent",
			schema:         schema,
			expectedErrMsg: "component 'NonExistent' does not exist",
		},
		{
			name:   "reference to non-map intermediate",
			path:   "#/components/schemas/User/type/invalid",
			schema: schema,
			expectedErrMsg: "intermediate component 'type' is not a map",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jsonSchemaRetrieveRef(tc.path, tc.schema)

			if tc.expectedErrMsg != "" {
				require.ErrorContains(t, err, tc.expectedErrMsg)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedResult, got)
			}
		})
	}
}

func TestJsonSchemaDereferenceHelper(t *testing.T) {
	schema := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"User": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	tests := []struct {
		name           string
		obj            any
		fullSchema     map[string]any
		skipKeys       []string
		processedRefs  map[string]struct{}
		expectedResult any
		expectedErrMsg string
	}{
		{
			name:       "primitive value",
			obj:        "string",
			fullSchema: schema,
			skipKeys:   []string{},
			expectedResult: "string",
		},
		{
			name:       "map without refs",
			obj:        map[string]any{"type": "string"},
			fullSchema: schema,
			skipKeys:   []string{},
			expectedResult: map[string]any{"type": "string"},
		},
		{
			name: "map with valid ref",
			obj: map[string]any{
				"$ref": "#/components/schemas/User",
			},
			fullSchema: schema,
			skipKeys:   []string{},
			expectedResult: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			},
		},
		{
			name: "map with non-string ref",
			obj: map[string]any{
				"$ref": 123,
			},
			fullSchema:     schema,
			skipKeys:       []string{},
			expectedErrMsg: "'$ref' value must be a string, got int",
		},
		{
			name: "recursive reference",
			obj: map[string]any{
				"$ref": "#/components/schemas/User",
			},
			fullSchema: schema,
			skipKeys:   []string{},
			processedRefs: map[string]struct{}{
				"#/components/schemas/User": {},
			},
			expectedErrMsg: "self recursive schema is currently not supported",
		},
		{
			name: "skip key",
			obj: map[string]any{
				"skipme": "value",
				"keepme": "value",
			},
			fullSchema: schema,
			skipKeys:   []string{"skipme"},
			expectedResult: map[string]any{
				"skipme": "value",
				"keepme": "value",
			},
		},
		{
			name:       "slice",
			obj:        []any{"a", "b"},
			fullSchema: schema,
			skipKeys:   []string{},
			expectedResult: []any{"a", "b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jsonSchemaDereferenceHelper(tc.obj, tc.fullSchema, tc.skipKeys, tc.processedRefs)

			if tc.expectedErrMsg != "" {
				require.ErrorContains(t, err, tc.expectedErrMsg)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedResult, got)
			}
		})
	}
}

func TestJsonSchemaSkipKeys(t *testing.T) {
	schema := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"User": map[string]any{
					"type": "object",
				},
			},
		},
	}

	tests := []struct {
		name           string
		obj            any
		fullSchema     map[string]any
		processedRefs  map[string]struct{}
		expectedResult []string
		expectedErrMsg string
	}{
		{
			name:           "primitive value",
			obj:            "string",
			fullSchema:     schema,
			expectedResult: nil,
		},
		{
			name: "map with ref",
			obj: map[string]any{
				"$ref": "#/components/schemas/User",
			},
			fullSchema:     schema,
			expectedResult: []string{"components"},
		},
		{
			name: "map with non-string ref",
			obj: map[string]any{
				"$ref": 123,
			},
			fullSchema:     schema,
			expectedErrMsg: "'$ref' value must be a string, got int",
		},
		{
			name: "recursive reference",
			obj: map[string]any{
				"$ref": "#/components/schemas/User",
			},
			fullSchema: schema,
			processedRefs: map[string]struct{}{
				"#/components/schemas/User": {},
			},
			expectedErrMsg: "self recursive schema is currently not supported",
		},
		{
			name:           "slice",
			obj:            []any{"a", "b"},
			fullSchema:     schema,
			expectedResult: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jsonSchemaSkipKeys(tc.obj, tc.fullSchema, tc.processedRefs)

			if tc.expectedErrMsg != "" {
				require.ErrorContains(t, err, tc.expectedErrMsg)
			} else {
				require.NoError(t, err)
				if tc.expectedResult == nil {
					require.Empty(t, got)
				} else {
					require.Equal(t, tc.expectedResult, got)
				}
			}
		})
	}
}

func TestJsonSchemaDereference(t *testing.T) {
	tests := []struct {
		name           string
		schemaObj      map[string]any
		expectedErrMsg string
	}{
		{
			name:           "nil schema",
			schemaObj:      nil,
			expectedErrMsg: "schema object cannot be nil",
		},
		{
			name: "valid schema",
			schemaObj: map[string]any{
				"type": "object",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jsonSchemaDereference(tc.schemaObj)

			if tc.expectedErrMsg != "" {
				require.ErrorContains(t, err, tc.expectedErrMsg)
			} else {
				require.NoError(t, err)
				require.NotNil(t, got)
			}
		})
	}
}

func TestJsonSchemaToGapic(t *testing.T) {
	allowedFields := map[string]struct{}{
		"type":        {},
		"description": {},
		"properties":  {},
		"items":       {},
		"nullable":    {},
		"anyOf":       {},
	}

	tests := []struct {
		name           string
		schema         map[string]any
		allowedFields  map[string]struct{}
		expectedErrMsg string
	}{
		{
			name:           "nil schema",
			schema:         nil,
			allowedFields:  allowedFields,
			expectedErrMsg: "schema cannot be nil",
		},
		{
			name:           "nil allowed fields",
			schema:         map[string]any{},
			allowedFields:  nil,
			expectedErrMsg: "allowedSchemaFieldsSet cannot be nil",
		},
		{
			name: "invalid items type",
			schema: map[string]any{
				"items": "not a map",
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "'items' must be a dict, got string",
		},
		{
			name: "invalid properties type",
			schema: map[string]any{
				"properties": "not a map",
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "'properties' must be a dict, got string",
		},
		{
			name: "invalid property type",
			schema: map[string]any{
				"properties": map[string]any{
					"field": "not a map",
				},
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "property 'field' must be a dict, got string",
		},
		{
			name: "invalid type value",
			schema: map[string]any{
				"type": 123,
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "the value of 'type' must be a list or a string, got int",
		},
		{
			name: "type list with wrong length",
			schema: map[string]any{
				"type": []any{"string", "number", "boolean"},
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "length of the list must be 2",
		},
		{
			name: "type list without null",
			schema: map[string]any{
				"type": []any{"string", "number"},
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "it must contain one non-null type and 'null'",
		},
		{
			name: "invalid allOf type",
			schema: map[string]any{
				"allOf": "not a list",
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "'allOf' must be a list, got string",
		},
		{
			name: "empty allOf",
			schema: map[string]any{
				"allOf": []any{},
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "'allOf' cannot be empty",
		},
		{
			name: "allOf with too many items",
			schema: map[string]any{
				"allOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "number"},
				},
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "Only one value for 'allOf' key is supported",
		},
		{
			name: "invalid allOf item type",
			schema: map[string]any{
				"allOf": []any{"not a map"},
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "item in 'allOf' must be an object",
		},
		{
			name: "invalid anyOf type",
			schema: map[string]any{
				"anyOf": "not a list",
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "'anyOf' must be a list, got string",
		},
		{
			name: "empty anyOf",
			schema: map[string]any{
				"anyOf": []any{},
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "'anyOf' cannot be empty",
		},
		{
			name: "invalid anyOf item type",
			schema: map[string]any{
				"anyOf": []any{"not a map"},
			},
			allowedFields:  allowedFields,
			expectedErrMsg: "item 0 in 'anyOf' must be a dict, got string",
		},
		{
			name: "valid schema",
			schema: map[string]any{
				"type":        "object",
				"description": "A test object",
				"properties": map[string]any{
					"name": map[string]any{
						"type": "string",
					},
				},
			},
			allowedFields: allowedFields,
		},
		{
			name: "schema with $defs (skipped)",
			schema: map[string]any{
				"type": "object",
				"$defs": map[string]any{
					"User": map[string]any{"type": "object"},
				},
			},
			allowedFields: allowedFields,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jsonSchemaToGapic(tc.schema, tc.allowedFields)

			if tc.expectedErrMsg != "" {
				require.ErrorContains(t, err, tc.expectedErrMsg)
			} else {
				require.NoError(t, err)
				require.NotNil(t, got)
			}
		})
	}
}

func TestJsonSchemaMapToSchema(t *testing.T) {
	tests := []struct {
		name           string
		schemaMap      map[string]any
		expectedErrMsg string
	}{
		{
			name:           "nil schema map",
			schemaMap:      nil,
			expectedErrMsg: "schemaMap cannot be nil",
		},
		{
			name: "valid schema map",
			schemaMap: map[string]any{
				"type": "string",
			},
		},
		{
			name: "schema map with invalid JSON values",
			schemaMap: map[string]any{
				"invalid": func() {}, // functions can't be marshaled to JSON
			},
			expectedErrMsg: "failed to marshal map to JSON",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jsonSchemaMapToSchema(tc.schemaMap)

			if tc.expectedErrMsg != "" {
				require.ErrorContains(t, err, tc.expectedErrMsg)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.NotNil(t, got)
			}
		})
	}
}

func TestJsonSchemaToGemini(t *testing.T) {
	tests := []struct {
		name           string
		schema         map[string]any
		expectedErrMsg string
	}{
		{
			name:           "nil schema",
			schema:         nil,
			expectedErrMsg: "schema cannot be nil",
		},
		{
			name: "valid schema",
			schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type": "string",
					},
				},
			},
		},
		{
			name: "schema with invalid reference",
			schema: map[string]any{
				"$ref": "#/invalid/ref",
			},
			expectedErrMsg: "Reference '#/invalid/ref' not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jsonSchemaToGemini(tc.schema)

			if tc.expectedErrMsg != "" {
				require.ErrorContains(t, err, tc.expectedErrMsg)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.NotNil(t, got)
			}
		})
	}
}

func TestJsonSchemaToGeminiIntegration(t *testing.T) {
	trueBool := true
	tests := []struct {
		name                   string
		input                  json.RawMessage
		expectedResponseSchema *genai.Schema
		expectedErrMsg         string
	}{
		{
			name: "nested schema for ResponseSchema",
			input: json.RawMessage(`{
    "type": "object",
    "properties": {
        "steps": {
            "type": "array",
            "items": {
                "$ref": "#/$defs/step"
            }
        },
        "final_answer": {
            "type": "string"
        }
    },
    "$defs": {
        "step": {
            "type": "object",
            "properties": {
                "explanation": {
                    "type": "string"
                },
                "output": {
                    "type": "string"
                }
            },
            "required": [
                "explanation",
                "output"
            ],
            "additionalProperties": false
        }
    },
    "required": [
        "steps",
        "final_answer"
    ],
    "additionalProperties": false
}`),

			expectedResponseSchema: &genai.Schema{
				Properties: map[string]*genai.Schema{
					"final_answer": {Type: "string"},
					"steps": {
						Items: &genai.Schema{
							Properties: map[string]*genai.Schema{
								"explanation": {Type: "string"},
								"output":      {Type: "string"},
							},
							Type:     "object",
							Required: []string{"explanation", "output"},
						},
						Type: "array",
					},
				},
				Type:     "object",
				Required: []string{"steps", "final_answer"},
			},
		},

		{
			name: "anyof list for ResponseSchema",
			input: json.RawMessage(`{
    "type": "object",
    "properties": {
        "item": {
            "anyOf": [
                {
                    "type": "object",
                    "description": "The user object to insert into the database",
                    "properties": {
                        "name": {
                            "type": "string",
                            "description": "The name of the user"
                        },
                        "age": {
                            "type": "number",
                            "description": "The age of the user"
                        }
                    },
                    "additionalProperties": false,
                    "required": [
                        "name",
                        "age"
                    ]
                },
                {
                    "type": "object",
                    "description": "The address object to insert into the database",
                    "properties": {
                        "number": {
                            "type": "string",
                            "description": "The number of the address. Eg. for 123 main st, this would be 123"
                        },
                        "street": {
                            "type": "string",
                            "description": "The street name. Eg. for 123 main st, this would be main st"
                        },
                        "city": {
                            "type": "string",
                            "description": "The city of the address"
                        }
                    },
                    "additionalProperties": false,
                    "required": [
                        "number",
                        "street",
                        "city"
                    ]
                },
                {
                    "type": "object",
                    "description": "The email address object to insert into the database",
                    "properties": {
                        "company": {
                            "type": "string",
                            "description": "The company to use."
                        },
                        "url": {
                            "type": "string",
                            "description": "The email address"
                        }
                    },
                    "additionalProperties": false,
                    "required": [
                        "company",
                        "url"
                    ]
                }
            ]
        }
    },
    "additionalProperties": false,
    "required": [
        "item"
    ]
}`),

			expectedResponseSchema: &genai.Schema{
				Properties: map[string]*genai.Schema{
					"item": {
						AnyOf: []*genai.Schema{
							{
								Description: "The user object to insert into the database",
								Properties: map[string]*genai.Schema{
									"age":  {Type: "number", Description: "The age of the user"},
									"name": {Type: "string", Description: "The name of the user"},
								},
								Type:     "object",
								Required: []string{"name", "age"},
							},
							{
								Description: "The address object to insert into the database",
								Properties: map[string]*genai.Schema{
									"city":   {Type: "string", Description: "The city of the address"},
									"number": {Type: "string", Description: "The number of the address. Eg. for 123 main st, this would be 123"},
									"street": {Type: "string", Description: "The street name. Eg. for 123 main st, this would be main st"},
								},
								Type:     "object",
								Required: []string{"number", "street", "city"},
							},
							{
								Description: "The email address object to insert into the database",
								Properties: map[string]*genai.Schema{
									"company": {Type: "string", Description: "The company to use."},
									"url":     {Type: "string", Description: "The email address"},
								},
								Required: []string{"company", "url"},
								Type:     "object",
							},
						},
					},
				},
				Type:     "object",
				Required: []string{"item"},
			},
		},

		{
			name: "anyof null for ResponseSchema",
			input: json.RawMessage(`{
    "type": "object",
    "description": "Data model identifying a single paragraph for paragraph re-ranking.",
    "properties": {
        "paragraph_id": {
            "anyOf": [
                {
                    "type": "string"
                },
                {
                    "type": "null"
                }
            ],
            "title": "Paragraph Id"
        },
        "document_id": {
            "title": "Document Id",
            "type": "string"
        }
    },
    "required": [
        "paragraph_id",
        "document_id"
    ],
    "title": "ParagraphIdentifier",
    "additionalProperties": false
}`),

			expectedResponseSchema: &genai.Schema{
				Description: "Data model identifying a single paragraph for paragraph re-ranking.",
				Properties: map[string]*genai.Schema{
					"document_id": {Type: "string", Title: "Document Id"},
					"paragraph_id": {
						AnyOf: []*genai.Schema{
							{
								Type: "string",
							},
						},
						Nullable: &trueBool,
						Title:    "Paragraph Id",
					},
				},
				Required: []string{"paragraph_id", "document_id"},
				Title:    "ParagraphIdentifier",
				Type:     "object",
			},
		},

		{
			name: "allOf with not a list",
			input: json.RawMessage(`{
    "type": "object",
    "properties": {
        "item": {
            "allOf": "string"
        }
    },
    "additionalProperties": false,
    "required": [
        "item"
    ]
}`),
			expectedErrMsg: "invalid JSON schema: 'allOf' must be a list",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var schemaMap map[string]any
			err := json.Unmarshal([]byte(tc.input), &schemaMap)
			require.NoError(t, err)

			got, err := jsonSchemaToGemini(schemaMap)

			if tc.expectedErrMsg != "" {
				require.ErrorContains(t, err, tc.expectedErrMsg)
			} else {
				require.NoError(t, err)

				if diff := cmp.Diff(tc.expectedResponseSchema, got, cmpopts.IgnoreUnexported(genai.Schema{})); diff != "" {
					t.Errorf("ResponseSchema mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}
