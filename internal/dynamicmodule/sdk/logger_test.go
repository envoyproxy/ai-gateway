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

func newLogFuncWithBuffer(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	var logOutput bytes.Buffer
	h := &handler{logFunc: func(slevel slog.Level, message string) {
		logOutput.WriteString("[" + slevel.String() + "] " + message + "\n")
	}}
	logger := slog.New(h)
	return logger, &logOutput
}

func TestHandler(t *testing.T) {
	l, buf := newLogFuncWithBuffer(t)
	require.True(t, l.Handler().Enabled(nil, slog.LevelInfo))
	require.False(t, l.Handler().Enabled(nil, slog.LevelDebug-1))

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
