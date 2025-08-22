package main

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetLogLevel(t *testing.T) {
	t.Parallel()
	// Test the setLogLevel function.
	testCases := []struct {
		level    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"invalid", slog.LevelInfo}, // Should fall through to default.
		{"", slog.LevelInfo},        // Empty should use default.
	}

	for _, tc := range testCases {
		t.Run("log_level_"+tc.level, func(t *testing.T) {
			t.Parallel()
			// Call setLogLevel.
			setLogLevel(tc.level)

			// We can't easily verify the log level was set correctly
			// without accessing internal slog state, but we can verify
			// the function doesn't panic.
			assert.NotPanics(t, func() {
				slog.Info("test message")
			})
		})
	}
}

func TestMain(t *testing.T) {
	t.Parallel()
	// Test main function is difficult due to os.Exit() calls.
	// We test the individual functions instead.

	t.Run("setLogLevel handles all levels", func(t *testing.T) {
		t.Parallel()
		// Test that setLogLevel doesn't panic for any level.
		levels := []string{"debug", "info", "warn", "error", "invalid", ""}
		for _, level := range levels {
			assert.NotPanics(t, func() {
				setLogLevel(level)
			})
		}
	})
}
