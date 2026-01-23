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

// Package progress provides progress reporting for MCP operations.
package progress

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Reporter defines the interface for reporting progress during operations.
type Reporter interface {
	// Report sends a progress notification with the given progress value,
	// optional total, and message.
	Report(ctx context.Context, progress float64, total float64, message string)
}

// MCPReporter implements Reporter using MCP progress notifications.
type MCPReporter struct {
	server        *server.MCPServer
	progressToken mcp.ProgressToken
}

// NewMCPReporter creates a new MCPReporter with the given server and token.
func NewMCPReporter(srv *server.MCPServer, token mcp.ProgressToken) *MCPReporter {
	return &MCPReporter{
		server:        srv,
		progressToken: token,
	}
}

// Report sends a progress notification via MCP.
func (r *MCPReporter) Report(ctx context.Context, progress, total float64, message string) {
	if r.server == nil || r.progressToken == nil {
		return
	}

	params := map[string]any{
		"progressToken": r.progressToken,
		"progress":      progress,
	}
	if total > 0 {
		params["total"] = total
	}
	if message != "" {
		params["message"] = message
	}

	// Send notification - ignore errors as progress is optional.
	//nolint:errcheck // Progress notifications are best-effort; errors are non-fatal.
	r.server.SendNotificationToClient(ctx, "notifications/progress", params)
}

// NoOpReporter implements Reporter but does nothing.
// Used when no progress token is provided in the request.
type NoOpReporter struct{}

// NewNoOpReporter creates a new NoOpReporter.
func NewNoOpReporter() *NoOpReporter {
	return &NoOpReporter{}
}

// Report does nothing for NoOpReporter.
func (*NoOpReporter) Report(_ context.Context, _, _ float64, _ string) {
	// Intentionally empty - used when client doesn't request progress.
}
