// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package xdg

// Directories holds XDG Base Directory paths for aigw.
// See https://specifications.freedesktop.org/basedir-spec/latest/
type Directories struct {
	// ConfigHome is the base directory for user-specific configuration files.
	// XDG specification: $XDG_CONFIG_HOME
	// Default: ~/.config/aigw (or $AIGW_CONFIG_HOME)
	// Contents: config.yaml (default config), envoy-version (func-e version preference)
	ConfigHome string

	// DataHome is the base directory for user-specific data files.
	// XDG specification: $XDG_DATA_HOME
	// Default: ~/.local/share/aigw (or $AIGW_DATA_HOME)
	// Contents: envoy-versions/ (downloaded Envoy binaries via func-e)
	DataHome string

	// StateHome is the base directory for user-specific state data.
	// XDG specification: $XDG_STATE_HOME
	// Default: ~/.local/state/aigw (or $AIGW_STATE_HOME)
	// Contents: runs/{runID}/ (per-run logs and configs), envoy-runs/{runID}/ (func-e logs)
	StateHome string

	// RuntimeDir is the base directory for user-specific runtime files.
	// XDG specification: $XDG_RUNTIME_DIR
	// Default: /tmp/aigw-${UID} (or $AIGW_RUNTIME_DIR)
	// Contents: {runID}/uds.sock (extproc socket), {runID}/admin-address.txt (func-e admin)
	RuntimeDir string
}
