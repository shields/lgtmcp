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

// Package testutil provides shared test helpers used across packages.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"msrl.dev/lgtmcp/internal/logging"
)

// NewTestLogger creates a no-op logger for use in tests.
func NewTestLogger() logging.Logger {
	logger, err := logging.New(logging.Config{
		Output: "none",
	})
	if err != nil {
		panic(err)
	}
	return logger
}

// CreateFile creates a file with the given content, creating parent directories as needed.
func CreateFile(t *testing.T, dir, name, content string) {
	t.Helper()
	fullPath := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o750); err != nil {
		t.Fatalf("Failed to create directories: %v", err)
	}
	require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o600))
}

// RunGitCmd runs a git command in the specified directory and returns its output.
func RunGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // Test helper with controlled args
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git command failed: %v\nOutput: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

// CreateTempGitRepo creates a temporary git repository with user config for commits.
func CreateTempGitRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	RunGitCmd(t, tmpDir, "init")
	RunGitCmd(t, tmpDir, "config", "user.email", "test@example.com")
	RunGitCmd(t, tmpDir, "config", "user.name", "Test User")
	return tmpDir
}
