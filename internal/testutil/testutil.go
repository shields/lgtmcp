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
//
// The environment is scrubbed of any GIT_* variables inherited from the caller
// so that the command operates strictly on the directory passed in. Without
// this, running tests under a git pre-commit hook (which sets GIT_DIR,
// GIT_INDEX_FILE, GIT_AUTHOR_*, etc.) would silently make every test git
// invocation operate on the surrounding repository — corrupting it.
//
// The developer's global and system git config are also ignored so that
// settings like commit.gpgsign, core.hooksPath, or commit.template cannot
// make tests behave differently from machine to machine.
func RunGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // Test helper with controlled args
	cmd.Dir = dir
	cmd.Env = append(
		scrubGitEnv(os.Environ()),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git command failed: %v\nOutput: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

// scrubGitEnv returns env with all GIT_* variables removed.
func scrubGitEnv(env []string) []string {
	out := env[:0:0]
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// CreateTempGitRepo creates a temporary git repository with user config for commits.
//
// The branch name is pinned so it does not depend on the git version's
// compiled-in default, and commit signing is disabled repo-locally: the
// production git helpers deliberately honor the user's global config
// (signing is a feature), so a developer's commit.gpgsign=true would
// otherwise make code-under-test commits in this repo try to sign.
func CreateTempGitRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	RunGitCmd(t, tmpDir, "init", "-b", "main")
	RunGitCmd(t, tmpDir, "config", "user.email", "test@example.com")
	RunGitCmd(t, tmpDir, "config", "user.name", "Test User")
	RunGitCmd(t, tmpDir, "config", "commit.gpgsign", "false")
	return tmpDir
}
