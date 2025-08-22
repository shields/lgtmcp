package prompts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		err := os.WriteFile(customPromptPath, []byte(customContent), 0o644)
		require.NoError(t, err)

		m := New(customPromptPath, "")
		prompt, err := m.LoadPrompt(ReviewPrompt)
		require.NoError(t, err)
		assert.Equal(t, customContent, prompt)
	})

	t.Run("error on non-existent custom file", func(t *testing.T) {
		t.Parallel()
		m := New("/non/existent/file.md", "")
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
		diff := "diff --git a/main.go b/main.go"
		changedFiles := []string{"main.go", "test.go"}
		analysisText := "The code looks good overall"

		prompt, err := m.BuildReviewPrompt(diff, changedFiles, analysisText)
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
		diff := "diff --git a/main.go b/main.go"
		changedFiles := []string{"main.go"}

		prompt, err := m.BuildReviewPrompt(diff, changedFiles, "")
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
		err := os.WriteFile(customPromptPath, []byte(customContent), 0o644)
		require.NoError(t, err)

		m := New(customPromptPath, "")
		prompt, err := m.BuildReviewPrompt("test diff", []string{"file1.go"}, "")
		require.NoError(t, err)
		assert.Contains(t, prompt, "Custom: test diff")
		assert.Contains(t, prompt, "Files: file1.go")
	})

	t.Run("invalid template syntax", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		customPromptPath := filepath.Join(tmpDir, "invalid.md")
		customContent := "Invalid: {{.UnknownField"
		err := os.WriteFile(customPromptPath, []byte(customContent), 0o644)
		require.NoError(t, err)

		m := New(customPromptPath, "")
		_, err = m.BuildReviewPrompt("test", []string{"file.go"}, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse review prompt template")
	})
}

func TestManager_BuildContextGatheringPrompt(t *testing.T) {
	t.Parallel()

	t.Run("build context gathering prompt", func(t *testing.T) {
		t.Parallel()
		m := New("", "")
		diff := "diff --git a/main.go b/main.go"
		changedFiles := []string{"main.go", "lib.go"}

		prompt, err := m.BuildContextGatheringPrompt(diff, changedFiles)
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
		err := os.WriteFile(customPromptPath, []byte(customContent), 0o644)
		require.NoError(t, err)

		m := New("", customPromptPath)
		prompt, err := m.BuildContextGatheringPrompt("test diff", []string{"file1.go", "file2.go"})
		require.NoError(t, err)
		assert.Contains(t, prompt, "Analyze: test diff")
		assert.Contains(t, prompt, "file1.go")
	})
}

func TestPromptData(t *testing.T) {
	t.Parallel()

	t.Run("ReviewPromptData fields", func(t *testing.T) {
		t.Parallel()
		data := ReviewPromptData{
			AnalysisSection: "analysis",
			FilesList:       "files",
			Diff:            "diff",
		}
		assert.Equal(t, "analysis", data.AnalysisSection)
		assert.Equal(t, "files", data.FilesList)
		assert.Equal(t, "diff", data.Diff)
	})

	t.Run("ContextGatheringPromptData fields", func(t *testing.T) {
		t.Parallel()
		data := ContextGatheringPromptData{
			FilesList: "files",
			Diff:      "diff",
		}
		assert.Equal(t, "files", data.FilesList)
		assert.Equal(t, "diff", data.Diff)
	})
}

func TestEmbeddedPrompts(t *testing.T) {
	t.Parallel()

	t.Run("default review prompt is embedded", func(t *testing.T) {
		t.Parallel()
		assert.NotEmpty(t, defaultReviewPrompt)
		assert.Contains(t, defaultReviewPrompt, "{{.Diff}}")
		assert.Contains(t, defaultReviewPrompt, "{{.FilesList}}")
	})

	t.Run("default context gathering prompt is embedded", func(t *testing.T) {
		t.Parallel()
		assert.NotEmpty(t, defaultContextGatheringPrompt)
		assert.Contains(t, defaultContextGatheringPrompt, "{{.Diff}}")
		assert.Contains(t, defaultContextGatheringPrompt, "{{.FilesList}}")
	})
}
