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
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"msrl.dev/lgtmcp/internal/config"
	"msrl.dev/lgtmcp/internal/logging"
)

func TestLogSender_UnboundDropsWithoutError(t *testing.T) {
	t.Parallel()
	ls := NewLogSender()
	// No server bound yet: SendLog must be a safe no-op, not a panic or error.
	require.NoError(t, ls.SendLog("info", "before binding"))
}

func TestLogSender_BoundSendsWithoutError(t *testing.T) {
	t.Parallel()
	srv := mcpsrv.NewMCPServer("test", "v0", mcpsrv.WithLogging())
	ls := NewLogSender()
	ls.Bind(srv)
	// With no connected client the broadcast reaches nobody, but it must still
	// succeed for every level the logger emits.
	for _, level := range []string{"debug", "info", "warn", "error"} {
		require.NoError(t, ls.SendLog(level, "message"))
	}
}

func TestMCPLogLevel(t *testing.T) {
	t.Parallel()
	cases := map[string]mcp.LoggingLevel{
		"debug":   mcp.LoggingLevelDebug,
		"info":    mcp.LoggingLevelInfo,
		"warn":    mcp.LoggingLevelWarning, // slog "warn" -> MCP "warning"
		"error":   mcp.LoggingLevelError,
		"unknown": mcp.LoggingLevel("unknown"),
	}
	for in, want := range cases {
		assert.Equal(t, want, mcpLogLevel(in), "level %q", in)
	}
}

// TestBindLogSender_EndToEnd wires a logger configured for "mcp" output to a
// server through the lazy-injection path and logs through it, exercising the
// full chain without a connected client.
func TestBindLogSender_EndToEnd(t *testing.T) {
	t.Parallel()
	cfg := config.NewTestConfig()
	cfg.Logging.Output = "mcp"

	ls := NewLogSender()
	logger, err := logging.New(logging.Config{Output: "mcp", MCPSender: ls})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, logger.Close())
	})

	server, err := New(cfg, logger)
	if err != nil {
		t.Skip("cannot create server for testing - likely missing credentials")
	}
	server.BindLogSender(ls)

	// Routes through the bound server; a no-op without a client, but it must
	// not error or panic.
	logger.Info("hello", "key", "value")
	logger.Warn("careful")
}
