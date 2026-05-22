// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package peyeeye provides a Processor that integrates with the Peyeeye
// PII redaction & rehydration API (https://peyeeye.ai). On the request
// path it redacts PII from chat-completion-style request bodies before
// they are forwarded upstream; on the response path it rehydrates the
// model's output by swapping placeholders back to the original PII.
//
// In prose, "Peyeeye" / "peyeeye" is preferred. The mixed-case
// PEyeEye spelling is reserved for Go identifiers.
package peyeeye

import (
	"errors"
	"os"
	"strings"
)

// DefaultAPIBase is the default Peyeeye API base URL.
const DefaultAPIBase = "https://api.peyeeye.ai"

// SessionMode selects how Peyeeye holds the redaction mapping:
//
//   - SessionModeStateful: Peyeeye stores a token -> value mapping under a
//     ses_… id; rehydrate references the id.
//   - SessionModeStateless: Peyeeye returns a sealed skey_… blob; nothing is
//     retained server-side.
type SessionMode string

const (
	// SessionModeStateful is the default session mode.
	SessionModeStateful SessionMode = "stateful"
	// SessionModeStateless asks Peyeeye to return a sealed blob instead of
	// retaining the mapping server-side.
	SessionModeStateless SessionMode = "stateless"
)

// PEyeEyeConfig is the configuration for the Peyeeye processor.
//
// All knobs match the canonical reference integration in BerriAI/litellm
// so that operators of multiple gateways see the same names everywhere.
//
//nolint:revive // PEyeEye prefix is load-bearing brand naming, not stutter.
type PEyeEyeConfig struct {
	// APIKey is the Peyeeye API key. If empty, the value of the
	// PEYEEYE_API_KEY environment variable is used.
	APIKey string `json:"apiKey,omitempty"`

	// APIBase overrides the Peyeeye API base URL. If empty, the value of
	// the PEYEEYE_API_BASE environment variable is used, falling back to
	// DefaultAPIBase.
	APIBase string `json:"apiBase,omitempty"`

	// Locale is the BCP-47 locale hint passed to /v1/redact. Defaults to
	// "auto" when empty.
	Locale string `json:"locale,omitempty"`

	// Entities optionally restricts detection to a subset of Peyeeye
	// entity ids (e.g. "EMAIL", "PHONE_NUMBER"). When empty, Peyeeye's
	// default entity set is used.
	Entities []string `json:"entities,omitempty"`

	// SessionMode is "stateful" (default) or "stateless".
	SessionMode SessionMode `json:"sessionMode,omitempty"`
}

// ErrMissingAPIKey is returned when no Peyeeye API key is configured.
var ErrMissingAPIKey = errors.New(
	"peyeeye: missing API key; set the PEYEEYE_API_KEY environment variable " +
		"or PEyeEyeConfig.APIKey",
)

// Resolve fills in defaults from the environment and validates that an API
// key is available. It returns a copy with all fields set so the rest of
// the package can rely on non-empty values.
func (c *PEyeEyeConfig) Resolve() (PEyeEyeConfig, error) {
	out := PEyeEyeConfig{}
	if c != nil {
		out = *c
	}

	if out.APIKey == "" {
		out.APIKey = os.Getenv("PEYEEYE_API_KEY")
	}
	if out.APIKey == "" {
		return out, ErrMissingAPIKey
	}

	if out.APIBase == "" {
		out.APIBase = os.Getenv("PEYEEYE_API_BASE")
	}
	if out.APIBase == "" {
		out.APIBase = DefaultAPIBase
	}
	out.APIBase = strings.TrimRight(out.APIBase, "/")

	if out.Locale == "" {
		out.Locale = "auto"
	}

	switch out.SessionMode {
	case "":
		out.SessionMode = SessionModeStateful
	case SessionModeStateful, SessionModeStateless:
		// ok.
	default:
		return out, errors.New(
			"peyeeye: invalid SessionMode " + string(out.SessionMode) +
				`; must be "stateful" or "stateless"`,
		)
	}

	return out, nil
}
