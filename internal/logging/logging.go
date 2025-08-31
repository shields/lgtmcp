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
)

// ErrMCPSenderRequired is returned when MCP logging is requested without a sender.
var ErrMCPSenderRequired = errors.New("MCP sender required for MCP logging")

// Config represents logging configuration.
type Config struct {
	// Level is the minimum log level (debug, info, warn, error).
	Level string `json:"level"`

	// Output specifies where logs should be written:
	// - "none": Disable logging
	// - "stdout": Write to standard output
	// - "stderr": Write to standard error
	// - "directory": Write to files in specified directory (default)
	// - "mcp": Send logs to MCP client
	// - "buffer": Internal use for testing.
	Output string `json:"output"`

	// Directory is the directory for log files (when Output is "directory").
	Directory string `json:"directory,omitempty"`

	// MCPSender is used to send logs to MCP client (when Output is "mcp").
	MCPSender MCPLogSender `json:"-"`
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

// New creates a new logger based on configuration.
// If Output is empty or unrecognized, it defaults to directory logging.
func New(config Config) (Logger, error) {
	switch config.Output {
	case "none":
		return &nopLogger{}, nil
	case "stdout":
		return newStdLogger(os.Stdout, config.Level)
	case "stderr":
		return newStdLogger(os.Stderr, config.Level)
	case "directory", "": // Default to directory for empty string
		return newDirectoryLogger(config)
	case "mcp":
		return newMCPLogger(config)
	case "buffer":
		// For testing.
		return newBufferLogger(config.Level)
	default:
		// Default to directory logging for unrecognized output types.
		return newDirectoryLogger(config)
	}
}

// standardLogger implements Logger using slog.
type standardLogger struct {
	handler slog.Handler
	logger  *slog.Logger
	closer  io.Closer
}

func newStdLogger(w io.Writer, level string) (Logger, error) {
	handler := newTextHandler(w, level)

	return &standardLogger{
		handler: handler,
		logger:  slog.New(handler),
		closer:  io.NopCloser(nil),
	}, nil
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
			// Linux and others: ~/.local/share/lgtmcp/logs/
			dataHome := os.Getenv("XDG_DATA_HOME")
			if dataHome == "" {
				dataHome = filepath.Join(home, ".local", "share")
			}
			logDir = filepath.Join(dataHome, "lgtmcp", "logs")
		}
	}

	// Ensure directory exists.
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create or open log file.
	filename := filepath.Join(logDir, "lgtmcp.log")
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // Safe path construction
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	handler := newTextHandler(file, config.Level)

	return &standardLogger{
		handler: handler,
		logger:  slog.New(handler),
		closer:  file,
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
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
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
		// Ignore unpaired argument (matches slog behavior)
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
		handler: l.handler,
		logger:  l.logger.With(args...),
		closer:  nil,
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

func newBufferLogger(level string) (Logger, error) {
	buf := &strings.Builder{}
	handler := newTextHandler(buf, level)

	return &bufferLogger{
		standardLogger: &standardLogger{
			handler: handler,
			logger:  slog.New(handler),
			closer:  io.NopCloser(nil),
		},
		buffer: buf,
	}, nil
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
