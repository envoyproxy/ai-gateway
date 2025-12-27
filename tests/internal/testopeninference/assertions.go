// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopeninference

import (
	"cmp"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-json"
	gocmp "github.com/google/go-cmp/cmp"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
)

// RequireSpanEqual asserts that two spans are equal after normalizing variable fields
// like IDs, timestamps, and JSON formatting.
func RequireSpanEqual(t testing.TB, expected, actual *tracev1.Span) {
	t.Helper()

	if expected == actual {
		return
	}
	if expected == nil && actual != nil {
		t.Fatalf("expected span is nil but actual is not nil")
		return
	}
	if actual == nil && expected != nil {
		t.Fatalf("actual span is nil but expected is not nil")
		return
	}

	// Clone the spans to avoid modifying the originals.
	expectedCopy := proto.Clone(expected).(*tracev1.Span)
	actualCopy := proto.Clone(actual).(*tracev1.Span)

	normalizeSpanForComparison(expectedCopy)
	normalizeSpanForComparison(actualCopy)

	if diff := gocmp.Diff(expectedCopy, actualCopy, protocmp.Transform()); diff != "" {
		t.Fatalf("spans are not equal (-expected +actual):\n%s", diff)
	}
}

// normalizeSpanForComparison clears variable fields, normalizes JSON and errors,
// filters zero/empty attributes, and sorts attributes for consistent comparison.
func normalizeSpanForComparison(span *tracev1.Span) {
	span.TraceId = nil
	span.SpanId = nil
	span.ParentSpanId = nil
	span.StartTimeUnixNano = 0
	span.EndTimeUnixNano = 0

	for _, event := range span.Events {
		event.TimeUnixNano = 0
		event.Attributes = filterAndNormalizeEventAttrs(event.Attributes)
	}

	for _, link := range span.Links {
		link.TraceId = nil
		link.SpanId = nil
	}

	for _, attr := range span.Attributes {
		if sv := attr.Value.GetStringValue(); strings.HasPrefix(sv, "{") || strings.HasPrefix(sv, "[") {
			attr.Value.Value = &commonv1.AnyValue_StringValue{StringValue: normalizeJSON(sv)}
		}
	}

	span.Attributes = filterSpanAttrs(span.Attributes, hasErrorEvent(span.Events))

	sortAttributes(span.Attributes)

	if span.Status != nil && span.Status.Message != "" {
		span.Status.Message = normalizeErrorMessage(span.Status.Message)
	}
}

// filterAndNormalizeEventAttrs removes Python-specific attrs and normalizes exceptions.
func filterAndNormalizeEventAttrs(attrs []*commonv1.KeyValue) []*commonv1.KeyValue {
	filtered := make([]*commonv1.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		switch attr.Key {
		case "exception.stacktrace", "exception.escaped":
			continue
		case "exception.type":
			if sv := attr.Value.GetStringValue(); sv != "" {
				attr.Value.Value = &commonv1.AnyValue_StringValue{StringValue: strings.TrimPrefix(sv, "openai.")}
			}
		case "exception.message":
			if sv := attr.Value.GetStringValue(); sv != "" {
				attr.Value.Value = &commonv1.AnyValue_StringValue{StringValue: normalizeErrorMessage(sv)}
			}
		}
		filtered = append(filtered, attr)
	}
	return filtered
}

// filterSpanAttrs removes Python-specific attrs, skips zeros/empties, and error-specific fields.
func filterSpanAttrs(attrs []*commonv1.KeyValue, hasError bool) []*commonv1.KeyValue {
	filtered := make([]*commonv1.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		if attr.Key == "exception.stacktrace" || attr.Key == "exception.escaped" {
			continue
		}
		if hasError && attr.Key == "llm.model_name" {
			continue
		}
		if isZeroValue(attr.Value) {
			continue
		}
		filtered = append(filtered, attr)
	}
	return filtered
}

// hasErrorEvent checks if the span has an exception event.
func hasErrorEvent(events []*tracev1.Span_Event) bool {
	for _, e := range events {
		if e.Name == "exception" {
			return true
		}
	}
	return false
}

// isZeroValue returns true if the value is zero or empty.
func isZeroValue(v *commonv1.AnyValue) bool {
	if v == nil {
		return true
	}
	switch val := v.Value.(type) {
	case *commonv1.AnyValue_StringValue:
		return val.StringValue == ""
	case *commonv1.AnyValue_IntValue:
		return val.IntValue == 0
	case *commonv1.AnyValue_DoubleValue:
		return val.DoubleValue == 0
	case *commonv1.AnyValue_BoolValue:
		return !val.BoolValue
	case *commonv1.AnyValue_ArrayValue:
		return val.ArrayValue == nil || len(val.ArrayValue.Values) == 0
	case *commonv1.AnyValue_KvlistValue:
		return val.KvlistValue == nil || len(val.KvlistValue.Values) == 0
	case *commonv1.AnyValue_BytesValue:
		return len(val.BytesValue) == 0
	default:
		return false
	}
}

// normalizeJSON compacts JSON strings, removing zero-value objects.
func normalizeJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	processedV := processValue(v)
	b, err := json.Marshal(processedV)
	if err != nil {
		return s
	}
	return string(b)
}

// processValue recursively processes JSON values, removing zero-value objects.
func processValue(v any) any {
	if v == nil {
		return nil
	}

	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Map:
		result := make(map[string]any)
		allZero := true
		for _, key := range val.MapKeys() {
			keyStr := key.String()
			elem := val.MapIndex(key)
			processed := processValue(elem.Interface())
			if !isZero(processed) {
				allZero = false
				result[keyStr] = processed
			}
		}
		if allZero {
			return nil
		}
		return result
	case reflect.Slice:
		result := make([]any, 0, val.Len())
		allZero := true
		for i := 0; i < val.Len(); i++ {
			elem := val.Index(i)
			processed := processValue(elem.Interface())
			result = append(result, processed)
			if !isZero(processed) {
				allZero = false
			}
		}
		if allZero {
			return nil
		}
		return result
	default:
		// Return leaf values as-is; isZero checks will handle filtering at parent level
		return v
	}
}

// isZero checks if a JSON value is considered zero.
func isZero(v any) bool {
	if v == nil {
		return true
	}

	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Bool:
		return !val.Bool()
	case reflect.Float64:
		return val.Float() == 0
	case reflect.String:
		return val.Len() == 0
	case reflect.Array:
		return true
	case reflect.Map:
		// Maps where all values are zero are considered zero
		if val.Len() == 0 {
			return true
		}
		for _, key := range val.MapKeys() {
			if !isZero(val.MapIndex(key).Interface()) {
				return false
			}
		}
		return true
	case reflect.Slice:
		// Slices containing only nil/zero values are considered zero
		if val.Len() == 0 {
			return true
		}
		for i := 0; i < val.Len(); i++ {
			if !isZero(val.Index(i).Interface()) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// normalizeErrorMessage converts Python dicts to JSON and extracts error codes.
func normalizeErrorMessage(s string) string {
	s = strings.ReplaceAll(s, "'", "\"")
	if re := regexp.MustCompile(`Error code: \d+`); re.MatchString(s) {
		return re.FindString(s)
	}
	return s
}

// sortAttributes sorts key-value pairs by key.
func sortAttributes(attrs []*commonv1.KeyValue) {
	slices.SortFunc(attrs, func(a, b *commonv1.KeyValue) int {
		return cmp.Compare(a.Key, b.Key)
	})
}
