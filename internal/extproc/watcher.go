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
	lastMod         time.Time
	path            string
	rcv             ConfigReceiver
	l               *slog.Logger
	usingDefaultCfg bool
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
			perTickCtx, cancel := context.WithTimeout(ctx, tick)
			if err := cw.loadConfig(perTickCtx); err != nil {
				cw.l.Error("failed to update config", slog.String("error", err.Error()))
			}
			cancel()
		}
	}
}

// loadConfig loads a new config from the given path and updates the Receiver by
// calling the [Receiver.Load].
func (cw *configWatcher) loadConfig(ctx context.Context) error {
	var cfg *filterapi.Config
	stat, err := os.Stat(cw.path)
	switch {
	case err != nil && os.IsNotExist(err):
		// If the file does not exist, do not fail (which could lead to the extproc process to terminate).
		// Instead, load the default configuration and keep running unconfigured.
		cfg = filterapi.MustLoadDefaultConfig()
	case err != nil:
		return err
	}

	if cfg != nil {
		if cw.usingDefaultCfg { // Do not re-reload the same thing on every tick.
			return nil
		}
		cw.l.Info("config file does not exist; loading default config", slog.String("path", cw.path))
		cw.lastMod = time.Now()
		cw.usingDefaultCfg = true
	} else {
		cw.usingDefaultCfg = false
		if stat.ModTime().Sub(cw.lastMod) <= 0 {
			return nil
		}
		cw.l.Info("loading a new config", slog.String("path", cw.path))
		cw.lastMod = stat.ModTime()
		cfg, err = filterapi.UnmarshalConfigYaml(cw.path)
		if err != nil {
			return err
		}
	}

	if err = cw.rcv.LoadConfig(ctx, cfg); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	return nil
}
