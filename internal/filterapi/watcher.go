// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package filterapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/envoyproxy/ai-gateway/internal/version"
)

// ConfigReceiver is an interface that can receive *Config updates.
// This is mostly for decoupling and testing purposes.
type ConfigReceiver interface {
	// LoadConfig updates the configuration.
	LoadConfig(ctx context.Context, config *Config) error
}

// TODO(huabing): the legacy config watcher can be removed in the next release
type configWatcher struct {
	lastMod    time.Time
	path       string
	rcv        ConfigReceiver
	l          *slog.Logger
	versionStr string
}

type bundleConfigWatcher struct {
	lastMod    time.Time
	path       string
	rcv        ConfigReceiver
	l          *slog.Logger
	versionStr string
}

// StartLegacyConfigWatcher starts a watcher for the given path and Receiver.
// Periodically checks the file for changes and calls the Receiver's UpdateConfig method.
func StartLegacyConfigWatcher(ctx context.Context, path string, rcv ConfigReceiver, l *slog.Logger, tick time.Duration) error {
	cw := &configWatcher{rcv: rcv, l: l, path: path, versionStr: version.Parse()}

	if err := cw.loadConfig(ctx); err != nil {
		return fmt.Errorf("failed to load initial config: %w", err)
	}

	l.Info("start watching the config file", slog.String("path", path), slog.String("interval", tick.String()))
	go cw.watch(ctx, tick)
	return nil
}

// StartConfigBundleWatcher starts a watcher for the sharded bundle directory.
func StartConfigBundleWatcher(ctx context.Context, bundlePath string, rcv ConfigReceiver, l *slog.Logger, tick time.Duration) error {
	cw := &bundleConfigWatcher{rcv: rcv, l: l, path: bundlePath, versionStr: version.Parse()}

	if err := cw.loadConfig(ctx); err != nil {
		// The initial load of the bundle may fail because of race condition
		// when the bundle is being created. We will retry on the next tick.
		if !errors.Is(err, ErrBundleChecksumMismatch) {
			return fmt.Errorf("failed to load initial bundled config: %w", err)
		}
		l.Warn("failed to load initial bundled config; will retry on next watch tick", slog.String("error", err.Error()))
	}

	l.Info("start watching the config bundle", slog.String("path", bundlePath), slog.String("interval", tick.String()))
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

func (cw *bundleConfigWatcher) watch(ctx context.Context, tick time.Duration) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			cw.l.Info("stop watching the config bundle", slog.String("path", cw.path))
			return
		case <-ticker.C:
			perTickCtx, cancel := context.WithTimeout(ctx, tick)
			if err := cw.loadConfig(perTickCtx); err != nil {
				cw.l.Error("failed to update bundled config", slog.String("error", err.Error()))
			}
			cancel()
		}
	}
}

// loadConfig loads a new config from the given path and updates the ConfigReceiver by
// calling the [ConfigReceiver.Load].
func (cw *configWatcher) loadConfig(ctx context.Context) error {
	var cfg *Config
	stat, err := os.Stat(cw.path)
	if err != nil {
		return err
	}

	if stat.ModTime().Sub(cw.lastMod) <= 0 {
		return nil
	}
	cw.l.Info("loading a new config", slog.String("path", cw.path))
	cw.lastMod = stat.ModTime()
	cfg, err = UnmarshalConfigYaml(cw.path)
	if err != nil {
		return err
	}

	if cfg.Version != cw.versionStr {
		return fmt.Errorf(`config version mismatch: expected %q, got %q. Likely in the middle of rolling update`,
			cw.versionStr, cfg.Version)
	}

	if err = cw.rcv.LoadConfig(ctx, cfg); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	return nil
}

func (cw *bundleConfigWatcher) loadConfig(ctx context.Context) error {
	indexPath := filepath.Join(cw.path, ConfigBundleIndexFileName)
	indexRaw, err := os.ReadFile(indexPath)
	if err != nil {
		return err
	}
	indexStat, err := os.Stat(indexPath)
	if err != nil {
		return err
	}
	index, err := UnmarshalConfigBundleIndex(indexRaw)
	if err != nil {
		return err
	}

	maxMod := indexStat.ModTime()
	for i := 0; i < len(index.Parts); i++ {
		partPath := filepath.Join(cw.path, filepath.FromSlash(index.Parts[i].Path))
		partStat, statErr := os.Stat(partPath)
		if statErr != nil {
			return statErr
		}
		if partStat.ModTime().After(maxMod) {
			maxMod = partStat.ModTime()
		}
	}
	if maxMod.Sub(cw.lastMod) <= 0 {
		return nil
	}

	cw.l.Info("loading a new bundled config", slog.String("path", cw.path))
	cfg, err := ReassembleBundleConfig(index, func(part ConfigBundlePart) ([]byte, error) {
		partPath := filepath.Join(cw.path, filepath.FromSlash(part.Path))
		return os.ReadFile(partPath)
	})
	if err != nil {
		return err
	}

	if cfg.Version != cw.versionStr {
		return fmt.Errorf(`config version mismatch: expected %q, got %q. Likely in the middle of rolling update`,
			cw.versionStr, cfg.Version)
	}

	if err = cw.rcv.LoadConfig(ctx, cfg); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	cw.lastMod = maxMod
	return nil
}
