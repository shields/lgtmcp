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

// Package logging provides configurable logging for LGTMCP.
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	cfgpkg "msrl.dev/lgtmcp/internal/config"
)

// ErrMCPSenderRequired is returned when MCP logging is requested without a sender.
var ErrMCPSenderRequired = errors.New("MCP sender required for MCP logging")

// ErrStdoutNotAllowed is returned when stdout logging is requested. The server
// speaks MCP over stdio, so anything else written to stdout corrupts the
// protocol stream.
var ErrStdoutNotAllowed = errors.New(
	`logging output "stdout" would corrupt the MCP stdio transport; use "stderr" or "directory"`,
)

// ErrUnknownOutput is returned when the configured logging output is not one of
// the recognized values. A typo'd output is a configuration error and fails
// fast at startup rather than silently falling back to directory logging.
var ErrUnknownOutput = errors.New(
	`unknown logging output; valid values are "none", "stderr", "directory", "mcp"`,
)

// ErrUnknownLevel is returned when the configured logging level is not one of
// the recognized values. A typo'd level is a configuration error and fails
// fast at startup rather than silently logging at info.
var ErrUnknownLevel = errors.New(
	`unknown logging level; valid values are "debug", "info", "warn", "error"`,
)

// Config represents logging configuration.
type Config struct {
	// Level is the minimum log level (debug, info, warn, error).
	Level string `json:"level"`

	// Output specifies where logs should be written:
	// - "none": Disable logging
	// - "stderr": Write to standard error
	// - "directory": Write to files in specified directory (default)
	// - "mcp": Send logs to the MCP client as notifications/message (requires
	//   MCPSender; the server binary wires up one backed by the MCP transport)
	// "stdout" is rejected with [ErrStdoutNotAllowed]: stdout carries the
	// MCP stdio protocol, so logging there would corrupt the transport.
	Output string `json:"output"`

	// Directory is the directory for log files (when Output is "directory").
	Directory string `json:"directory,omitempty"`

	// MCPSender is used to send logs to MCP client (when Output is "mcp").
	MCPSender MCPLogSender `json:"-"`

	// ConfigDir is the lgtmcp config directory used to validate Directory
	// when it is set. When empty, [cfgpkg.Dir] is used. Tests may set
	// it to a temporary directory; production code should leave it empty.
	ConfigDir string `json:"-"`
}

// MCPLogSender interface for sending logs to MCP client.
type MCPLogSender interface {
	SendLog(level, message string) error
}

// Logger interface for structured logging.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	With(args ...any) Logger
	Close() error
}

// New creates a new logger based on configuration. An empty Output defaults to
// directory logging and an empty Level defaults to info; any other unrecognized
// Output or Level is a configuration error and returns [ErrUnknownOutput] /
// [ErrUnknownLevel] rather than silently falling back.
func New(config Config) (Logger, error) {
	if err := validateLevel(config.Level); err != nil {
		return nil, err
	}

	switch config.Output {
	case "none":
		return &nopLogger{}, nil
	case "stdout":
		return nil, ErrStdoutNotAllowed
	case "stderr":
		return newStdLogger(os.Stderr, config.Level), nil
	case "mcp":
		return newMCPLogger(config)
	case "", "directory":
		return newDirectoryLogger(config)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownOutput, config.Output)
	}
}

// validateLevel reports whether level is a recognized slog level name. An empty
// level is accepted and resolves to info (see [parseLevel]).
func validateLevel(level string) error {
	switch level {
	case "", "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnknownLevel, level)
	}
}

// standardLogger implements Logger using slog.
type standardLogger struct {
	logger *slog.Logger
	closer io.Closer
}

func newStdLogger(w io.Writer, level string) *standardLogger {
	return &standardLogger{
		logger: slog.New(newTextHandler(w, level)),
	}
}

func newDirectoryLogger(config Config) (Logger, error) {
	// Use the standard platform-specific user log location if no directory specified.
	logDir := config.Directory
	if logDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}

		// Use platform-specific default log directory.
		switch runtime.GOOS {
		case "darwin":
			// macOS: ~/Library/Logs/lgtmcp/
			logDir = filepath.Join(home, "Library", "Logs", "lgtmcp")
		case "windows":
			// Windows: %LOCALAPPDATA%\lgtmcp\logs\
			if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
				logDir = filepath.Join(localAppData, "lgtmcp", "logs")
			} else {
				logDir = filepath.Join(home, "AppData", "Local", "lgtmcp", "logs")
			}
		default:
			// Linux and others: ~/.local/share/lgtmcp/logs/. The XDG spec
			// requires absolute paths in these variables; ignore relative
			// values rather than resolving them against the working directory.
			dataHome := os.Getenv("XDG_DATA_HOME")
			if !filepath.IsAbs(dataHome) {
				dataHome = filepath.Join(home, ".local", "share")
			}
			logDir = filepath.Join(dataHome, "lgtmcp", "logs")
		}
	} else {
		// Reject traversal and absolute paths outside the lgtmcp config
		// directory so that a compromised config cannot drop log files at
		// arbitrary filesystem locations.
		base := config.ConfigDir
		if base == "" {
			dir, err := cfgpkg.Dir()
			if err != nil {
				return nil, fmt.Errorf("cannot determine log directory base: %w", err)
			}
			base = dir
		}
		safeDir, err := cfgpkg.ValidatePathIn(logDir, base)
		if err != nil {
			return nil, fmt.Errorf("invalid log directory: %w", err)
		}
		logDir = safeDir
	}

	if err := os.MkdirAll(logDir, 0o750); err != nil { //nolint:gosec // Path validated above
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create or open log file.
	filename := filepath.Join(logDir, "lgtmcp.log")
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // Path validated above
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	return &standardLogger{
		logger: slog.New(newTextHandler(file, config.Level)),
		closer: file,
	}, nil
}

func newMCPLogger(config Config) (Logger, error) {
	if config.MCPSender == nil {
		return nil, ErrMCPSenderRequired
	}

	return &mcpLogger{
		sender:  config.MCPSender,
		level:   parseLevel(config.Level),
		context: nil,
	}, nil
}

func newTextHandler(w io.Writer, level string) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: parseLevel(level),
	})
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "info":
		fallthrough
	default:
		return slog.LevelInfo
	}
}

// formatMessage formats a message with key-value pairs for MCP client.
func formatMessage(msg string, args ...any) string {
	if len(args) == 0 {
		return msg
	}

	var pairs []string
	for i := 0; i < len(args); i += 2 {
		if i+1 < len(args) {
			key := fmt.Sprintf("%v", args[i])
			value := fmt.Sprintf("%v", args[i+1])
			pairs = append(pairs, fmt.Sprintf("%s=%s", key, value))
		}
		// Ignore a trailing unpaired argument (slog instead reports it
		// under a !BADKEY attribute; dropping it is fine for MCP logs).
	}

	if len(pairs) > 0 {
		return fmt.Sprintf("%s [%s]", msg, strings.Join(pairs, " "))
	}

	return msg
}

func (l *standardLogger) Debug(msg string, args ...any) {
	l.logger.Debug(msg, args...)
}

func (l *standardLogger) Info(msg string, args ...any) {
	l.logger.Info(msg, args...)
}

func (l *standardLogger) Warn(msg string, args ...any) {
	l.logger.Warn(msg, args...)
}

func (l *standardLogger) Error(msg string, args ...any) {
	l.logger.Error(msg, args...)
}

func (l *standardLogger) With(args ...any) Logger {
	// Create a new logger with additional context.
	// Don't share the closer - only the original logger should close resources.
	return &standardLogger{
		logger: l.logger.With(args...),
	}
}

func (l *standardLogger) Close() error {
	if l.closer != nil {
		return l.closer.Close()
	}

	return nil
}

// mcpLogger sends logs to MCP client.
type mcpLogger struct {
	sender  MCPLogSender
	level   slog.Level
	context []any // Store context key-value pairs
}

func (m *mcpLogger) Debug(msg string, args ...any) {
	if m.level > slog.LevelDebug {
		return
	}
	// Best effort logging to MCP client.
	//nolint:errcheck // MCP logging is best-effort
	_ = m.sender.SendLog("debug", formatMessage(msg, append(m.context, args...)...))
}

func (m *mcpLogger) Info(msg string, args ...any) {
	if m.level > slog.LevelInfo {
		return
	}
	// Best effort logging to MCP client.
	//nolint:errcheck // MCP logging is best-effort
	_ = m.sender.SendLog("info", formatMessage(msg, append(m.context, args...)...))
}

func (m *mcpLogger) Warn(msg string, args ...any) {
	if m.level > slog.LevelWarn {
		return
	}
	// Best effort logging to MCP client.
	//nolint:errcheck // MCP logging is best-effort
	_ = m.sender.SendLog("warn", formatMessage(msg, append(m.context, args...)...))
}

func (m *mcpLogger) Error(msg string, args ...any) {
	if m.level > slog.LevelError {
		return
	}
	// Best effort logging to MCP client.
	//nolint:errcheck // MCP logging is best-effort
	_ = m.sender.SendLog("error", formatMessage(msg, append(m.context, args...)...))
}

func (m *mcpLogger) With(args ...any) Logger {
	// Create a new slice to prevent aliasing the parent's context slice.
	newContext := make([]any, 0, len(m.context)+len(args))
	newContext = append(newContext, m.context...)
	newContext = append(newContext, args...)

	return &mcpLogger{
		sender:  m.sender,
		level:   m.level,
		context: newContext,
	}
}

func (*mcpLogger) Close() error {
	return nil
}

// nopLogger is a no-op logger for when logging is disabled.
type nopLogger struct{}

// bufferLogger for testing purposes.
type bufferLogger struct {
	*standardLogger

	buffer *strings.Builder
}

func (b *bufferLogger) String() string {
	return b.buffer.String()
}

func newBufferLogger(level string) *bufferLogger {
	buf := &strings.Builder{}

	return &bufferLogger{
		standardLogger: newStdLogger(buf, level),
		buffer:         buf,
	}
}

func (*nopLogger) Debug(_ string, _ ...any) {}
func (*nopLogger) Info(_ string, _ ...any)  {}
func (*nopLogger) Warn(_ string, _ ...any)  {}
func (*nopLogger) Error(_ string, _ ...any) {}
func (n *nopLogger) With(_ ...any) Logger {
	return n
}

func (*nopLogger) Close() error {
	return nil
}
