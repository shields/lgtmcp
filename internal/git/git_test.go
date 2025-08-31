package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shields/lgtmcp/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()
	t.Run("valid git repository", func(t *testing.T) {
		t.Parallel()
		dir := createTempGitRepo(t)
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
}

func TestGetDiff(t *testing.T) {
	t.Parallel()
	t.Run("no changes after commit", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		// Create initial commit.
		createFile(t, tmpDir, "initial.txt", "content")
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "initial")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.ErrorIs(t, err, ErrNoChanges)
		assert.Empty(t, diff)
	})

	t.Run("initial commit with files", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		createFile(t, tmpDir, "file1.txt", "content1")
		createFile(t, tmpDir, "file2.txt", "content2")

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

	t.Run("modified files", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		createFile(t, tmpDir, "file1.txt", "initial")
		runGitCmd(t, tmpDir, "add", "file1.txt")
		runGitCmd(t, tmpDir, "commit", "-m", "initial")

		createFile(t, tmpDir, "file1.txt", "modified")

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
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		// Create initial commit.
		createFile(t, tmpDir, "existing.txt", "existing content")
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "initial")

		// Add untracked file.
		createFile(t, tmpDir, "untracked.txt", "new content")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		diff, err := g.GetDiff(t.Context())
		require.NoError(t, err)
		assert.Contains(t, diff, "untracked.txt")
		assert.Contains(t, diff, "+new content")
		assert.Contains(t, diff, "new file mode")
	})

	t.Run("mixed changes - staged and unstaged", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		// Create initial commit.
		createFile(t, tmpDir, "existing.txt", "initial")
		runGitCmd(t, tmpDir, "add", "existing.txt")
		runGitCmd(t, tmpDir, "commit", "-m", "initial")

		// Modify existing file.
		createFile(t, tmpDir, "existing.txt", "modified")

		// Add new staged file.
		createFile(t, tmpDir, "staged.txt", "staged content")
		runGitCmd(t, tmpDir, "add", "staged.txt")

		// Modify staged file after staging (creates unstaged changes).
		createFile(t, tmpDir, "staged.txt", "staged content modified")

		// Add untracked file.
		createFile(t, tmpDir, "untracked.txt", "untracked content")

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
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		// Create a file with multiple lines
		file := filepath.Join(tmpDir, "multiline.txt")
		lines := []string{}
		for i := 1; i <= 30; i++ {
			lines = append(lines, fmt.Sprintf("Line %d", i))
		}
		require.NoError(t, os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o600))

		// Commit the file
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "Initial commit")

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
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

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
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		// Create a file with multiple lines
		file := filepath.Join(tmpDir, "multiline.txt")
		lines := []string{}
		for i := 1; i <= 30; i++ {
			lines = append(lines, fmt.Sprintf("Line %d", i))
		}
		require.NoError(t, os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o600))

		// Commit the file
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "Initial commit")

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
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err = g.GetDiff(ctx)
		require.Error(t, err)
	})
}

func TestStageAll(t *testing.T) {
	t.Parallel()
	t.Run("stage all changes", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		createFile(t, tmpDir, "file1.txt", "content1")
		createFile(t, tmpDir, "file2.txt", "content2")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		err = g.StageAll(t.Context())
		require.NoError(t, err)

		status := runGitCmd(t, tmpDir, "status", "--porcelain")
		assert.Contains(t, status, "A  file1.txt")
		assert.Contains(t, status, "A  file2.txt")
	})

	t.Run("no changes to stage", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		err = g.StageAll(t.Context())
		require.NoError(t, err)
	})
}

func TestCommit(t *testing.T) {
	t.Parallel()
	t.Run("successful commit", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		createFile(t, tmpDir, "file.txt", "content")
		runGitCmd(t, tmpDir, "add", "file.txt")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		sha, err := g.Commit(t.Context(), "test commit")
		require.NoError(t, err)
		assert.NotEmpty(t, sha)
		assert.Len(t, sha, 40)

		log := runGitCmd(t, tmpDir, "log", "--oneline", "-1")
		assert.Contains(t, log, "test commit")
	})

	t.Run("empty commit message", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		sha, err := g.Commit(t.Context(), "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "commit message cannot be empty")
		assert.Empty(t, sha)
	})

	t.Run("nothing to commit", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		sha, err := g.Commit(t.Context(), "test commit")
		require.Error(t, err)
		assert.Empty(t, sha)
	})
}

func TestGetFileContent(t *testing.T) {
	t.Parallel()
	t.Run("read existing file", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		createFile(t, tmpDir, "test.txt", "file content")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		content, err := g.GetFileContent(t.Context(), "test.txt")
		require.NoError(t, err)
		assert.Equal(t, "file content", content)
	})

	t.Run("read file in subdirectory", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0o750))
		createFile(t, tmpDir, "subdir/file.txt", "nested content")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		content, err := g.GetFileContent(t.Context(), "subdir/file.txt")
		require.NoError(t, err)
		assert.Equal(t, "nested content", content)
	})

	t.Run("file not found", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		content, err := g.GetFileContent(t.Context(), "nonexistent.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "file not found")
		assert.Empty(t, content)
	})

	t.Run("absolute path rejected", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		content, err := g.GetFileContent(t.Context(), "/etc/passwd")
		require.ErrorIs(t, err, ErrInvalidPath)
		assert.Empty(t, content)
	})

	t.Run("path traversal rejected", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		content, err := g.GetFileContent(t.Context(), "../../../etc/passwd")
		require.ErrorIs(t, err, ErrPathOutsideRepo)
		assert.Empty(t, content)
	})

	t.Run("symlink outside repo rejected", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

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
}

func TestGetRepoPath(t *testing.T) {
	t.Parallel()
	tmpDir := createTempGitRepo(t)
	defer cleanupTempDir(t, tmpDir)

	g, err := New(tmpDir, nil)
	require.NoError(t, err)

	assert.Equal(t, tmpDir, g.GetRepoPath())
}

func TestCheckGitRepo(t *testing.T) {
	t.Parallel()
	t.Run("valid git repo", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

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

	t.Run(".git is file not directory", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		createFile(t, tmpDir, ".git", "not a directory")
		assert.False(t, CheckGitRepo(tmpDir))
	})
}

func TestRunGitCommand(t *testing.T) {
	t.Parallel()
	t.Run("command timeout", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

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
		tmpDir := createTempGitRepo(t)
		defer cleanupTempDir(t, tmpDir)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		_, err = g.runGitCommand(t.Context(), "invalid-command")
		require.ErrorIs(t, err, ErrCommandFailed)
	})
}

func createTempGitRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	runGitCmd(t, tmpDir, "init")
	runGitCmd(t, tmpDir, "config", "user.email", "test@example.com")
	runGitCmd(t, tmpDir, "config", "user.name", "Test User")

	return tmpDir
}

func createFile(t *testing.T, dir, name, content string) {
	t.Helper()
	fullPath := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o750); err != nil {
		t.Fatalf("Failed to create directories: %v", err)
	}
	err := os.WriteFile(fullPath, []byte(content), 0o600)
	require.NoError(t, err)
}

func runGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git command failed: %v\nOutput: %s", err, output)
	}

	return strings.TrimSpace(string(output))
}

// cleanupTempDir is a helper function for test cleanup that handles RemoveAll errors.
func cleanupTempDir(t *testing.T, tmpDir string) {
	t.Helper()
	if err := os.RemoveAll(tmpDir); err != nil {
		t.Logf("Warning: failed to cleanup temp dir %s: %v", tmpDir, err)
	}
}
