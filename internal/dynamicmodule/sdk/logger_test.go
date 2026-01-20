// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package sdk

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSlogLogger(t *testing.T) {
	s := NewSlogLogger()
	require.NotNil(t, s)
}

func newLogFuncWithBuffer() (*slog.Logger, *bytes.Buffer) {
	var logOutput bytes.Buffer
	h := &handler{logFunc: func(slevel slog.Level, message string) {
		logOutput.WriteString("[" + slevel.String() + "] " + message + "\n")
	}}
	logger := slog.New(h)
	return logger, &logOutput
}

func TestHandler(t *testing.T) {
	l, buf := newLogFuncWithBuffer()
	require.True(t, l.Handler().Enabled(t.Context(), slog.LevelInfo))
	require.False(t, l.Handler().Enabled(t.Context(), slog.LevelDebug-1))

	l.Debug("test")
	require.Equal(t, "[DEBUG] test\n", buf.String())
	buf.Reset()

	l = l.WithGroup("mygroup")
	l.Info("info message", slog.String("key", "value"))
	require.Equal(t, "[INFO] info message mygroup.key=value\n", buf.String())

	l = l.With(slog.String("key", "value"), slog.GroupAttrs("aaa"))
	buf.Reset()
	l.Warn("warn message", slog.Int("number", 42))
	require.Equal(t, "[WARN] warn message mygroup.key=value mygroup.aaa=[] mygroup.number=42\n", buf.String())
}
