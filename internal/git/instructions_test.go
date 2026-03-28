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

package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"msrl.dev/lgtmcp/internal/testutil"
)

func TestFindAgentFiles(t *testing.T) {
	t.Parallel()

	t.Run("no AGENTS.md files", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"main.go"})
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("root AGENTS.md only", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "AGENTS.md", "Root instructions")
		testutil.CreateFile(t, tmpDir, "main.go", "package main")

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
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "src/AGENTS.md", "Src instructions")
		testutil.CreateFile(t, tmpDir, "src/main.go", "package main")

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
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "AGENTS.md", "Root instructions")
		testutil.CreateFile(t, tmpDir, "src/AGENTS.md", "Src instructions")
		testutil.CreateFile(t, tmpDir, "src/main.go", "package main")

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
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "AGENTS.md", "Root instructions")
		testutil.CreateFile(t, tmpDir, "src/a.go", "package src")
		testutil.CreateFile(t, tmpDir, "src/b.go", "package src")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"src/a.go", "src/b.go"})
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, "AGENTS.md", files[0].Path)
	})

	t.Run("deep nesting walks up to root", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "AGENTS.md", "Root instructions")
		testutil.CreateFile(t, tmpDir, "a/b/c/d/file.go", "package d")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"a/b/c/d/file.go"})
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, "AGENTS.md", files[0].Path)
	})

	t.Run("root-level changed file", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "AGENTS.md", "Root instructions")
		testutil.CreateFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"main.go"})
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, "AGENTS.md", files[0].Path)
	})

	t.Run("symlink outside repo is skipped", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// Create an AGENTS.md outside the repo.
		outsideDir := t.TempDir()
		testutil.CreateFile(t, outsideDir, "AGENTS.md", "Evil instructions")

		// Create a symlink to it.
		err := os.Symlink(
			filepath.Join(outsideDir, "AGENTS.md"),
			filepath.Join(tmpDir, "AGENTS.md"),
		)
		require.NoError(t, err)

		testutil.CreateFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"main.go"})
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("directory named AGENTS.md is skipped", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// Create a directory named AGENTS.md.
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "AGENTS.md"), 0o750))
		testutil.CreateFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"main.go"})
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("empty changed files", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{})
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("nil changed files", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles(nil)
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("oversized AGENTS.md is skipped", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// Create an AGENTS.md larger than maxInstructionFileSize (50KB).
		largeContent := strings.Repeat("x", maxInstructionFileSize+1)
		testutil.CreateFile(t, tmpDir, "AGENTS.md", largeContent)
		testutil.CreateFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindAgentFiles([]string{"main.go"})
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("depth sorting with multiple directories", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "AGENTS.md", "Root")
		testutil.CreateFile(t, tmpDir, "a/AGENTS.md", "Level 1 a")
		testutil.CreateFile(t, tmpDir, "b/AGENTS.md", "Level 1 b")
		testutil.CreateFile(t, tmpDir, "a/sub/AGENTS.md", "Level 2")
		testutil.CreateFile(t, tmpDir, "a/sub/file.go", "package sub")
		testutil.CreateFile(t, tmpDir, "b/file.go", "package b")

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
		result := FormatAgentInstructions([]InstructionFile{})
		assert.Empty(t, result)
	})

	t.Run("single file", func(t *testing.T) {
		t.Parallel()
		files := []InstructionFile{
			{Path: "AGENTS.md", Content: "Review carefully"},
		}
		result := FormatAgentInstructions(files)
		assert.Contains(t, result, "Repository Agent Instructions")
		assert.Contains(t, result, "### AGENTS.md")
		assert.Contains(t, result, "Review carefully")
	})

	t.Run("multiple files", func(t *testing.T) {
		t.Parallel()
		files := []InstructionFile{
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
		files := []InstructionFile{
			{Path: "AGENTS.md", Content: "\n  Instructions with whitespace  \n\n"},
		}
		result := FormatAgentInstructions(files)
		assert.Contains(t, result, "Instructions with whitespace")
		assert.NotContains(t, result, "\n  Instructions")
	})
}

func TestFindReviewFiles(t *testing.T) {
	t.Parallel()

	t.Run("no REVIEW.md files", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindReviewFiles([]string{"main.go"})
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("root REVIEW.md only", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "REVIEW.md", "Review guidelines")
		testutil.CreateFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindReviewFiles([]string{"main.go"})
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, "REVIEW.md", files[0].Path)
		assert.Equal(t, "Review guidelines", files[0].Content)
	})

	t.Run("nested REVIEW.md only", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "src/REVIEW.md", "Src review guidelines")
		testutil.CreateFile(t, tmpDir, "src/main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindReviewFiles([]string{"src/main.go"})
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, filepath.Join("src", "REVIEW.md"), files[0].Path)
		assert.Equal(t, "Src review guidelines", files[0].Content)
	})

	t.Run("coexists with AGENTS.md independently", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		testutil.CreateFile(t, tmpDir, "AGENTS.md", "Agent instructions")
		testutil.CreateFile(t, tmpDir, "REVIEW.md", "Review guidelines")
		testutil.CreateFile(t, tmpDir, "main.go", "package main")

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		agentFiles, err := g.FindAgentFiles([]string{"main.go"})
		require.NoError(t, err)
		require.Len(t, agentFiles, 1)
		assert.Equal(t, "AGENTS.md", agentFiles[0].Path)

		reviewFiles, err := g.FindReviewFiles([]string{"main.go"})
		require.NoError(t, err)
		require.Len(t, reviewFiles, 1)
		assert.Equal(t, "REVIEW.md", reviewFiles[0].Path)
	})

	t.Run("empty changed files", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		g, err := New(tmpDir, nil)
		require.NoError(t, err)

		files, err := g.FindReviewFiles([]string{})
		require.NoError(t, err)
		assert.Nil(t, files)
	})
}

func TestFormatReviewInstructions(t *testing.T) {
	t.Parallel()

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		result := FormatReviewInstructions(nil)
		assert.Empty(t, result)
	})

	t.Run("single file", func(t *testing.T) {
		t.Parallel()
		files := []InstructionFile{
			{Path: "REVIEW.md", Content: "Check for tests"},
		}
		result := FormatReviewInstructions(files)
		assert.Contains(t, result, "Repository Review Instructions")
		assert.Contains(t, result, "### REVIEW.md")
		assert.Contains(t, result, "Check for tests")
	})

	t.Run("multiple files", func(t *testing.T) {
		t.Parallel()
		files := []InstructionFile{
			{Path: "REVIEW.md", Content: "Root review rules"},
			{Path: "src/REVIEW.md", Content: "Src review rules"},
		}
		result := FormatReviewInstructions(files)
		assert.Contains(t, result, "### REVIEW.md")
		assert.Contains(t, result, "Root review rules")
		assert.Contains(t, result, "### src/REVIEW.md")
		assert.Contains(t, result, "Src review rules")
	})
}
