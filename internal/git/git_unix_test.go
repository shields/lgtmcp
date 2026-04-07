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

//go:build unix

package git

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"msrl.dev/lgtmcp/internal/testutil"
)

func TestIsGitRepo_SpecialFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// Create .git as a named pipe (FIFO).
	fifoPath := filepath.Join(tmpDir, ".git")
	require.NoError(t, syscall.Mkfifo(fifoPath, 0o600))

	assert.False(t, CheckGitRepo(tmpDir))
}

func TestGetFileContent_SymlinkChainEscapesRepo(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)

	// Build a chain: link1 -> link2 -> /etc/passwd. Both intermediates
	// live inside the repo so a single os.Readlink hop appears safe,
	// but the resolved chain escapes the repository.
	link2 := filepath.Join(tmpDir, "link2")
	require.NoError(t, os.Symlink("/etc/passwd", link2))

	link1 := filepath.Join(tmpDir, "link1")
	require.NoError(t, os.Symlink(link2, link1))

	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	content, err := g.GetFileContent(t.Context(), "link1")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPathOutsideRepo)
	assert.Empty(t, content)
}

func TestGetFileContent_SymlinkToSiblingDirectory(t *testing.T) {
	t.Parallel()
	baseDir := t.TempDir()

	// Create the repo and a sibling directory whose name shares the
	// repo's prefix ("repo" vs "repo-secrets").
	repoDir := filepath.Join(baseDir, "repo")
	siblingDir := filepath.Join(baseDir, "repo-secrets")

	testutil.RunGitCmd(t, baseDir, "init", "repo")
	testutil.RunGitCmd(t, repoDir, "config", "user.email", "test@example.com")
	testutil.RunGitCmd(t, repoDir, "config", "user.name", "Test User")

	require.NoError(t, os.MkdirAll(siblingDir, 0o750))
	secretFile := filepath.Join(siblingDir, "file")
	require.NoError(t, os.WriteFile(secretFile, []byte("SECRET DATA"), 0o600))

	// Symlink inside the repo pointing to the sibling directory's file.
	require.NoError(t, os.Symlink(secretFile, filepath.Join(repoDir, "leak")))

	g, err := New(repoDir, nil)
	require.NoError(t, err)

	content, err := g.GetFileContent(t.Context(), "leak")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPathOutsideRepo)
	assert.Empty(t, content)
}
