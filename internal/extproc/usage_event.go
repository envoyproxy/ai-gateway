// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"time"

	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/usageevent"
)

// buildUsageEventID derives a stable per-response identifier for deduplication from the
// gateway-generated request ID, route, and backend, per the proposal's UsageEvent design.
func buildUsageEventID(requestID, routeName, backendName string) string {
	return requestID + "|" + routeName + "|" + backendName
}

// extractUsageEventAttributes copies allowlisted header values into a UsageEvent's Attributes
// map. Only headers explicitly present in attributeHeaders (configured via
// --usage-events-attributes) are copied, so operators control exactly what attribution
// metadata crosses the trust boundary into exported events; everything else, including
// Authorization, is never included.
func extractUsageEventAttributes(headers, attributeHeaders map[string]string) map[string]string {
	if len(attributeHeaders) == 0 {
		return nil
	}
	var attrs map[string]string
	for header, attrKey := range attributeHeaders {
		if v, ok := headers[header]; ok && v != "" {
			if attrs == nil {
				attrs = make(map[string]string, len(attributeHeaders))
			}
			attrs[attrKey] = v
		}
	}
	return attrs
}

// usageEventParams holds the per-response fields needed to construct a UsageEvent.
type usageEventParams struct {
	requestID     string
	routeName     string
	backendName   string
	provider      string
	reqModel      string
	responseModel string
	statusCode    int
	succeeded     bool
	usage         metrics.TokenUsage
	attributes    map[string]string
	now           time.Time
}

// buildUsageEvent constructs the normalized UsageEvent for a completed request from
// per-response metadata already tracked by the upstream processor.
func buildUsageEvent(p *usageEventParams) usageevent.UsageEvent {
	status := usageevent.StatusSucceeded
	if !p.succeeded {
		status = usageevent.StatusFailed
	}
	input, _ := p.usage.InputTokens()
	output, _ := p.usage.OutputTokens()
	cachedInput, _ := p.usage.CachedInputTokens()
	cacheWrite, _ := p.usage.CacheCreationInputTokens()
	reasoning, _ := p.usage.ReasoningTokens()

	return usageevent.UsageEvent{
		SchemaVersion:         usageevent.SchemaVersion,
		EventID:               buildUsageEventID(p.requestID, p.routeName, p.backendName),
		EmittedAt:             p.now.UnixMilli(),
		RequestID:             p.requestID,
		Status:                status,
		StatusCode:            p.statusCode,
		Provider:              p.provider,
		Backend:               p.backendName,
		ModelRequested:        p.reqModel,
		ModelResponse:         p.responseModel,
		InputTokens:           int(input),
		OutputTokens:          int(output),
		CachedInputTokens:     int(cachedInput),
		CacheWriteInputTokens: int(cacheWrite),
		ReasoningTokens:       int(reasoning),
		Attributes:            p.attributes,
	}
}
