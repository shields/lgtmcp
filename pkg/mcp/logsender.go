// Copyright © 2026 Michael Shields
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

package mcp

import (
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"msrl.dev/lgtmcp/internal/logging"
)

// LogSender adapts the MCP server's notifications/message capability to the
// logging.MCPLogSender interface used by the "mcp" logging output.
//
// The application logger is constructed before the MCP server (the server
// takes the logger as a dependency), so the sender is created unbound, handed
// to the logger, and then bound to the server once it exists — "lazy
// injection". Until Bind is called, SendLog drops messages because there is no
// transport to carry them yet.
type LogSender struct {
	mu        sync.RWMutex
	mcpServer *server.MCPServer
}

var _ logging.MCPLogSender = (*LogSender)(nil)

// NewLogSender returns an unbound LogSender. Call Bind once the MCP server is
// available.
func NewLogSender() *LogSender {
	return &LogSender{}
}

// Bind attaches the MCP server that notifications are sent through. It is safe
// to call concurrently with SendLog.
func (l *LogSender) Bind(s *server.MCPServer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.mcpServer = s
}

// SendLog emits a notifications/message log notification to every initialized
// client (a stdio server has exactly one). Before Bind it is a no-op. The
// configured logger level already gates which records reach here; per-client
// logging/setLevel filtering is not applied, because the logging.Logger
// interface carries no request context from which to resolve the session.
func (l *LogSender) SendLog(level, message string) error {
	l.mu.RLock()
	s := l.mcpServer
	l.mu.RUnlock()
	if s == nil {
		return nil
	}

	s.SendNotificationToAllClients(string(mcp.MethodNotificationMessage), map[string]any{
		"level":  mcpLogLevel(level),
		"logger": "lgtmcp",
		"data":   message,
	})

	return nil
}

// mcpLogLevel maps the logger's slog-style level names to MCP logging levels.
// They coincide except that slog "warn" is MCP "warning"; an unrecognized value
// passes through unchanged.
func mcpLogLevel(level string) mcp.LoggingLevel {
	switch level {
	case "debug":
		return mcp.LoggingLevelDebug
	case "info":
		return mcp.LoggingLevelInfo
	case "warn":
		return mcp.LoggingLevelWarning
	case "error":
		return mcp.LoggingLevelError
	default:
		return mcp.LoggingLevel(level)
	}
}
