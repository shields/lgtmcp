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

func TestGetDiff_ExecutableUntrackedFile(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)

	testutil.CreateFile(t, tmpDir, "existing.txt", "existing")
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

	// An executable untracked script must synthesize as mode 100755 (CreateFile
	// writes 0o600, so chmod it executable explicitly)...
	testutil.CreateFile(t, tmpDir, "run.sh", "#!/bin/sh\necho hi\n")
	//nolint:gosec // Deliberately executable to exercise 100755 mode synthesis.
	require.NoError(t, os.Chmod(filepath.Join(tmpDir, "run.sh"), 0o755))
	// ...while a plain untracked file stays 100644.
	testutil.CreateFile(t, tmpDir, "notes.txt", "plain\n")

	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	diff, err := g.GetDiff(t.Context())
	require.NoError(t, err)
	// Assert each mode line together with its diff header so the modes are
	// coupled to the right files; separate Contains checks would still pass if
	// the two modes were swapped between run.sh and notes.txt.
	assert.Contains(t, diff, "diff --git a/run.sh b/run.sh\nnew file mode 100755")
	assert.Contains(t, diff, "diff --git a/notes.txt b/notes.txt\nnew file mode 100644")
}

// TestGetDiff_SpecialCharacterFilenames exercises names that are legal on unix
// but not Windows: untracked files containing a double quote or a newline must
// be read via the raw -z listing and rendered with C-quoted headers exactly as
// git does, instead of being silently dropped (quote) or corrupting the
// synthesized header lines (newline).
func TestGetDiff_SpecialCharacterFilenames(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)

	testutil.CreateFile(t, tmpDir, "existing.txt", "existing")
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

	testutil.CreateFile(t, tmpDir, `with"quote.txt`, "quoted name\n")
	testutil.CreateFile(t, tmpDir, "new\nline.txt", "newline name\n")

	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	diff, err := g.GetDiff(t.Context())
	require.NoError(t, err)
	assert.Contains(t, diff, `diff --git "a/with\"quote.txt" "b/with\"quote.txt"`)
	assert.Contains(t, diff, "+quoted name")
	assert.Contains(t, diff, `diff --git "a/new\nline.txt" "b/new\nline.txt"`)
	assert.Contains(t, diff, "+newline name")
	// The raw newline byte must never reach a header line.
	assert.NotContains(t, diff, "a/new\nline.txt")
}

func TestGetDiff_SymlinkUntracked(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)

	// target.txt is committed so it does not appear in the diff itself; only the
	// untracked symlink should. git records a symlink as mode 120000 with the
	// link target as content — never the dereferenced file's bytes.
	testutil.CreateFile(t, tmpDir, "target.txt", "secret-bytes\n")
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")
	require.NoError(t, os.Symlink("target.txt", filepath.Join(tmpDir, "link")))

	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	diff, err := g.GetDiff(t.Context())
	require.NoError(t, err)
	assert.Contains(t, diff, "diff --git a/link b/link")
	assert.Contains(t, diff, "new file mode 120000")
	assert.Contains(t, diff, "+target.txt")
	// os.Readlink reads only the link text, so the target file's contents must
	// never leak into the diff.
	assert.NotContains(t, diff, "secret-bytes")
}

func TestGetDiff_SymlinkEscapingRepoIsSurfaced(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)

	testutil.CreateFile(t, tmpDir, "existing.txt", "existing")
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

	// A symlink whose target escapes the repo must still be shown as a new
	// 120000 entry — previously GetFileContent rejected it and the file was
	// silently dropped from the diff entirely.
	require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(tmpDir, "escape")))

	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	diff, err := g.GetDiff(t.Context())
	require.NoError(t, err)
	assert.Contains(t, diff, "diff --git a/escape b/escape")
	assert.Contains(t, diff, "new file mode 120000")
	assert.Contains(t, diff, "+/etc/passwd")
}

func TestGetDiff_DanglingSymlinkSurfaced(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)

	testutil.CreateFile(t, tmpDir, "existing.txt", "existing")
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

	// A symlink to a nonexistent in-repo target must still be surfaced as a
	// 120000 entry with the (dangling) target text as content — os.Readlink does
	// not require the target to exist, and the old GetFileContent path dropped it.
	require.NoError(t, os.Symlink("does-not-exist.txt", filepath.Join(tmpDir, "dangle")))

	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	diff, err := g.GetDiff(t.Context())
	require.NoError(t, err)
	assert.Contains(t, diff, "diff --git a/dangle b/dangle")
	assert.Contains(t, diff, "new file mode 120000")
	assert.Contains(t, diff, "+does-not-exist.txt")
}

// TestRunGit_SignalKilledSurfacesSignal pins the error when git dies from a
// signal instead of exiting: the signal name must reach the caller rather
// than being collapsed into an opaque "exit status -1" (seen in the wild as
// intermittent git crashes whose cause the old message hid).
func TestRunGit_SignalKilledSurfacesSignal(t *testing.T) {
	// No t.Parallel(): t.Setenv forbids it.
	binDir := t.TempDir()
	fakeGit := filepath.Join(binDir, "git")
	//nolint:gosec // The fake git must be executable.
	require.NoError(t, os.WriteFile(fakeGit, []byte("#!/bin/sh\nkill -SEGV $$\n"), 0o755))
	t.Setenv("PATH", binDir)

	res, err := runGit(t.Context(), t.TempDir(), nil, nil, "status")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signal")
	assert.Equal(t, -1, res.exitCode)
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
