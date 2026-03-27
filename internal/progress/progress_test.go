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

package progress

import (
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
)

func TestNoOpReporter(t *testing.T) {
	t.Parallel()

	t.Run("Report does nothing", func(t *testing.T) {
		t.Parallel()
		reporter := NewNoOpReporter()

		// Should not panic or error.
		assert.NotPanics(t, func() {
			reporter.Report(t.Context(), 1, 5, "test message")
		})
	})

	t.Run("implements Reporter interface", func(t *testing.T) {
		t.Parallel()
		var reporter Reporter = NewNoOpReporter()
		assert.NotNil(t, reporter)
	})
}

func TestMCPReporter(t *testing.T) {
	t.Parallel()

	t.Run("nil server does not panic", func(t *testing.T) {
		t.Parallel()
		reporter := NewMCPReporter(nil, "test-token")

		// Should not panic even with nil server.
		assert.NotPanics(t, func() {
			reporter.Report(t.Context(), 1, 5, "test message")
		})
	})

	t.Run("nil token does not panic", func(t *testing.T) {
		t.Parallel()
		reporter := NewMCPReporter(nil, nil)

		// Should not panic with nil token.
		assert.NotPanics(t, func() {
			reporter.Report(t.Context(), 1, 5, "test message")
		})
	})

	t.Run("implements Reporter interface", func(t *testing.T) {
		t.Parallel()
		var reporter Reporter = NewMCPReporter(nil, "test-token")
		assert.NotNil(t, reporter)
	})

	t.Run("report with total and message", func(t *testing.T) {
		t.Parallel()
		srv := server.NewMCPServer("test", "0.1")
		reporter := NewMCPReporter(srv, "test-token")

		// Exercises all branches: server != nil, token != nil, total > 0, message != "".
		// SendNotificationToClient fails silently (no connected client).
		assert.NotPanics(t, func() {
			reporter.Report(t.Context(), 3, 10, "processing files")
		})
	})

	t.Run("report with zero total and empty message", func(t *testing.T) {
		t.Parallel()
		srv := server.NewMCPServer("test", "0.1")
		reporter := NewMCPReporter(srv, "test-token")

		// Covers the false branches of total > 0 and message != "".
		assert.NotPanics(t, func() {
			reporter.Report(t.Context(), 1, 0, "")
		})
	})
}
