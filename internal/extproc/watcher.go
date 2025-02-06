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
	stat, err := os.Stat(cw.path)
	if err != nil {
		return err
	}
	if stat.ModTime().Sub(cw.lastMod) <= 0 {
		return nil
	}
	cw.lastMod = stat.ModTime()
	cw.l.Info("loading a new config", slog.String("path", cw.path))

	// Print the diff between the old and new config.
	if cw.l.Enabled(ctx, slog.LevelDebug) {
		// Re-hydrate the current config file for later diffing.
		previous := cw.current
		cw.current, err = cw.getConfigString()
		if err != nil {
			return fmt.Errorf("failed to read the config file: %w", err)
		}

		cw.diff(previous, cw.current)
	}

	cfg, err := filterapi.UnmarshalConfigYaml(cw.path)
	if err != nil {
		return err
	}
	return cw.rcv.LoadConfig(ctx, cfg)
}

// getConfigString gets a string representation of the current config
// read from the path. This is only used for debug log path for diff prints.
func (cw *configWatcher) getConfigString() (string, error) {
	currentByte, err := os.ReadFile(cw.path)
	if err != nil {
		return "", err
	}
	return string(currentByte), nil
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
