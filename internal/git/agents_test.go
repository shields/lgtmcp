package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindAgentFiles(t *testing.T) {
	t.Parallel()

	t.Run("no AGENTS.md files", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		createFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"main.go"})
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("root AGENTS.md only", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		createFile(t, tmpDir, "AGENTS.md", "Root instructions")
		createFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"main.go"})
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, "AGENTS.md", files[0].Path)
		assert.Equal(t, "Root instructions", files[0].Content)
	})

	t.Run("nested AGENTS.md only", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		createFile(t, tmpDir, "src/AGENTS.md", "Src instructions")
		createFile(t, tmpDir, "src/main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"src/main.go"})
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, filepath.Join("src", "AGENTS.md"), files[0].Path)
		assert.Equal(t, "Src instructions", files[0].Content)
	})

	t.Run("root and nested AGENTS.md", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		createFile(t, tmpDir, "AGENTS.md", "Root instructions")
		createFile(t, tmpDir, "src/AGENTS.md", "Src instructions")
		createFile(t, tmpDir, "src/main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"src/main.go"})
		require.NoError(t, err)
		require.Len(t, files, 2)
		// Root should come first (fewer separators).
		assert.Equal(t, "AGENTS.md", files[0].Path)
		assert.Equal(t, "Root instructions", files[0].Content)
		assert.Equal(t, filepath.Join("src", "AGENTS.md"), files[1].Path)
		assert.Equal(t, "Src instructions", files[1].Content)
	})

	t.Run("multiple changed files dedup ancestors", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		createFile(t, tmpDir, "AGENTS.md", "Root instructions")
		createFile(t, tmpDir, "src/a.go", "package src")
		createFile(t, tmpDir, "src/b.go", "package src")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"src/a.go", "src/b.go"})
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, "AGENTS.md", files[0].Path)
	})

	t.Run("deep nesting walks up to root", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		createFile(t, tmpDir, "AGENTS.md", "Root instructions")
		createFile(t, tmpDir, "a/b/c/d/file.go", "package d")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"a/b/c/d/file.go"})
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, "AGENTS.md", files[0].Path)
	})

	t.Run("root-level changed file", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		createFile(t, tmpDir, "AGENTS.md", "Root instructions")
		createFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"main.go"})
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, "AGENTS.md", files[0].Path)
	})

	t.Run("symlink outside repo is skipped", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		// Create an AGENTS.md outside the repo.
		outsideDir := t.TempDir()
		createFile(t, outsideDir, "AGENTS.md", "Evil instructions")

		// Create a symlink to it.
		err := os.Symlink(
			filepath.Join(outsideDir, "AGENTS.md"),
			filepath.Join(tmpDir, "AGENTS.md"),
		)
		require.NoError(t, err)

		createFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"main.go"})
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("directory named AGENTS.md is skipped", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		// Create a directory named AGENTS.md.
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "AGENTS.md"), 0o750))
		createFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"main.go"})
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("empty changed files", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{})
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("nil changed files", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles(nil)
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("oversized AGENTS.md is skipped", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		// Create an AGENTS.md larger than maxAgentFileSize (50KB).
		largeContent := strings.Repeat("x", maxAgentFileSize+1)
		createFile(t, tmpDir, "AGENTS.md", largeContent)
		createFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"main.go"})
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("depth sorting with multiple directories", func(t *testing.T) {
		t.Parallel()
		tmpDir := createTempGitRepo(t)

		createFile(t, tmpDir, "AGENTS.md", "Root")
		createFile(t, tmpDir, "a/AGENTS.md", "Level 1 a")
		createFile(t, tmpDir, "b/AGENTS.md", "Level 1 b")
		createFile(t, tmpDir, "a/sub/AGENTS.md", "Level 2")
		createFile(t, tmpDir, "a/sub/file.go", "package sub")
		createFile(t, tmpDir, "b/file.go", "package b")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"a/sub/file.go", "b/file.go"})
		require.NoError(t, err)
		require.Len(t, files, 4)

		// Root first, then level-1 alphabetically, then level-2.
		assert.Equal(t, "AGENTS.md", files[0].Path)
		assert.Equal(t, filepath.Join("a", "AGENTS.md"), files[1].Path)
		assert.Equal(t, filepath.Join("b", "AGENTS.md"), files[2].Path)
		assert.Equal(t, filepath.Join("a", "sub", "AGENTS.md"), files[3].Path)
	})
}

func TestFormatAgentInstructions(t *testing.T) {
	t.Parallel()

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		result := FormatAgentInstructions(nil)
		assert.Empty(t, result)
	})

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()
		result := FormatAgentInstructions([]AgentFile{})
		assert.Empty(t, result)
	})

	t.Run("single file", func(t *testing.T) {
		t.Parallel()
		files := []AgentFile{
			{Path: "AGENTS.md", Content: "Review carefully"},
		}
		result := FormatAgentInstructions(files)
		assert.Contains(t, result, "Repository Agent Instructions")
		assert.Contains(t, result, "### AGENTS.md")
		assert.Contains(t, result, "Review carefully")
	})

	t.Run("multiple files", func(t *testing.T) {
		t.Parallel()
		files := []AgentFile{
			{Path: "AGENTS.md", Content: "Root rules"},
			{Path: "src/AGENTS.md", Content: "Src rules"},
		}
		result := FormatAgentInstructions(files)
		assert.Contains(t, result, "### AGENTS.md")
		assert.Contains(t, result, "Root rules")
		assert.Contains(t, result, "### src/AGENTS.md")
		assert.Contains(t, result, "Src rules")
	})

	t.Run("trims whitespace from content", func(t *testing.T) {
		t.Parallel()
		files := []AgentFile{
			{Path: "AGENTS.md", Content: "\n  Instructions with whitespace  \n\n"},
		}
		result := FormatAgentInstructions(files)
		assert.Contains(t, result, "Instructions with whitespace")
		assert.NotContains(t, result, "\n  Instructions")
	})
}
