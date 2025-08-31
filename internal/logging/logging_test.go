package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		config      Config
		expectError bool
	}{
		{
			name: "disabled logging",
			config: Config{
				Output: "none",
			},
			expectError: false,
		},
		{
			name: "stdout logging",
			config: Config{
				Output: "stdout",
				Level:  "info",
			},
			expectError: false,
		},
		{
			name: "stderr logging",
			config: Config{
				Output: "stderr",
				Level:  "debug",
			},
			expectError: false,
		},
		{
			name: "directory logging",
			config: Config{
				Output:    "directory",
				Directory: t.TempDir(),
				Level:     "info",
			},
			expectError: false,
		},
		{
			name: "directory logging without directory uses default",
			config: Config{
				Output: "directory",
				Level:  "info",
				// Don't test default directory creation in unit tests
				// as it would create directories outside of temp.
				Directory: t.TempDir(),
			},
			expectError: false, // Uses provided temp directory.
		},
		{
			name: "invalid log level",
			config: Config{
				Output: "stdout",
				Level:  "invalid",
			},
			expectError: false, // Falls back to info.
		},
		{
			name: "mcp logging",
			config: Config{
				Output:    "mcp",
				Level:     "info",
				MCPSender: &mockMCPSender{},
			},
			expectError: false,
		},
		{
			name: "mcp logging without sender",
			config: Config{
				Output: "mcp",
				Level:  "info",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			logger, err := New(tt.config)
			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, logger)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, logger)
				if logger != nil {
					_ = logger.Close()
				}
			}
		})
	}
}

func TestLogger_LogLevels(t *testing.T) {
	t.Parallel()
	// Create a logger with debug level to capture all logs.
	config := Config{
		Output: "buffer",
		Level:  "debug",
	}

	logger, err := New(config)
	require.NoError(t, err)
	defer func() { _ = logger.Close() }()

	// Cast to access the buffer.
	bufLogger, ok := logger.(*bufferLogger)
	require.True(t, ok)

	// Log messages at different levels.
	logger.Debug("debug message", "key", "value")
	logger.Info("info message", "key", "value")
	logger.Warn("warn message", "key", "value")
	logger.Error("error message", "key", "value")

	// Check that all messages were logged.
	output := bufLogger.String()
	assert.Contains(t, output, "debug message")
	assert.Contains(t, output, "info message")
	assert.Contains(t, output, "warn message")
	assert.Contains(t, output, "error message")
}

func TestLogger_LogLevelFiltering(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		level       string
		expectDebug bool
		expectInfo  bool
		expectWarn  bool
		expectError bool
	}{
		{
			name:        "debug level",
			level:       "debug",
			expectDebug: true,
			expectInfo:  true,
			expectWarn:  true,
			expectError: true,
		},
		{
			name:        "info level",
			level:       "info",
			expectDebug: false,
			expectInfo:  true,
			expectWarn:  true,
			expectError: true,
		},
		{
			name:        "warn level",
			level:       "warn",
			expectDebug: false,
			expectInfo:  false,
			expectWarn:  true,
			expectError: true,
		},
		{
			name:        "error level",
			level:       "error",
			expectDebug: false,
			expectInfo:  false,
			expectWarn:  false,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := Config{
				Output: "buffer",
				Level:  tt.level,
			}

			logger, err := New(config)
			require.NoError(t, err)
			defer func() { _ = logger.Close() }()

			bufLogger, ok := logger.(*bufferLogger)
			require.True(t, ok)

			// Log at all levels.
			logger.Debug("debug message")
			logger.Info("info message")
			logger.Warn("warn message")
			logger.Error("error message")

			output := bufLogger.String()

			if tt.expectDebug {
				assert.Contains(t, output, "debug message")
			} else {
				assert.NotContains(t, output, "debug message")
			}

			if tt.expectInfo {
				assert.Contains(t, output, "info message")
			} else {
				assert.NotContains(t, output, "info message")
			}

			if tt.expectWarn {
				assert.Contains(t, output, "warn message")
			} else {
				assert.NotContains(t, output, "warn message")
			}

			if tt.expectError {
				assert.Contains(t, output, "error message")
			} else {
				assert.NotContains(t, output, "error message")
			}
		})
	}
}

func TestDirectoryLogger(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	config := Config{
		Output:    "directory",
		Directory: tempDir,
		Level:     "info",
	}

	logger, err := New(config)
	require.NoError(t, err)
	defer func() { _ = logger.Close() }()

	// Log some messages.
	logger.Info("test message 1")
	logger.Info("test message 2")

	// Check that log file was created.
	logFile := filepath.Join(tempDir, "lgtmcp.log")
	assert.FileExists(t, logFile)

	// Read the log file.
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "test message 1")
	assert.Contains(t, string(content), "test message 2")
}

func TestDirectoryLogger_LargeMessages(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	config := Config{
		Output:    "directory",
		Directory: tempDir,
		Level:     "info",
	}

	logger, err := New(config)
	require.NoError(t, err)
	defer func() { _ = logger.Close() }()

	// Log a large message.
	largeMessage := strings.Repeat("This is a large message for testing. ", 100)
	logger.Info(largeMessage)

	// Check that log file was created.
	logFile := filepath.Join(tempDir, "lgtmcp.log")
	assert.FileExists(t, logFile)

	// Read the log file.
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "This is a large message")
}

func TestMCPLogger(t *testing.T) {
	t.Parallel()
	sender := &mockMCPSender{}

	config := Config{
		Output:    "mcp",
		Level:     "info",
		MCPSender: sender,
	}

	logger, err := New(config)
	require.NoError(t, err)
	defer func() { _ = logger.Close() }()

	// Log messages.
	logger.Info("test info", "key", "value")
	logger.Error("test error", "key", "value")
	// Test odd number of arguments - the unpaired arg should be ignored
	logger.Warn("test warn", "key1", "value1", "unpaired")

	// Check that messages were sent.
	assert.Len(t, sender.messages, 3)
	assert.Contains(t, sender.messages[0], "test info")
	assert.Contains(t, sender.messages[0], "key=value")
	assert.Contains(t, sender.messages[1], "test error")
	assert.Contains(t, sender.messages[1], "key=value")
	assert.Contains(t, sender.messages[2], "test warn")
	assert.Contains(t, sender.messages[2], "key1=value1")
	// The unpaired argument should be ignored
	assert.NotContains(t, sender.messages[2], "unpaired")
}

func TestNopLogger(t *testing.T) {
	t.Parallel()
	config := Config{
		Output: "none",
	}

	logger, err := New(config)
	require.NoError(t, err)
	defer func() { _ = logger.Close() }()

	// These should not panic.
	logger.Debug("debug")
	logger.Info("info")
	logger.Warn("warn")
	logger.Error("error")
}

func TestWith(t *testing.T) {
	t.Parallel()

	t.Run("buffer logger", func(t *testing.T) {
		t.Parallel()
		config := Config{
			Output: "buffer",
			Level:  "info",
		}

		logger, err := New(config)
		require.NoError(t, err)
		defer func() { _ = logger.Close() }()

		// Create context logger.
		ctxLogger := logger.With("request_id", "123")

		// Log with context.
		ctxLogger.Info("test message")

		// Check output.
		bufLogger, ok := logger.(*bufferLogger)
		require.True(t, ok)
		output := bufLogger.String()
		assert.Contains(t, output, "test message")
		assert.Contains(t, output, "request_id")
		assert.Contains(t, output, "123")
	})

	t.Run("mcp logger", func(t *testing.T) {
		t.Parallel()
		sender := &mockMCPSender{}

		config := Config{
			Output:    "mcp",
			Level:     "info",
			MCPSender: sender,
		}

		logger, err := New(config)
		require.NoError(t, err)
		defer func() { _ = logger.Close() }()

		// Create context logger with multiple attributes.
		ctxLogger := logger.With("request_id", "456", "user", "alice")

		// Log with context.
		ctxLogger.Info("operation completed", "status", "success")

		// Check that context was included in the message.
		require.Len(t, sender.messages, 1)
		msg := sender.messages[0]
		assert.Contains(t, msg, "operation completed")
		assert.Contains(t, msg, "request_id=456")
		assert.Contains(t, msg, "user=alice")
		assert.Contains(t, msg, "status=success")
	})
}

// Mock MCP sender for testing.
type mockMCPSender struct {
	messages []string
}

func (m *mockMCPSender) SendLog(level, message string) error {
	_ = level // Explicitly unused.
	m.messages = append(m.messages, message)

	return nil
}
