// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopeninference

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
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

	if diff := cmp.Diff(expectedCopy, actualCopy, protocmp.Transform()); diff != "" {
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

// normalizeJSON compacts JSON strings, handling invalid input as-is.
func normalizeJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(b)
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
	sort.Slice(attrs, func(i, j int) bool {
		return attrs[i].Key < attrs[j].Key
	})
}
