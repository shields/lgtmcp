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

package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"msrl.dev/lgtmcp/internal/config"
	"msrl.dev/lgtmcp/internal/testutil"
)

func TestWriteNewFileDiffEmptyContent(t *testing.T) {
	t.Parallel()
	// Callers skip empty content, but in isolation an empty file must render
	// like git: header lines only, with no hunk or no-newline marker.
	var buf bytes.Buffer
	writeNewFileDiff(&buf, "empty.txt", "", 0o644)
	out := buf.String()
	assert.Contains(t, out, "diff --git a/empty.txt b/empty.txt")
	assert.Contains(t, out, "new file mode 100644")
	assert.NotContains(t, out, "--- /dev/null")
	assert.NotContains(t, out, "@@")
	assert.NotContains(t, out, "No newline")
}

func TestWriteNewFileDiffMode(t *testing.T) {
	t.Parallel()
	t.Run("executable regular file uses 100755", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		writeNewFileDiff(&buf, "run.sh", "#!/bin/sh\n", 0o755)
		out := buf.String()
		assert.Contains(t, out, "new file mode 100755")
		assert.NotContains(t, out, "100644")
	})

	t.Run("non-executable regular file uses 100644", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		writeNewFileDiff(&buf, "notes.txt", "hello\n", 0o644)
		out := buf.String()
		assert.Contains(t, out, "new file mode 100644")
		assert.NotContains(t, out, "100755")
	})

	t.Run("symlink uses 120000 with target as content", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		// A symlink's mode carries ModeSymlink plus exec perm bits; the 120000
		// branch must win over the executable-bit branch, and the target string
		// (no trailing newline) is the content.
		writeNewFileDiff(&buf, "link", "target.txt", os.ModeSymlink|0o777)
		out := buf.String()
		assert.Contains(t, out, "new file mode 120000")
		assert.Contains(t, out, "+target.txt")
		assert.Contains(t, out, "No newline at end of file")
	})

	t.Run("empty executable file keeps 100755 with header only", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		// The mode line is written before the empty-content early return, so an
		// empty executable still records 100755 — with no hunk.
		writeNewFileDiff(&buf, "empty.sh", "", 0o755)
		out := buf.String()
		assert.Contains(t, out, "new file mode 100755")
		assert.NotContains(t, out, "@@")
	})
}

func TestGitFileMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mode os.FileMode
		want string
	}{
		{"plain 0644", 0o644, "100644"},
		{"owner-only 0600", 0o600, "100644"},
		{"group/other exec only", 0o655, "100644"},
		{"owner exec 0755", 0o755, "100755"},
		{"owner exec 0744", 0o744, "100755"},
		{"owner exec 0711", 0o711, "100755"},
		{"symlink with perm bits", os.ModeSymlink | 0o777, "120000"},
		{"symlink without perm bits", os.ModeSymlink, "120000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, gitFileMode(tt.mode))
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()
	t.Run("valid git repository", func(t *testing.T) {
		t.Parallel()
		dir := testutil.CreateTempGitRepo(t)
		g, err := New(dir, nil)
		require.NoError(t, err)
		assert.NotNil(t, g)
		assert.Equal(t, dir, g.repoPath)
	})

	t.Run("not a git repository", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		g, err := New(dir, nil)
		require.ErrorIs(t, err, ErrNotGitRepo)
		assert.Nil(t, g)
	})

	t.Run("worktree repository", func(t *testing.T) {
		t.Parallel()
		worktreeDir := createTempWorktree(t)
		g, err := New(worktreeDir, nil)
		require.NoError(t, err)
		assert.NotNil(t, g)
		assert.Equal(t, worktreeDir, g.repoPath)
	})
}

func TestGetDiff(t *testing.T) { //nolint:maintidx // many subtests in one test function
	t.Parallel()
	t.Run("no changes after commit", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// Create initial commit.
		testutil.CreateFile(t, tmpDir, "initial.txt", "content")
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.ErrorIs(t, err, ErrNoChanges)
		assert.Empty(t, diff)
	})

	t.Run("initial commit with files", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "file1.txt", "content1")
		testutil.CreateFile(t, tmpDir, "file2.txt", "content2")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)
		// Should show all files as new.
		assert.Contains(t, diff, "file1.txt")
		assert.Contains(t, diff, "file2.txt")
		assert.Contains(t, diff, "+content1")
		assert.Contains(t, diff, "+content2")
	})

	t.Run("initial commit with only an empty file is surfaced", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// No HEAD yet, so this exercises the initial-commit branch and its own
		// ErrNoChanges gate. An empty new file must still be surfaced as a
		// header-only block rather than collapsing to "no changes".
		testutil.CreateFile(t, tmpDir, "empty.py", "")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)
		assert.Contains(t, diff, "diff --git a/empty.py b/empty.py")
		assert.Contains(t, diff, "new file mode 100644")
	})

	t.Run("modified files", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "file1.txt", "initial")
		testutil.RunGitCmd(t, tmpDir, "add", "file1.txt")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

		testutil.CreateFile(t, tmpDir, "file1.txt", "modified")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)
		assert.Contains(t, diff, "file1.txt")
		assert.Contains(t, diff, "-initial")
		assert.Contains(t, diff, "+modified")
	})

	t.Run("untracked files with existing commits", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// Create initial commit.
		testutil.CreateFile(t, tmpDir, "existing.txt", "existing content")
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

		// Add untracked file.
		testutil.CreateFile(t, tmpDir, "untracked.txt", "new content")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)
		assert.Contains(t, diff, "untracked.txt")
		assert.Contains(t, diff, "+new content")
		assert.Contains(t, diff, "new file mode")
	})

	t.Run("empty untracked file is surfaced as a header-only block", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "existing.txt", "existing")
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

		// A newly added empty file (e.g. __init__.py, .gitkeep) has no content,
		// but its addition is still reviewable and git shows it — so it must
		// appear as a header-only new-file block, not be dropped from the diff.
		testutil.CreateFile(t, tmpDir, "empty.py", "")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)
		assert.Contains(t, diff, "diff --git a/empty.py b/empty.py")
		assert.Contains(t, diff, "new file mode 100644")
	})

	t.Run("untracked file ending in newline has no phantom empty added line", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "existing.txt", "existing")
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

		// Trailing newline is the line terminator, not a blank final line, so
		// the synthesized diff must not append a spurious empty "+" line.
		testutil.CreateFile(t, tmpDir, "untracked.txt", "alpha\nbeta\n")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)
		assert.Contains(t, diff, "+alpha\n+beta\n")
		// A genuine blank trailing line would be "+\n"; the terminator must not
		// produce one.
		assert.NotContains(t, diff, "+beta\n+\n")
	})

	t.Run("untracked file with genuine blank final line keeps it", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "existing.txt", "existing")
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

		// Two trailing newlines: one terminates "beta", the other is a real
		// blank line that must be preserved as a single "+" line.
		testutil.CreateFile(t, tmpDir, "untracked.txt", "alpha\nbeta\n\n")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)
		assert.Contains(t, diff, "+alpha\n+beta\n+\n")
	})

	t.Run("untracked file emits a hunk header like git", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "existing.txt", "existing")
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

		testutil.CreateFile(t, tmpDir, "untracked.txt", "alpha\nbeta\n")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)
		// Two content lines -> "@@ -0,0 +1,2 @@", matching git's own output.
		assert.Contains(t, diff, "@@ -0,0 +1,2 @@")
	})

	t.Run("untracked file without trailing newline gets a no-newline marker", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "existing.txt", "existing")
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

		// No trailing newline: git marks this explicitly, and so should the
		// synthesized diff.
		testutil.CreateFile(t, tmpDir, "untracked.txt", "alpha\nbeta")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)
		assert.Contains(t, diff, "+alpha\n+beta\n")
		assert.Contains(t, diff, "\\ No newline at end of file")
	})

	t.Run("forces a/ b/ prefixes even with diff.mnemonicPrefix set", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "main.go", "package main\n")
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")
		// With diff.mnemonicPrefix set, git would otherwise emit c/ and w/ (or
		// i/) prefixes that ExtractChangedFiles does not understand. GetDiff
		// must force the standard a/ b/ prefixes regardless.
		testutil.RunGitCmd(t, tmpDir, "config", "diff.mnemonicPrefix", "true")

		testutil.CreateFile(t, tmpDir, "main.go", "package main\n\nfunc main() {}\n")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)
		assert.Contains(t, diff, "diff --git a/main.go b/main.go")
		assert.Contains(t, diff, "--- a/main.go")
		assert.Contains(t, diff, "+++ b/main.go")
		assert.NotContains(t, diff, "c/main.go")
		assert.NotContains(t, diff, "w/main.go")
	})

	t.Run("mixed changes - staged and unstaged", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// Create initial commit.
		testutil.CreateFile(t, tmpDir, "existing.txt", "initial")
		testutil.RunGitCmd(t, tmpDir, "add", "existing.txt")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

		// Modify existing file.
		testutil.CreateFile(t, tmpDir, "existing.txt", "modified")

		// Add new staged file.
		testutil.CreateFile(t, tmpDir, "staged.txt", "staged content")
		testutil.RunGitCmd(t, tmpDir, "add", "staged.txt")

		// Modify staged file after staging (creates unstaged changes).
		testutil.CreateFile(t, tmpDir, "staged.txt", "staged content modified")

		// Add untracked file.
		testutil.CreateFile(t, tmpDir, "untracked.txt", "untracked content")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)

		// Should show all changes in unified diff.
		assert.Contains(t, diff, "existing.txt")
		assert.Contains(t, diff, "-initial")
		assert.Contains(t, diff, "+modified")

		// Staged file should show with its current working directory content.
		assert.Contains(t, diff, "staged.txt")
		assert.Contains(t, diff, "+staged content modified")

		// Untracked file.
		assert.Contains(t, diff, "untracked.txt")
		assert.Contains(t, diff, "+untracked content")
	})

	t.Run("custom context lines", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// Create a file with multiple lines
		file := filepath.Join(tmpDir, "multiline.txt")
		lines := []string{}
		for i := 1; i <= 30; i++ {
			lines = append(lines, fmt.Sprintf("Line %d", i))
		}
		require.NoError(t, os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o600))

		// Commit the file
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "Initial commit")

		// Modify a line in the middle
		lines[15] = "MODIFIED LINE 16"
		require.NoError(t, os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o600))

		// Test with custom context lines
		contextLines := 5
		cfg := &config.GitConfig{
			DiffContextLines: &contextLines,
		}
		g, err := New(tmpDir, cfg)
		require.NoError(t, err)
		assert.Equal(t, 5, g.diffContextLines)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)

		// The diff should include context lines around line 16
		// With 5 lines of context, we should see 5 lines before and 5 lines after
		// That's lines 11-15 (before), line 16 (changed), and lines 17-21 (after)
		assert.Contains(t, diff, "Line 11")
		assert.Contains(t, diff, "Line 15")
		assert.Contains(t, diff, "-Line 16")
		assert.Contains(t, diff, "+MODIFIED LINE 16")
		assert.Contains(t, diff, "Line 17")
		assert.Contains(t, diff, "Line 21")

		// Actually, git includes one more line to show the hunk header properly
		// So Line 10 might be visible as context
		// Let's check for lines definitely outside the range
		assert.NotContains(t, diff, "Line 9")
		assert.NotContains(t, diff, "Line 22")
	})

	t.Run("default context lines", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// Test with nil config (should default to 20)
		g, err := New(tmpDir, nil)
		require.NoError(t, err)
		assert.Equal(t, 20, g.diffContextLines)

		// Test with zero context lines (should be 0, not default)
		zeroLines := 0
		cfg := &config.GitConfig{
			DiffContextLines: &zeroLines,
		}
		g2, err := New(tmpDir, cfg)
		require.NoError(t, err)
		assert.Equal(t, 0, g2.diffContextLines)
	})

	t.Run("zero context lines", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// Create a file with multiple lines
		file := filepath.Join(tmpDir, "multiline.txt")
		lines := []string{}
		for i := 1; i <= 30; i++ {
			lines = append(lines, fmt.Sprintf("Line %d", i))
		}
		require.NoError(t, os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o600))

		// Commit the file
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "Initial commit")

		// Modify a line in the middle
		lines[15] = "MODIFIED LINE 16"
		require.NoError(t, os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o600))

		// Test with zero context lines
		zeroLines := 0
		cfg := &config.GitConfig{
			DiffContextLines: &zeroLines,
		}
		g, err := New(tmpDir, cfg)
		require.NoError(t, err)
		assert.Equal(t, 0, g.diffContextLines)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)

		// With 0 context lines, we should only see the changed line
		assert.Contains(t, diff, "-Line 16")
		assert.Contains(t, diff, "+MODIFIED LINE 16")

		// Git shows one line of context even with --unified=0 for the hunk header
		// But it shouldn't show more than that
		assert.NotContains(t, diff, "Line 14")
		assert.NotContains(t, diff, "Line 18")
	})

	t.Run("context cancellation", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err = g.GetDiff(ctx)
		require.Error(t, err)
	})

	t.Run("worktree with changes", func(t *testing.T) {
		t.Parallel()
		worktreeDir := createTempWorktree(t)

		g, err := New(worktreeDir, nil)
		require.NoError(t, err)

		testutil.CreateFile(t, worktreeDir, "worktree-file.txt", "worktree content")

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)
		assert.Contains(t, diff, "worktree-file.txt")
		assert.Contains(t, diff, "+worktree content")
	})
}

func TestStageAll(t *testing.T) {
	t.Parallel()
	t.Run("stage all changes", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "file1.txt", "content1")
		testutil.CreateFile(t, tmpDir, "file2.txt", "content2")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		err = g.StageAll(t.Context())
		require.NoError(t, err)

		status := testutil.RunGitCmd(t, tmpDir, "status", "--porcelain")
		assert.Contains(t, status, "A  file1.txt")
		assert.Contains(t, status, "A  file2.txt")
	})

	t.Run("no changes to stage", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		err = g.StageAll(t.Context())
		require.NoError(t, err)
	})

	t.Run("stage deleted file", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "to-delete.txt", "will be deleted")
		testutil.RunGitCmd(t, tmpDir, "add", "to-delete.txt")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "add file")

		require.NoError(t, os.Remove(filepath.Join(tmpDir, "to-delete.txt")))

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		err = g.StageAll(t.Context())
		require.NoError(t, err)

		status := testutil.RunGitCmd(t, tmpDir, "status", "--porcelain")
		assert.Contains(t, status, "D  to-delete.txt")
	})
}

func TestStageFiles(t *testing.T) {
	t.Parallel()

	t.Run("stages only listed files and skips extras", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// The two files that "passed the security scan".
		testutil.CreateFile(t, tmpDir, "scanned1.txt", "content1")
		testutil.CreateFile(t, tmpDir, "scanned2.txt", "content2")

		// An extra file that appears after the scan but before staging.
		testutil.CreateFile(t, tmpDir, "extra-unscanned.txt", "leaked secret")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		err = g.StageFiles(t.Context(), []string{"scanned1.txt", "scanned2.txt"})
		require.NoError(t, err)

		status := testutil.RunGitCmd(t, tmpDir, "status", "--porcelain")
		assert.Contains(t, status, "A  scanned1.txt")
		assert.Contains(t, status, "A  scanned2.txt")
		// The extra file must remain untracked, never staged.
		assert.Contains(t, status, "?? extra-unscanned.txt")
		assert.NotContains(t, status, "A  extra-unscanned.txt")
	})

	t.Run("empty file list is a no-op", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)
		testutil.CreateFile(t, tmpDir, "untracked.txt", "data")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		err = g.StageFiles(t.Context(), nil)
		require.NoError(t, err)

		status := testutil.RunGitCmd(t, tmpDir, "status", "--porcelain")
		assert.Contains(t, status, "?? untracked.txt")
	})

	t.Run("stages modifications and deletions for listed files", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "to-modify.txt", "original")
		testutil.CreateFile(t, tmpDir, "to-delete.txt", "doomed")
		testutil.CreateFile(t, tmpDir, "untouched.txt", "keep")
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

		testutil.CreateFile(t, tmpDir, "to-modify.txt", "changed")
		require.NoError(t, os.Remove(filepath.Join(tmpDir, "to-delete.txt")))
		// An extra file appearing after the original scan.
		testutil.CreateFile(t, tmpDir, "sneaky.txt", "should not be staged")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		err = g.StageFiles(t.Context(), []string{"to-modify.txt", "to-delete.txt"})
		require.NoError(t, err)

		status := testutil.RunGitCmd(t, tmpDir, "status", "--porcelain")
		assert.Contains(t, status, "M  to-modify.txt")
		assert.Contains(t, status, "D  to-delete.txt")
		assert.Contains(t, status, "?? sneaky.txt")
		assert.NotContains(t, status, "A  sneaky.txt")
	})

	t.Run("rejects empty path", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)
		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		err = g.StageFiles(t.Context(), []string{"valid.txt", ""})
		require.ErrorIs(t, err, ErrInvalidPath)
	})

	t.Run("literal pathspec prevents wildcard expansion", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// A file literally named "*". Without GIT_LITERAL_PATHSPECS,
		// this would glob to every file in the directory.
		testutil.CreateFile(t, tmpDir, "star.txt", "the star")
		testutil.CreateFile(t, tmpDir, "secret.txt", "leaked secret")
		testutil.CreateFile(t, tmpDir, "*", "literal star file")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		err = g.StageFiles(t.Context(), []string{"*"})
		require.NoError(t, err)

		status := testutil.RunGitCmd(t, tmpDir, "status", "--porcelain")
		// Only the literal "*" file should be staged.
		assert.Contains(t, status, "A  *\n")
		assert.Contains(t, status, "?? secret.txt")
		assert.Contains(t, status, "?? star.txt")
		assert.NotContains(t, status, "A  secret.txt")
		assert.NotContains(t, status, "A  star.txt")
	})

	t.Run("stages files whose names begin with dash", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "-dashed.txt", "leading dash")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		err = g.StageFiles(t.Context(), []string{"-dashed.txt"})
		require.NoError(t, err)

		status := testutil.RunGitCmd(t, tmpDir, "status", "--porcelain")
		assert.Contains(t, status, "-dashed.txt")
		assert.NotContains(t, status, "??")
	})
}

func TestCommit(t *testing.T) {
	t.Parallel()
	t.Run("successful commit", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "file.txt", "content")
		testutil.RunGitCmd(t, tmpDir, "add", "file.txt")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		sha, err := g.Commit(t.Context(), "test commit")
		require.NoError(t, err)
		assert.NotEmpty(t, sha)
		assert.Len(t, sha, 40)

		log := testutil.RunGitCmd(t, tmpDir, "log", "--oneline", "-1")
		assert.Contains(t, log, "test commit")
	})

	t.Run("empty commit message", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		sha, err := g.Commit(t.Context(), "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "commit message cannot be empty")
		assert.Empty(t, sha)
	})

	t.Run("nothing to commit", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		sha, err := g.Commit(t.Context(), "test commit")
		require.Error(t, err)
		assert.Empty(t, sha)
	})

	t.Run("commit preserves author from config", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "file.txt", "content")
		testutil.RunGitCmd(t, tmpDir, "add", "file.txt")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		_, err = g.Commit(t.Context(), "author test")
		require.NoError(t, err)

		author := testutil.RunGitCmd(t, tmpDir, "log", "-1", "--format=%an <%ae>")
		assert.Equal(t, "Test User <test@example.com>", author)
	})
}

func TestGetFileContent(t *testing.T) {
	t.Parallel()
	t.Run("read existing file", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "test.txt", "file content")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		content, err := g.GetFileContent(t.Context(), "test.txt")
		require.NoError(t, err)
		assert.Equal(t, "file content", content)
	})

	t.Run("read file in subdirectory", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0o750))
		testutil.CreateFile(t, tmpDir, "subdir/file.txt", "nested content")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		content, err := g.GetFileContent(t.Context(), "subdir/file.txt")
		require.NoError(t, err)
		assert.Equal(t, "nested content", content)
	})

	t.Run("file not found", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		content, err := g.GetFileContent(t.Context(), "nonexistent.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "file not found")
		assert.Empty(t, content)
	})

	t.Run("absolute path rejected", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		content, err := g.GetFileContent(t.Context(), "/etc/passwd")
		require.ErrorIs(t, err, ErrInvalidPath)
		assert.Empty(t, content)
	})

	t.Run("path traversal rejected", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		content, err := g.GetFileContent(t.Context(), "../../../etc/passwd")
		require.ErrorIs(t, err, ErrPathOutsideRepo)
		assert.Empty(t, content)
	})

	t.Run("symlink outside repo rejected", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(tmpDir, "link")))

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		content, err := g.GetFileContent(t.Context(), "link")
		require.Error(t, err)
		assert.Empty(t, content)
	})

	t.Run("directory traversal vulnerability - sibling directory with same prefix", func(t *testing.T) {
		t.Parallel()
		// Create a temp directory for our test.
		baseDir := t.TempDir()

		// Create two directories: one is our repo, one is a sibling with same prefix.
		repoDir := filepath.Join(baseDir, "repo")
		siblingDir := filepath.Join(baseDir, "repo-secrets")

		// Initialize the repo directory.
		require.NoError(t, os.MkdirAll(repoDir, 0o750))
		require.NoError(t, os.MkdirAll(filepath.Join(repoDir, ".git"), 0o750))

		// Create a secret file in the sibling directory.
		require.NoError(t, os.MkdirAll(siblingDir, 0o750))
		secretFile := filepath.Join(siblingDir, "secret.txt")
		require.NoError(t, os.WriteFile(secretFile, []byte("SECRET DATA"), 0o600))

		// Initialize git repo.
		g, err := New(repoDir, nil)
		require.NoError(t, err)

		// Try to access the secret file using a crafted path.
		// This constructs a path that would resolve to /baseDir/repo-secrets/secret.txt.
		// The vulnerability is that absPath would start with "/baseDir/repo" which passes.
		// the naive prefix check, even though it's actually "/baseDir/repo-secrets/secret.txt".
		craftedPath := "../repo-secrets/secret.txt"

		// This SHOULD fail but with the vulnerable code it might not.
		content, err := g.GetFileContent(t.Context(), craftedPath)

		// The file access should be rejected.
		require.Error(t, err, "Should reject access to sibling directory")
		assert.Contains(t, err.Error(), "outside repo", "Error should indicate path is outside repo")
		assert.Empty(t, content, "Should not return any content from outside repo")
	})

	t.Run("worktree file retrieval", func(t *testing.T) {
		t.Parallel()
		worktreeDir := createTempWorktree(t)

		g, err := New(worktreeDir, nil)
		require.NoError(t, err)

		testutil.CreateFile(t, worktreeDir, "wt-file.txt", "worktree data")

		content, err := g.GetFileContent(t.Context(), "wt-file.txt")
		require.NoError(t, err)
		assert.Equal(t, "worktree data", content)
	})
}

func TestGetRepoPath(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)

	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	assert.Equal(t, tmpDir, g.GetRepoPath())
}

func TestCheckGitRepo(t *testing.T) {
	t.Parallel()
	t.Run("valid git repo", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		assert.True(t, CheckGitRepo(tmpDir))
	})

	t.Run("non-git directory", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		assert.False(t, CheckGitRepo(tmpDir))
	})

	t.Run("nonexistent directory", func(t *testing.T) {
		t.Parallel()
		assert.False(t, CheckGitRepo("/nonexistent/path"))
	})

	t.Run(".git file without gitdir prefix", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		testutil.CreateFile(t, tmpDir, ".git", "not a directory")
		assert.False(t, CheckGitRepo(tmpDir))
	})

	t.Run("valid git worktree", func(t *testing.T) {
		t.Parallel()
		worktreeDir := createTempWorktree(t)
		assert.True(t, CheckGitRepo(worktreeDir))
	})
}

func TestRunGitCommand(t *testing.T) {
	t.Parallel()
	t.Run("command timeout", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(t.Context(), 1)
		defer cancel()

		_, err = g.runGitCommand(ctx, "log", "--follow", "--all", "--", ".")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timed out")
	})

	t.Run("invalid command", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		_, err = g.runGitCommand(t.Context(), "invalid-command")
		require.ErrorIs(t, err, ErrCommandFailed)
	})
}

func TestStageAll_Error(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)
	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	// Remove .git to make git commands fail.
	require.NoError(t, os.RemoveAll(filepath.Join(tmpDir, ".git")))

	err = g.StageAll(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stage all changes")
}

func TestStageFiles_Error(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)
	testutil.CreateFile(t, tmpDir, "file.txt", "content")
	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	// Remove .git to make git commands fail.
	require.NoError(t, os.RemoveAll(filepath.Join(tmpDir, ".git")))

	err = g.StageFiles(t.Context(), []string{"file.txt"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stage files")
}

func TestCommit_StatusError(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)
	testutil.CreateFile(t, tmpDir, "file.txt", "content")
	testutil.RunGitCmd(t, tmpDir, "add", "file.txt")
	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	// Remove .git so git status fails.
	require.NoError(t, os.RemoveAll(filepath.Join(tmpDir, ".git")))

	_, err = g.Commit(t.Context(), "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get status")
}

func TestCommit_CommitCommandError(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)
	testutil.CreateFile(t, tmpDir, "file.txt", "content")
	testutil.RunGitCmd(t, tmpDir, "add", "file.txt")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

	// Create a change to commit.
	testutil.CreateFile(t, tmpDir, "file.txt", "modified")
	testutil.RunGitCmd(t, tmpDir, "add", "file.txt")

	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	// Corrupt the index so commit fails.
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".git", "index"), []byte("corrupt"), 0o600))

	_, err = g.Commit(t.Context(), "test")
	require.Error(t, err)
}

func TestGetFileContent_NotRegularFile(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)
	// Create a directory where a file is expected.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "adir"), 0o750))

	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	_, err = g.GetFileContent(t.Context(), "adir")
	require.ErrorIs(t, err, ErrNotRegularFile)
}

func TestHasGitdirPrefix_ShortFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	// Create .git file shorter than "gitdir: " (8 bytes).
	testutil.CreateFile(t, tmpDir, ".git", "short")

	assert.False(t, CheckGitRepo(tmpDir))
}

func TestGetDiff_DiffCommandError(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)
	testutil.CreateFile(t, tmpDir, "file.txt", "content")
	testutil.RunGitCmd(t, tmpDir, "add", "file.txt")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")
	testutil.CreateFile(t, tmpDir, "file.txt", "modified")

	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	// Remove .git/HEAD to make git diff HEAD fail.
	require.NoError(t, os.Remove(filepath.Join(tmpDir, ".git", "HEAD")))

	_, err = g.GetDiff(t.Context())
	require.Error(t, err)
}

func TestGetDiff_InitialCommitNoFiles(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)
	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	_, err = g.GetDiff(t.Context())
	require.ErrorIs(t, err, ErrNoChanges)
}

// createTempWorktree creates a main repo with an initial commit and adds a
// worktree. It returns the worktree path. The main repo and worktree are
// cleaned up automatically by t.TempDir.
func createTempWorktree(t *testing.T) string {
	t.Helper()
	mainRepo := testutil.CreateTempGitRepo(t)
	testutil.CreateFile(t, mainRepo, "init.txt", "init")
	testutil.RunGitCmd(t, mainRepo, "add", ".")
	testutil.RunGitCmd(t, mainRepo, "commit", "-m", "initial")

	worktreeDir := t.TempDir()
	testutil.RunGitCmd(t, mainRepo, "worktree", "add", worktreeDir, "-b", "worktree-branch")

	return worktreeDir
}

func TestIsIgnored(t *testing.T) {
	t.Parallel()
	repoDir := testutil.CreateTempGitRepo(t)

	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".gitignore"),
		[]byte("secret.txt\n*.key\nbuild/\n!keep.key\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "nested"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "nested", ".gitignore"),
		[]byte("local.conf\n"), 0o600))
	// .git/info/exclude is consulted alongside the .gitignore files; an
	// in-process matcher that only reads .gitignore would miss it.
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".git", "info", "exclude"),
		[]byte("excluded.txt\n"), 0o600))

	tests := []struct {
		name    string
		path    string
		ignored bool
	}{
		{"plain ignored file", "secret.txt", true},
		{"pattern ignored file", "private.key", true},
		{"negated pattern is not ignored", "keep.key", false},
		{"member of ignored directory", "build/output.js", true},
		{"unmatched file is not ignored", "main.go", false},
		{"nested gitignore applies", "nested/local.conf", true},
		{"nested gitignore does not leak up", "local.conf", false},
		{"info/exclude is honored", "excluded.txt", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := IsIgnored(t.Context(), repoDir, tt.path)
			require.NoError(t, err)
			assert.Equal(t, tt.ignored, got)
		})
	}
}

func TestIsIgnored_LeadingDashPath(t *testing.T) {
	t.Parallel()
	repoDir := testutil.CreateTempGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("-weird.txt\n"), 0o600))

	// The "--" separator must keep a path beginning with "-" from being parsed
	// as an option flag.
	got, err := IsIgnored(t.Context(), repoDir, "-weird.txt")
	require.NoError(t, err)
	assert.True(t, got)
}

func TestIsIgnored_NotGitRepo(t *testing.T) {
	t.Parallel()
	// A non-repository directory makes check-ignore exit 128; IsIgnored must
	// surface that as an error so callers fail closed.
	got, err := IsIgnored(t.Context(), t.TempDir(), "file.txt")
	require.ErrorIs(t, err, ErrCommandFailed)
	assert.False(t, got)
}

// TestIsIgnored_GitEnvLeak verifies that inherited git-location variables
// (GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE) cannot redirect the check at a
// different repository, as happens when lgtmcp runs inside another git process
// such as a pre-commit hook. It mutates the process environment and therefore
// must not run in parallel.
func TestIsIgnored_GitEnvLeak(t *testing.T) {
	repoDir := testutil.CreateTempGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("secret.txt\n"), 0o600))

	otherRepo := testutil.CreateTempGitRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(otherRepo, ".git"))
	t.Setenv("GIT_WORK_TREE", otherRepo)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(otherRepo, ".git", "index"))

	ignored, err := IsIgnored(t.Context(), repoDir, "secret.txt")
	require.NoError(t, err)
	assert.True(t, ignored, "target repo's .gitignore must be honored despite leaked git env")

	notIgnored, err := IsIgnored(t.Context(), repoDir, "normal.txt")
	require.NoError(t, err)
	assert.False(t, notIgnored)
}

// TestIsIgnored_GitConfigGlobalLeak verifies that a leaked GIT_CONFIG_GLOBAL —
// which the previous narrow scrub left in place — cannot repoint
// core.excludesFile and skew the result. It mutates the environment and must
// not run in parallel.
func TestIsIgnored_GitConfigGlobalLeak(t *testing.T) {
	repoDir := testutil.CreateTempGitRepo(t)

	// An attacker-controlled global config that ignores a file the repo does not.
	evilDir := t.TempDir()
	evilExcludes := filepath.Join(evilDir, "excludes")
	require.NoError(t, os.WriteFile(evilExcludes, []byte("globally-evil.txt\n"), 0o600))
	evilConfig := filepath.Join(evilDir, "gitconfig")
	require.NoError(t, os.WriteFile(evilConfig,
		fmt.Appendf(nil, "[core]\n\texcludesFile = %s\n", evilExcludes), 0o600))
	t.Setenv("GIT_CONFIG_GLOBAL", evilConfig)

	// Because runGit strips all GIT_* variables, the global excludesFile is not
	// consulted and the file is reported as not ignored.
	ignored, err := IsIgnored(t.Context(), repoDir, "globally-evil.txt")
	require.NoError(t, err)
	assert.False(t, ignored, "leaked GIT_CONFIG_GLOBAL must not influence the result")
}
