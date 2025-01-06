package extproc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
)

// ConfigReceiver is an interface that can receive *extprocconfig.Config updates.
type ConfigReceiver interface {
	// LoadConfig updates the configuration.
	LoadConfig(config *extprocconfig.Config) error
}

type configWatcher struct {
	lastMod time.Time
	path    string
	rcv     ConfigReceiver
	l       *slog.Logger
}

// StartConfigWatcher starts a watcher for the given path and Receiver.
// Periodically checks the file for changes and calls the Receiver's UpdateConfig method.
func StartConfigWatcher(ctx context.Context, path string, rcv ConfigReceiver, l *slog.Logger, tick time.Duration) error {
	cw := &configWatcher{rcv: rcv, l: l, path: path}

	if err := cw.loadConfig(); err != nil {
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
			if err := cw.loadConfig(); err != nil {
				cw.l.Error("failed to update config", slog.String("error", err.Error()))
			}
		}
	}
}

// loadConfig loads a new config from the given path and updates the Receiver by
// calling the [Receiver.Load].
func (cw *configWatcher) loadConfig() error {
	stat, err := os.Stat(cw.path)
	if err != nil {
		return err
	}
	if stat.ModTime().Sub(cw.lastMod) <= 0 {
		return nil
	}
	cw.lastMod = stat.ModTime()
	cw.l.Info("loading a new config", slog.String("path", cw.path))
	cfg, err := extprocconfig.UnmarshalConfigYaml(cw.path)
	if err != nil {
		return err
	}
	return cw.rcv.LoadConfig(cfg)
}
