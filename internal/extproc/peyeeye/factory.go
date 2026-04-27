// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package peyeeye

import (
	"log/slog"

	"github.com/envoyproxy/ai-gateway/internal/extproc"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

// WrapFactory decorates an existing extproc.ProcessorFactory so that every
// processor it produces is wrapped with PEyeEyeProcessor. Both router-level
// and upstream-level factories can be wrapped: redaction runs at the router
// level (where the raw request body is available) while rehydration runs on
// the response. For paths where the gateway only installs an upstream
// filter, the wrapper still adds value by redacting any request body that
// reaches it.
//
// All Peyeeye processor instances created via this wrapper share the
// supplied client. The client is safe for concurrent use from multiple
// processor instances.
func WrapFactory(inner extproc.ProcessorFactory, client Client, logger *slog.Logger) extproc.ProcessorFactory {
	if inner == nil {
		return inner
	}
	return func(
		config *filterapi.RuntimeConfig,
		requestHeaders map[string]string,
		log *slog.Logger,
		isUpstreamFilter bool,
		enableRedaction bool,
	) (extproc.Processor, error) {
		base, err := inner(config, requestHeaders, log, isUpstreamFilter, enableRedaction)
		if err != nil {
			return nil, err
		}
		return NewProcessor(base, client, logger), nil
	}
}
