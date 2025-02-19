// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

// ConfigReceiver is an interface that can receive *filterapi.Config updates.
// This is mostly for decoupling and testing purposes.
type ConfigReceiver interface {
	// LoadConfig updates the configuration.
	LoadConfig(ctx context.Context, config *filterapi.Config) error
}

type configWatcher struct {
	lastMod time.Time
	path    string
	rcv     ConfigReceiver
	l       *slog.Logger
	current string
}

// StartConfigWatcher starts a watcher for the given path and Receiver.
// Periodically checks the file for changes and calls the Receiver's UpdateConfig method.
func StartConfigWatcher(ctx context.Context, path string, rcv ConfigReceiver, l *slog.Logger, tick time.Duration) error {
	cw := &configWatcher{rcv: rcv, l: l, path: path}

	if err := cw.loadConfig(ctx); err != nil {
		return fmt.Errorf("failed to load initial config: %w", err)
	}

	l.Info("start watching the config file", slog.String("path", path), slog.String("interval", tick.String()))
	go cw.watch(ctx, tick)
	return nil
}

// watch periodically checks the file for changes and calls the update method.
func (cw *configWatcher) watch(ctx context.Context, tick time.Duration) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			cw.l.Info("stop watching the config file", slog.String("path", cw.path))
			return
		case <-ticker.C:
			if err := cw.loadConfig(ctx); err != nil {
				cw.l.Error("failed to update config", slog.String("error", err.Error()))
			}
		}
	}
}

// loadConfig loads a new config from the given path and updates the Receiver by
// calling the [Receiver.Load].
func (cw *configWatcher) loadConfig(ctx context.Context) error {
	var (
		cfg *filterapi.Config
		raw []byte
	)

	stat, err := os.Stat(cw.path)
	if err != nil && os.IsNotExist(err) {
		// If the file does not exist, do not fail (which could lead to the extproc process to terminate)
		// Instead, load the default configuration and keep running unconfigured
		cfg, raw = filterapi.MustLoadDefaultConfig()
	}
	// If reading the file fails, or loading the default config fails, abort
	if err != nil {
		return err
	}

	if cfg != nil {
		cw.l.Info("config file does not exist; loading default config", slog.String("path", cw.path))
		cw.lastMod = time.Now()
	} else {
		cw.l.Info("loading a new config", slog.String("path", cw.path))
		if stat.ModTime().Sub(cw.lastMod) <= 0 {
			return nil
		}
		cw.lastMod = stat.ModTime()
		cfg, raw, err = filterapi.UnmarshalConfigYaml(cw.path)
		if err != nil {
			return err
		}
	}

	// Print the diff between the old and new config.
	if cw.l.Enabled(ctx, slog.LevelDebug) {
		// Re-hydrate the current config file for later diffing.
		previous := cw.current
		cw.current = string(raw)
		cw.diff(previous, cw.current)
	}

	return cw.rcv.LoadConfig(ctx, cfg)
}

func (cw *configWatcher) diff(oldConfig, newConfig string) {
	if oldConfig == "" {
		return
	}

	oldLines := strings.Split(oldConfig, "\n")
	newLines := strings.Split(newConfig, "\n")

	for i := 0; i < len(oldLines) || i < len(newLines); i++ {
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = strings.TrimSpace(oldLines[i])
		}
		if i < len(newLines) {
			newLine = strings.TrimSpace(newLines[i])
		}

		if oldLine != newLine {
			cw.l.Debug("config line changed", slog.Int("line", i+1), slog.String("path", cw.path), slog.String("old", oldLine), slog.String("new", newLine))
		}
	}
}
