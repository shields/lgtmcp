// Copyright © 2025 Michael Shields
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
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
					logger.Close() //nolint:errcheck // Test cleanup
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
	defer logger.Close() //nolint:errcheck // Test cleanup

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
			defer logger.Close() //nolint:errcheck // Test cleanup

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
	defer logger.Close() //nolint:errcheck // Test cleanup

	// Log some messages.
	logger.Info("test message 1")
	logger.Info("test message 2")

	// Check that log file was created.
	logFile := filepath.Join(tempDir, "lgtmcp.log")
	assert.FileExists(t, logFile)

	// Read the log file.
	content, err := os.ReadFile(logFile) //nolint:gosec // Test file path is safe
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
	defer logger.Close() //nolint:errcheck // Test cleanup

	// Log a large message.
	largeMessage := strings.Repeat("This is a large message for testing. ", 100)
	logger.Info(largeMessage)

	// Check that log file was created.
	logFile := filepath.Join(tempDir, "lgtmcp.log")
	assert.FileExists(t, logFile)

	// Read the log file.
	content, err := os.ReadFile(logFile) //nolint:gosec // Test file path is safe
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
	defer logger.Close() //nolint:errcheck // Test cleanup

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
	defer logger.Close() //nolint:errcheck // Test cleanup

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
		defer logger.Close() //nolint:errcheck // Test cleanup

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
		defer logger.Close() //nolint:errcheck // Test cleanup

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

func TestNopLogger_AllMethods(t *testing.T) {
	t.Parallel()
	logger := &nopLogger{}
	logger.Debug("debug")
	logger.Info("info")
	logger.Warn("warn")
	logger.Error("error")
	child := logger.With("key", "value")
	assert.NotNil(t, child)
	assert.Equal(t, logger, child)
	assert.NoError(t, logger.Close())
}

func TestMCPLogger_DebugLevel(t *testing.T) {
	t.Parallel()
	sender := &mockMCPSender{}
	logger, err := New(Config{
		Output:    "mcp",
		Level:     "debug",
		MCPSender: sender,
	})
	require.NoError(t, err)
	defer logger.Close() //nolint:errcheck // Test cleanup

	logger.Debug("debug message")
	require.Len(t, sender.messages, 1)
	assert.Contains(t, sender.messages[0], "debug message")
}

func TestMCPLogger_LevelFiltering(t *testing.T) {
	t.Parallel()
	sender := &mockMCPSender{}
	logger, err := New(Config{
		Output:    "mcp",
		Level:     "error",
		MCPSender: sender,
	})
	require.NoError(t, err)
	defer logger.Close() //nolint:errcheck // Test cleanup

	logger.Debug("filtered debug")
	logger.Info("filtered info")
	logger.Warn("filtered warn")
	assert.Empty(t, sender.messages)

	logger.Error("passed error")
	require.Len(t, sender.messages, 1)
	assert.Contains(t, sender.messages[0], "passed error")
}

func TestStandardLogger_ChildClose(t *testing.T) {
	t.Parallel()
	config := Config{
		Output: "buffer",
		Level:  "info",
	}
	logger, err := New(config)
	require.NoError(t, err)
	defer logger.Close() //nolint:errcheck // Test cleanup

	child := logger.With("key", "value")
	assert.NoError(t, child.Close())
}

func TestFormatMessage_EmptyArgs(t *testing.T) {
	t.Parallel()
	result := formatMessage("hello world")
	assert.Equal(t, "hello world", result)
}

func TestDirectoryLogger_MkdirAllError(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	// Create a file that blocks directory creation.
	blocker := filepath.Join(tmpDir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))

	_, err := New(Config{
		Output:    "directory",
		Directory: filepath.Join(blocker, "subdir"),
		Level:     "info",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create log directory")
}

func TestDirectoryLogger_OpenFileError(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	require.NoError(t, os.MkdirAll(logDir, 0o750))

	// Create lgtmcp.log as a directory so OpenFile fails.
	logFile := filepath.Join(logDir, "lgtmcp.log")
	require.NoError(t, os.MkdirAll(logFile, 0o750))

	_, err := New(Config{
		Output:    "directory",
		Directory: logDir,
		Level:     "info",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open log file")
}

func TestDirectoryLogger_DefaultPath(t *testing.T) {
	// Don't parallelize — modifies HOME env.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	// Clear XDG vars so the production code uses its defaults.
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("LOCALAPPDATA", "")

	logger, err := New(Config{
		Output: "directory",
		Level:  "info",
	})
	require.NoError(t, err)
	defer logger.Close() //nolint:errcheck // Test cleanup

	var logFile string
	switch runtime.GOOS {
	case "darwin":
		logFile = filepath.Join(tmpHome, "Library", "Logs", "lgtmcp", "lgtmcp.log")
	case "windows":
		logFile = filepath.Join(tmpHome, "AppData", "Local", "lgtmcp", "logs", "lgtmcp.log")
	default:
		logFile = filepath.Join(tmpHome, ".local", "share", "lgtmcp", "logs", "lgtmcp.log")
	}
	assert.FileExists(t, logFile)
}

func TestMCPLogger_ErrorLevelFiltering(t *testing.T) {
	t.Parallel()
	sender := &mockMCPSender{}
	// Create logger above error level so Error is also filtered.
	m := &mcpLogger{
		sender: sender,
		level:  slog.LevelError + 1,
	}
	m.Error("should be filtered")
	assert.Empty(t, sender.messages)
}

func TestFormatMessage_SingleUnpairedArg(t *testing.T) {
	t.Parallel()
	result := formatMessage("msg", "lonely")
	assert.Equal(t, "msg", result)
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
