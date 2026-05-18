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

package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"msrl.dev/lgtmcp/internal/config"
)

const testDiffGitHeader = "diff --git a/main.go b/main.go"

func TestManager_LoadPrompt(t *testing.T) {
	t.Parallel()

	t.Run("load default review prompt", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		prompt, err := m.LoadPrompt(ReviewPrompt)
		require.NoError(t, err)
		assert.Contains(t, prompt, "strict code reviewer")
		assert.Contains(t, prompt, "lgtm")
	})

	t.Run("load default context gathering prompt", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		prompt, err := m.LoadPrompt(ContextGatheringPrompt)
		require.NoError(t, err)
		assert.Contains(t, prompt, "analyzing code changes")
		assert.Contains(t, prompt, "get_file_content")
	})

	t.Run("load custom review prompt from file", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		customPromptPath := filepath.Join(tmpDir, "custom_review.md")
		customContent := "# Custom Review Prompt\n\nThis is a custom review prompt with {{.Diff}}"
		err := os.WriteFile(customPromptPath, []byte(customContent), 0o600)
		require.NoError(t, err)

		m := New(customPromptPath, "")
		m.SetConfigDir(tmpDir)
		prompt, err := m.LoadPrompt(ReviewPrompt)
		require.NoError(t, err)
		assert.Equal(t, customContent, prompt)
	})

	t.Run("error on non-existent custom file", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		missing := filepath.Join(tmpDir, "missing.md")
		m := New(missing, "")
		m.SetConfigDir(tmpDir)
		_, err := m.LoadPrompt(ReviewPrompt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read prompt file")
	})

	t.Run("unknown prompt type", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		_, err := m.LoadPrompt(PromptType("unknown"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown prompt type")
	})
}

func TestManager_BuildReviewPrompt(t *testing.T) {
	t.Parallel()

	t.Run("build review prompt with analysis", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		diff := testDiffGitHeader
		changedFiles := []string{"main.go", "test.go"}
		analysisText := "The code looks good overall"

		prompt, err := m.BuildReviewPrompt(diff, changedFiles, nil, analysisText, "")
		require.NoError(t, err)
		assert.Contains(t, prompt, diff)
		assert.Contains(t, prompt, "main.go")
		assert.Contains(t, prompt, "test.go")
		assert.Contains(t, prompt, analysisText)
		assert.Contains(t, prompt, "strict code reviewer")
	})

	t.Run("build review prompt without analysis", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		diff := testDiffGitHeader
		changedFiles := []string{"main.go"}

		prompt, err := m.BuildReviewPrompt(diff, changedFiles, nil, "", "")
		require.NoError(t, err)
		assert.Contains(t, prompt, diff)
		assert.Contains(t, prompt, "main.go")
		assert.NotContains(t, prompt, "Based on your previous analysis")
	})

	t.Run("build with custom template", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		customPromptPath := filepath.Join(tmpDir, "custom.md")
		customContent := "Custom: {{.Diff}} Files: {{.FilesList}}"
		err := os.WriteFile(customPromptPath, []byte(customContent), 0o600)
		require.NoError(t, err)

		m := New(customPromptPath, "")
		m.SetConfigDir(tmpDir)
		prompt, err := m.BuildReviewPrompt("test diff", []string{"file1.go"}, nil, "", "")
		require.NoError(t, err)
		assert.Contains(t, prompt, "Custom: test diff")
		assert.Contains(t, prompt, "Files: file1.go")
	})

	t.Run("invalid template syntax", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		customPromptPath := filepath.Join(tmpDir, "invalid.md")
		customContent := "Invalid: {{.UnknownField"
		err := os.WriteFile(customPromptPath, []byte(customContent), 0o600)
		require.NoError(t, err)

		m := New(customPromptPath, "")
		m.SetConfigDir(tmpDir)
		_, err = m.BuildReviewPrompt("test", []string{"file.go"}, nil, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse review prompt template")
	})
}

func TestManager_BuildContextGatheringPrompt(t *testing.T) {
	t.Parallel()

	t.Run("build context gathering prompt", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		diff := testDiffGitHeader
		changedFiles := []string{"main.go", "lib.go"}

		prompt, err := m.BuildContextGatheringPrompt(diff, changedFiles, nil, "")
		require.NoError(t, err)
		assert.Contains(t, prompt, diff)
		assert.Contains(t, prompt, "main.go")
		assert.Contains(t, prompt, "lib.go")
		assert.Contains(t, prompt, "analyzing code changes")
	})

	t.Run("build with custom template", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		customPromptPath := filepath.Join(tmpDir, "custom_context.md")
		customContent := "Analyze: {{.Diff}} in {{.FilesList}}"
		err := os.WriteFile(customPromptPath, []byte(customContent), 0o600)
		require.NoError(t, err)

		m := New("", customPromptPath)
		m.SetConfigDir(tmpDir)
		prompt, err := m.BuildContextGatheringPrompt("test diff", []string{"file1.go", "file2.go"}, nil, "")
		require.NoError(t, err)
		assert.Contains(t, prompt, "Analyze: test diff")
		assert.Contains(t, prompt, "file1.go")
	})
}

func TestManager_BuildReviewPromptWithInstructions(t *testing.T) {
	t.Parallel()

	t.Run("with agent instructions", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		diff := testDiffGitHeader
		changedFiles := []string{"main.go"}
		instructions := "## Agent Instructions\n\nAlways check for tests."

		prompt, err := m.BuildReviewPrompt(diff, changedFiles, nil, "", instructions)
		require.NoError(t, err)
		assert.Contains(t, prompt, "Agent Instructions")
		assert.Contains(t, prompt, "Always check for tests")
	})

	t.Run("without agent instructions", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		diff := testDiffGitHeader
		changedFiles := []string{"main.go"}

		prompt, err := m.BuildReviewPrompt(diff, changedFiles, nil, "", "")
		require.NoError(t, err)
		assert.NotContains(t, prompt, "Agent Instructions")
	})
}

func TestManager_BuildContextGatheringPromptWithInstructions(t *testing.T) {
	t.Parallel()

	t.Run("with agent instructions", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		diff := testDiffGitHeader
		changedFiles := []string{"main.go"}
		instructions := "## Agent Instructions\n\nCheck security carefully."

		prompt, err := m.BuildContextGatheringPrompt(diff, changedFiles, nil, instructions)
		require.NoError(t, err)
		assert.Contains(t, prompt, "Agent Instructions")
		assert.Contains(t, prompt, "Check security carefully")
	})

	t.Run("without agent instructions", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		diff := testDiffGitHeader
		changedFiles := []string{"main.go"}

		prompt, err := m.BuildContextGatheringPrompt(diff, changedFiles, nil, "")
		require.NoError(t, err)
		assert.NotContains(t, prompt, "Agent Instructions")
	})
}

func TestPromptData(t *testing.T) {
	t.Parallel()

	t.Run("ReviewPromptData fields", func(t *testing.T) {
		t.Parallel()
		data := ReviewPromptData{
			AnalysisSection:     "analysis",
			InstructionsSection: "agents",
			FilesList:           "files",
			Diff:                "diff",
		}
		assert.Equal(t, "analysis", data.AnalysisSection)
		assert.Equal(t, "agents", data.InstructionsSection)
		assert.Equal(t, "files", data.FilesList)
		assert.Equal(t, "diff", data.Diff)
	})

	t.Run("ContextGatheringPromptData fields", func(t *testing.T) {
		t.Parallel()
		data := ContextGatheringPromptData{
			InstructionsSection: "agents",
			FilesList:           "files",
			Diff:                "diff",
		}
		assert.Equal(t, "agents", data.InstructionsSection)
		assert.Equal(t, "files", data.FilesList)
		assert.Equal(t, "diff", data.Diff)
	})
}

func TestBuildReviewPrompt_LoadPromptError(t *testing.T) {
	t.Parallel()
	m := New("/nonexistent/review.md", "")
	_, err := m.BuildReviewPrompt("diff", []string{"file.go"}, nil, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load review prompt")
}

func TestBuildContextGatheringPrompt_LoadPromptError(t *testing.T) {
	t.Parallel()
	m := New("", "/nonexistent/context.md")
	_, err := m.BuildContextGatheringPrompt("diff", []string{"file.go"}, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load context gathering prompt")
}

func TestBuildContextGatheringPrompt_TemplateParseError(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	customPromptPath := filepath.Join(tmpDir, "broken.md")
	err := os.WriteFile(customPromptPath, []byte("{{.Broken"), 0o600)
	require.NoError(t, err)

	m := New("", customPromptPath)
	m.SetConfigDir(tmpDir)
	_, err = m.BuildContextGatheringPrompt("diff", []string{"file.go"}, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse context gathering prompt template")
}

func TestBuildContextGatheringPrompt_TemplateExecutionError(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	customPromptPath := filepath.Join(tmpDir, "bad_exec.md")
	// A template function that references a method that doesn't exist on the data struct.
	err := os.WriteFile(customPromptPath, []byte("{{.Diff.Missing}}"), 0o600)
	require.NoError(t, err)

	m := New("", customPromptPath)
	m.SetConfigDir(tmpDir)
	_, err = m.BuildContextGatheringPrompt("diff", []string{"file.go"}, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute context gathering prompt template")
}

func TestLoadPrompt_CustomContextGatheringPath_NotFound(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	missing := filepath.Join(tmpDir, "missing.md")
	m := New("", missing)
	m.SetConfigDir(tmpDir)
	_, err := m.LoadPrompt(ContextGatheringPrompt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read prompt file")
}

func TestBuildReviewPrompt_TemplateExecutionError(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	customPromptPath := filepath.Join(tmpDir, "bad_exec.md")
	err := os.WriteFile(customPromptPath, []byte("{{.Diff.Missing}}"), 0o600)
	require.NoError(t, err)

	m := New(customPromptPath, "")
	m.SetConfigDir(tmpDir)
	_, err = m.BuildReviewPrompt("diff", []string{"file.go"}, nil, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to execute review prompt template")
}

func TestLoadPrompt_PathValidation(t *testing.T) {
	t.Parallel()

	t.Run("rejects parent-directory traversal", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		m := New("../../../etc/passwd", "")
		m.SetConfigDir(tmpDir)
		_, err := m.LoadPrompt(ReviewPrompt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid prompt path")
		require.ErrorIs(t, err, config.ErrPathTraversal)
	})

	t.Run("rejects absolute path outside config dir", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		// /etc/passwd is well outside the per-test temp dir.
		evil := filepath.Join(string(filepath.Separator), "etc", "passwd")
		m := New(evil, "")
		m.SetConfigDir(tmpDir)
		_, err := m.LoadPrompt(ReviewPrompt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid prompt path")
		require.ErrorIs(t, err, config.ErrPathOutsideBase)
	})

	t.Run("accepts absolute path under config dir", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		promptPath := filepath.Join(tmpDir, "review.md")
		require.NoError(t, os.WriteFile(promptPath, []byte("ok"), 0o600))
		m := New(promptPath, "")
		m.SetConfigDir(tmpDir)
		got, err := m.LoadPrompt(ReviewPrompt)
		require.NoError(t, err)
		assert.Equal(t, "ok", got)
	})

	t.Run("relative path resolves against config dir", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "review.md"), []byte("relative-ok"), 0o600))
		m := New("review.md", "")
		m.SetConfigDir(tmpDir)
		got, err := m.LoadPrompt(ReviewPrompt)
		require.NoError(t, err)
		assert.Equal(t, "relative-ok", got)
	})

	t.Run("validation also applies to context gathering prompt", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		evil := filepath.Join(string(filepath.Separator), "etc", "shadow")
		m := New("", evil)
		m.SetConfigDir(tmpDir)
		_, err := m.LoadPrompt(ContextGatheringPrompt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid prompt path")
	})
}

func TestEmbeddedPrompts(t *testing.T) {
	t.Parallel()

	t.Run("default review prompt is embedded", func(t *testing.T) {
		t.Parallel()
		assert.NotEmpty(t, defaultReviewPrompt)
		assert.Contains(t, defaultReviewPrompt, "{{.Diff}}")
		assert.Contains(t, defaultReviewPrompt, ".ExistingFilesList")
		assert.Contains(t, defaultReviewPrompt, ".DeletedFilesList")
		assert.Contains(t, defaultReviewPrompt, ".InstructionsSection")
	})

	t.Run("default context gathering prompt is embedded", func(t *testing.T) {
		t.Parallel()
		assert.NotEmpty(t, defaultContextGatheringPrompt)
		assert.Contains(t, defaultContextGatheringPrompt, "{{.Diff}}")
		assert.Contains(t, defaultContextGatheringPrompt, ".ExistingFilesList")
		assert.Contains(t, defaultContextGatheringPrompt, ".DeletedFilesList")
		assert.Contains(t, defaultContextGatheringPrompt, ".InstructionsSection")
	})
}

// TestBuildPrompts_DeletedFileSections covers the new behavior: deletions go
// into a dedicated section so the model knows not to call get_file_content
// for them, and the existing-files section is suppressed entirely when the
// diff only deletes files.
func TestBuildPrompts_DeletedFileSections(t *testing.T) {
	t.Parallel()

	t.Run("review prompt with only existing files omits deleted section", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		prompt, err := m.BuildReviewPrompt("diff", []string{"keep.go"}, nil, "", "")
		require.NoError(t, err)
		assert.Contains(t, prompt, "Files changed in this diff")
		assert.Contains(t, prompt, "keep.go")
		assert.NotContains(t, prompt, "Files deleted by this change")
	})

	t.Run("review prompt with only deletions omits changed section", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		prompt, err := m.BuildReviewPrompt("diff", []string{"gone.go"}, []string{"gone.go"}, "", "")
		require.NoError(t, err)
		assert.NotContains(t, prompt, "Files changed in this diff")
		assert.Contains(t, prompt, "Files deleted by this change")
		assert.Contains(t, prompt, "gone.go")
	})

	t.Run("review prompt with both kinds renders both sections", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		prompt, err := m.BuildReviewPrompt(
			"diff", []string{"keep.go", "gone.go"}, []string{"gone.go"}, "", "",
		)
		require.NoError(t, err)
		existingIdx := strings.Index(prompt, "Files changed in this diff")
		deletedIdx := strings.Index(prompt, "Files deleted by this change")
		require.NotEqual(t, -1, existingIdx, "expected existing section in prompt")
		require.NotEqual(t, -1, deletedIdx, "expected deleted section in prompt")
		assert.Less(t, existingIdx, deletedIdx, "existing section must precede deleted section")
		// keep.go appears in the existing section, gone.go in the deleted section.
		assert.Contains(t, prompt[existingIdx:deletedIdx], "keep.go")
		assert.NotContains(t, prompt[existingIdx:deletedIdx], "gone.go")
		assert.Contains(t, prompt[deletedIdx:], "gone.go")
	})

	t.Run("context gathering prompt with only existing files omits deleted section", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		prompt, err := m.BuildContextGatheringPrompt("diff", []string{"keep.go"}, nil, "")
		require.NoError(t, err)
		assert.Contains(t, prompt, "Files changed in this diff")
		assert.NotContains(t, prompt, "Files deleted by this change")
	})

	t.Run("context gathering prompt with deletions includes warning not to fetch them", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		prompt, err := m.BuildContextGatheringPrompt(
			"diff", []string{"keep.go", "gone.go"}, []string{"gone.go"}, "",
		)
		require.NoError(t, err)
		assert.Contains(t, prompt, "Files deleted by this change")
		assert.Contains(t, prompt, "do not call get_file_content")
		assert.Contains(t, prompt, "gone.go")
	})

	t.Run("custom template using FilesList still receives full list", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		customPromptPath := filepath.Join(tmpDir, "custom.md")
		// Older custom templates only know about {{.FilesList}}; verify
		// they keep working and see deletions alongside other changes.
		err := os.WriteFile(customPromptPath, []byte("Files: {{.FilesList}}"), 0o600)
		require.NoError(t, err)

		m := New(customPromptPath, "")
		m.SetConfigDir(tmpDir)
		prompt, err := m.BuildReviewPrompt(
			"diff", []string{"keep.go", "gone.go"}, []string{"gone.go"}, "", "",
		)
		require.NoError(t, err)
		assert.Contains(t, prompt, "keep.go")
		assert.Contains(t, prompt, "gone.go")
	})

	t.Run("splitFiles preserves changedFiles order in both outputs", func(t *testing.T) {
		t.Parallel()
		existing, deleted := splitFiles(
			[]string{"a.go", "b.go", "c.go", "d.go"},
			[]string{"b.go", "d.go"},
		)
		assert.Equal(t, []string{"a.go", "c.go"}, existing)
		assert.Equal(t, []string{"b.go", "d.go"}, deleted)
	})

	t.Run("splitFiles with empty deletions returns full list", func(t *testing.T) {
		t.Parallel()
		existing, deleted := splitFiles([]string{"a.go", "b.go"}, nil)
		assert.Equal(t, []string{"a.go", "b.go"}, existing)
		assert.Empty(t, deleted)
	})
}
