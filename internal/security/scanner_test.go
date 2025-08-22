package security

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zricethezav/gitleaks/v8/report"
)

var fakeSecrets = FakeSecrets{}

func TestNew(t *testing.T) {
	t.Parallel()
	t.Run("default config", func(t *testing.T) {
		t.Parallel()
		scanner, err := New("")
		require.NoError(t, err)
		assert.NotNil(t, scanner)
		assert.NotNil(t, scanner.detector)
	})

	t.Run("custom config file not supported", func(t *testing.T) {
		t.Parallel()
		// Create a temporary config file.
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, ".gitleaks.toml")
		configContent := `
title = "test config"
[[rules]]
id = "test-rule"
description = "Test rule"
regex = '''(?i)test_secret'''
`
		err := os.WriteFile(configPath, []byte(configContent), 0o644)
		require.NoError(t, err)

		scanner, err := New(configPath)
		require.Error(t, err)
		assert.Nil(t, scanner)
		assert.Contains(t, err.Error(), "custom config not supported")
	})

	t.Run("invalid config file", func(t *testing.T) {
		t.Parallel()
		scanner, err := New("/nonexistent/config.toml")
		require.Error(t, err)
		assert.Nil(t, scanner)
		assert.Contains(t, err.Error(), "custom config not supported")
	})

	t.Run("malformed config file", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, ".gitleaks.toml")
		err := os.WriteFile(configPath, []byte("invalid toml content {{"), 0o644)
		require.NoError(t, err)

		scanner, err := New(configPath)
		require.Error(t, err)
		assert.Nil(t, scanner)
		assert.Contains(t, err.Error(), "custom config not supported")
	})
}

func TestScanDiff(t *testing.T) {
	t.Parallel()
	scanner, err := New("")
	require.NoError(t, err)

	t.Run("empty diff", func(t *testing.T) {
		t.Parallel()
		getFileContent := func(_ string) (string, error) {
			return "", nil
		}
		findings, err := scanner.ScanDiff(t.Context(), "", getFileContent)
		require.NoError(t, err)
		assert.Empty(t, findings)
	})

	t.Run("diff with no secrets", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/file.txt b/file.txt
index 0000000..1111111 100644
--- a/file.txt
+++ b/file.txt
@@ -1,3 +1,3 @@
 This is a test file
-with some content
+with modified content
 and no secrets`

		getFileContent := func(path string) (string, error) {
			if path == "file.txt" {
				return "This is a test file\nwith modified content\nand no secrets", nil
			}

			return "", os.ErrNotExist
		}

		findings, err := scanner.ScanDiff(t.Context(), diff, getFileContent)
		require.NoError(t, err)
		assert.Empty(t, findings)
	})

	t.Run("diff with GitHub token", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/config.txt b/config.txt
index 0000000..1111111 100644
--- a/config.txt
+++ b/config.txt
@@ -1,2 +1,3 @@
 config:
   setting: value
+  token: ` + fakeSecrets.GitHubPAT() + "`"

		getFileContent := func(path string) (string, error) {
			if path == "config.txt" {
				return "config:\n  setting: value\n  token: " + fakeSecrets.GitHubPAT(), nil
			}

			return "", os.ErrNotExist
		}

		findings, err := scanner.ScanDiff(t.Context(), diff, getFileContent)
		require.NoError(t, err)
		// Should detect GitHub token in the actual file content.
		assert.NotEmpty(t, findings)
		if len(findings) > 0 {
			assert.Equal(t, "config.txt", findings[0].File)
		}
	})

	t.Run("diff with private key", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/app.js b/app.js
+const privateKey = "` + fakeSecrets.FullPrivateKey() + `";`

		getFileContent := func(path string) (string, error) {
			if path == "app.js" {
				return `const privateKey = "` + fakeSecrets.FullPrivateKey() + `";`, nil
			}

			return "", os.ErrNotExist
		}

		findings, err := scanner.ScanDiff(t.Context(), diff, getFileContent)
		require.NoError(t, err)
		// Should detect private key pattern.
		assert.NotEmpty(t, findings)
	})

	t.Run("file not found", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/deleted.txt b/deleted.txt
deleted file mode 100644
index 1234567..0000000
--- a/deleted.txt
+++ /dev/null`

		getFileContent := func(_ string) (string, error) {
			return "", os.ErrNotExist
		}

		findings, err := scanner.ScanDiff(t.Context(), diff, getFileContent)
		require.NoError(t, err)
		assert.Empty(t, findings) // Deleted files are skipped.
	})

	t.Run("skip go.sum file", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/go.sum b/go.sum
index 0000000..1111111 100644
--- a/go.sum
+++ b/go.sum
@@ -1 +1 @@
-old checksum
+cloud.google.com/go/auth v0.15.0 h1:Ly0u4aA5vG/fsSsxu98qCQBemXtAtJf+95z9HK+cxps= # gitleaks:allow
diff --git a/main.go b/main.go
index 0000000..2222222 100644
--- a/main.go
+++ b/main.go
@@ -1 +1 @@
-old code
+token := "` + fakeSecrets.GitHubPAT() + `"`

		getFileContent := func(path string) (string, error) {
			switch path {
			case "go.sum":
				// This looks like an API key but it's actually a checksum.
				return "cloud.google.com/go/auth v0.15.0 h1:Ly0u4aA5vG/fsSsxu98qCQBemXtAtJf+95z9HK+cxps=", nil // gitleaks:allow
			case "main.go":
				return `token := "` + fakeSecrets.GitHubPAT() + `"`, nil
			default:
				return "", os.ErrNotExist
			}
		}

		findings, err := scanner.ScanDiff(t.Context(), diff, getFileContent)
		require.NoError(t, err)
		// Should find the GitHub token in main.go but not the checksum in go.sum.
		assert.Len(t, findings, 2) // GitHub PAT triggers both github-pat and generic-api-key rules.
		for _, finding := range findings {
			assert.Equal(t, "main.go", finding.File)
			assert.NotEqual(t, "go.sum", finding.File)
		}
	})

	t.Run("multiple files in diff", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/file1.txt b/file1.txt
index 0000000..1111111 100644
--- a/file1.txt
+++ b/file1.txt
@@ -1 +1 @@
-old content
+new content
diff --git a/file2.txt b/file2.txt
index 0000000..2222222 100644
--- a/file2.txt
+++ b/file2.txt
@@ -1 +1 @@
-old
+token=` + fakeSecrets.GitHubPAT() + "`"

		getFileContent := func(path string) (string, error) {
			switch path {
			case "file1.txt":
				return "new content", nil
			case "file2.txt":
				return `token=` + fakeSecrets.GitHubPAT(), nil
			default:
				return "", os.ErrNotExist
			}
		}

		findings, err := scanner.ScanDiff(t.Context(), diff, getFileContent)
		require.NoError(t, err)
		assert.NotEmpty(t, findings) // Should find GitHub token in file2.txt.
	})
}

func TestExtractChangedFiles(t *testing.T) {
	t.Parallel()
	t.Run("empty diff", func(t *testing.T) {
		t.Parallel()
		files := ExtractChangedFiles("")
		assert.Empty(t, files)
	})

	t.Run("single file diff", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/file.txt b/file.txt
index 0000000..1111111 100644
--- a/file.txt
+++ b/file.txt
@@ -1 +1 @@
-old
+new`
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"file.txt"}, files)
	})

	t.Run("multiple files diff", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/file1.txt b/file1.txt
index 0000000..1111111 100644
--- a/file1.txt
+++ b/file1.txt
@@ -1 +1 @@
-old
+new
diff --git a/dir/file2.go b/dir/file2.go
index 0000000..2222222 100644
--- a/dir/file2.go
+++ b/dir/file2.go
@@ -1 +1 @@
-old
+new`
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"file1.txt", "dir/file2.go"}, files)
	})

	t.Run("renamed file", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/old.txt b/new.txt
similarity index 100%
rename from old.txt
rename to new.txt`
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"new.txt"}, files)
	})

	t.Run("deleted file", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/deleted.txt b/deleted.txt
deleted file mode 100644
index 1234567..0000000
--- a/deleted.txt
+++ /dev/null`
		files := ExtractChangedFiles(diff)
		// Even deleted files are included (will be skipped when reading content).
		assert.Equal(t, []string{"deleted.txt"}, files)
	})

	t.Run("new file", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+new content`
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"new.txt"}, files)
	})

	t.Run("duplicate files in diff", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/file.txt b/file.txt
index 0000000..1111111 100644
--- a/file.txt
+++ b/file.txt
@@ -1 +1 @@
-old
+new
diff --git a/file.txt b/file.txt
index 1111111..2222222 100644
--- a/file.txt
+++ b/file.txt
@@ -2 +2 @@
-another
+change`
		files := ExtractChangedFiles(diff)
		// Should only include file once.
		assert.Equal(t, []string{"file.txt"}, files)
	})
}

func TestScanContent(t *testing.T) {
	t.Parallel()
	scanner, err := New("")
	require.NoError(t, err)

	t.Run("empty content", func(t *testing.T) {
		t.Parallel()
		findings, err := scanner.ScanContent(t.Context(), "", "test.txt")
		require.NoError(t, err)
		assert.Empty(t, findings)
	})

	t.Run("content with no secrets", func(t *testing.T) {
		t.Parallel()
		content := `package main
import "fmt"
func main() {
	fmt.Println("Hello, World!")
}`
		findings, err := scanner.ScanContent(t.Context(), content, "main.go")
		require.NoError(t, err)
		assert.Empty(t, findings)
	})

	t.Run("content with GitHub token", func(t *testing.T) {
		t.Parallel()
		content := `token: ` + fakeSecrets.GitHubPAT()
		findings, err := scanner.ScanContent(t.Context(), content, "config.yml")
		require.NoError(t, err)
		// Gitleaks should detect GitHub tokens.
		assert.NotNil(t, findings)
		if len(findings) > 0 {
			assert.Equal(t, "config.yml", findings[0].File)
		}
	})

	t.Run("content without filename", func(t *testing.T) {
		t.Parallel()
		content := `password = "super_secret_password_123"`
		findings, err := scanner.ScanContent(t.Context(), content, "")
		require.NoError(t, err)
		// DetectString returns a slice (possibly empty).
		assert.Empty(t, findings)
	})

	t.Run("multiple secrets in content", func(t *testing.T) {
		t.Parallel()
		content := `
github_token = "` + fakeSecrets.GitHubPAT() + `"
private_key = "` + fakeSecrets.FullPrivateKey() + `"
`
		findings, err := scanner.ScanContent(t.Context(), content, "secrets.txt")
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(findings), 2) // Should find at least 2 secrets.
	})
}

func TestFormatFindings(t *testing.T) {
	t.Parallel()
	t.Run("no findings", func(t *testing.T) {
		t.Parallel()
		result := FormatFindings([]report.Finding{})
		assert.Empty(t, result)

		result = FormatFindings(nil)
		assert.Empty(t, result)
	})

	t.Run("single finding", func(t *testing.T) {
		t.Parallel()
		findings := []report.Finding{
			{
				Description: "AWS Access Key",
				File:        "config.yml",
				StartLine:   10,
				RuleID:      "aws-access-key",
				Secret:      "AKIAIOSFODNN7EXAMPLE",
				Commit:      "abc123",
			},
		}

		result := FormatFindings(findings)
		assert.Contains(t, result, "Found 1 potential secret(s)")
		assert.Contains(t, result, "AWS Access Key")
		assert.Contains(t, result, "config.yml")
		assert.Contains(t, result, "Line: 10")
		assert.Contains(t, result, "aws-access-key")
		assert.Contains(t, result, "AKI...PLE") // Redacted secret.
		assert.Contains(t, result, "abc123")
	})

	t.Run("multiple findings", func(t *testing.T) {
		t.Parallel()
		findings := []report.Finding{
			{
				Description: "GitHub Token",
				File:        "main.go",
				StartLine:   5,
				RuleID:      "github-token",
				Secret:      "ghp_1234567890abcdef",
			},
			{
				Description: "Generic API Key",
				File:        "config.json",
				StartLine:   20,
				RuleID:      "generic-api-key",
				Secret:      "sk-abcd1234efgh5678",
			},
		}

		result := FormatFindings(findings)
		assert.Contains(t, result, "Found 2 potential secret(s)")
		assert.Contains(t, result, "1. GitHub Token")
		assert.Contains(t, result, "2. Generic API Key")
		assert.Contains(t, result, "main.go")
		assert.Contains(t, result, "config.json")
	})

	t.Run("finding without line number", func(t *testing.T) {
		t.Parallel()
		findings := []report.Finding{
			{
				Description: "API Key",
				File:        "test.txt",
				RuleID:      "api-key",
				Secret:      "key123",
			},
		}

		result := FormatFindings(findings)
		assert.Contains(t, result, "API Key")
		assert.NotContains(t, result, "Line:")
	})

	t.Run("finding with short secret", func(t *testing.T) {
		t.Parallel()
		findings := []report.Finding{
			{
				Description: "Short Key",
				File:        "test.txt",
				RuleID:      "short-key",
				Secret:      "abc",
			},
		}

		result := FormatFindings(findings)
		assert.Contains(t, result, "Secret: ***")
	})

	t.Run("finding without secret", func(t *testing.T) {
		t.Parallel()
		findings := []report.Finding{
			{
				Description: "Potential Secret",
				File:        "test.txt",
				RuleID:      "generic",
			},
		}

		result := FormatFindings(findings)
		assert.Contains(t, result, "Potential Secret")
		assert.NotContains(t, result, "Secret:")
	})
}

func TestRedactSecret(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		secret   string
		expected string
	}{
		{"empty string", "", "***"},
		{"short secret", "abc", "***"},
		{"8 chars", "12345678", "***"},
		{"9 chars", "123456789", "123...789"},
		{"long secret", "AKIAIOSFODNN7EXAMPLE", "AKI...PLE"},
		{"very long secret", fakeSecrets.GitHubPAT(), "ghp...456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := redactSecret(tt.secret)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewEdgeCases(t *testing.T) {
	t.Parallel()
	t.Run("empty config path uses default", func(t *testing.T) {
		t.Parallel()
		scanner, err := New("")
		require.NoError(t, err)
		assert.NotNil(t, scanner)
		assert.NotNil(t, scanner.detector)
	})
}

func TestHasFindings(t *testing.T) {
	t.Parallel()
	t.Run("no findings", func(t *testing.T) {
		t.Parallel()
		assert.False(t, HasFindings(nil))
		assert.False(t, HasFindings([]report.Finding{}))
	})

	t.Run("has findings", func(t *testing.T) {
		t.Parallel()
		findings := []report.Finding{
			{Description: "test"},
		}
		assert.True(t, HasFindings(findings))
	})
}
