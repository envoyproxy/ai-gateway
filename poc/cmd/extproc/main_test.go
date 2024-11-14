package main

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		logLevel      string
		expectedLevel slog.Level
	}{
		{
			logLevel:      "debug",
			expectedLevel: slog.LevelDebug,
		},
		{
			logLevel:      "DEBUG",
			expectedLevel: slog.LevelDebug,
		},
		{
			logLevel:      "info",
			expectedLevel: slog.LevelInfo,
		},
		{
			logLevel:      "INFO",
			expectedLevel: slog.LevelInfo,
		},
	}

	for _, tc := range cases {
		t.Run(tc.logLevel, func(t *testing.T) {
			var l slog.Level
			require.NoError(t, l.UnmarshalText([]byte(tc.logLevel)))
			require.Equal(t, tc.expectedLevel, l)
		})
	}
}
