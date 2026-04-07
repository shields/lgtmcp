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

package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zricethezav/gitleaks/v8/report"
)

var fakeSecrets = FakeSecrets{}

const (
	testMainGo    = "main.go"
	testGoSumHash = "cloud.google.com/go/auth v0.15.0 h1:Ly0u4aA5vG/fsSsxu98qCQBemXtAtJf+95z9HK+cxps=" // gitleaks:allow
)

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
		err := os.WriteFile(configPath, []byte(configContent), 0o600)
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
		err := os.WriteFile(configPath, []byte("invalid toml content {{"), 0o600)
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
				return testGoSumHash, nil
			case testMainGo:
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
			assert.Equal(t, testMainGo, finding.File)
			assert.NotEqual(t, "go.sum", finding.File)
		}
	})

	t.Run("skip nested go.sum file", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/submodule/go.sum b/submodule/go.sum
index 0000000..1111111 100644
--- a/submodule/go.sum
+++ b/submodule/go.sum
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
			case "submodule/go.sum":
				return testGoSumHash, nil
			case testMainGo:
				return `token := "` + fakeSecrets.GitHubPAT() + `"`, nil
			default:
				return "", os.ErrNotExist
			}
		}

		findings, err := scanner.ScanDiff(t.Context(), diff, getFileContent)
		require.NoError(t, err)
		assert.Len(t, findings, 2)
		for _, finding := range findings {
			assert.Equal(t, testMainGo, finding.File)
		}
	})

	t.Run("skip go.work.sum file", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/go.work.sum b/go.work.sum
index 0000000..1111111 100644
--- a/go.work.sum
+++ b/go.work.sum
@@ -1 +1 @@
-old checksum
+cloud.google.com/go/auth v0.15.0 h1:Ly0u4aA5vG/fsSsxu98qCQBemXtAtJf+95z9HK+cxps= # gitleaks:allow
diff --git a/sub/go.work.sum b/sub/go.work.sum
index 0000000..1111111 100644
--- a/sub/go.work.sum
+++ b/sub/go.work.sum
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
			case "go.work.sum", "sub/go.work.sum":
				return testGoSumHash, nil
			case testMainGo:
				return `token := "` + fakeSecrets.GitHubPAT() + `"`, nil
			default:
				return "", os.ErrNotExist
			}
		}

		findings, err := scanner.ScanDiff(t.Context(), diff, getFileContent)
		require.NoError(t, err)
		assert.Len(t, findings, 2)
		for _, finding := range findings {
			assert.Equal(t, testMainGo, finding.File)
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

	t.Run("secret in file with space in name", func(t *testing.T) {
		t.Parallel()
		// Regression test: a previous parser using strings.Fields silently
		// truncated paths with spaces, allowing such files to bypass the
		// secret scanner entirely.
		const filename = "my secret.txt"
		diff := `diff --git a/` + filename + ` b/` + filename + `
index 0000000..1111111 100644
--- a/` + filename + `
+++ b/` + filename + `
@@ -1 +1 @@
-old
+token=` + fakeSecrets.GitHubPAT() + "`"

		getFileContent := func(path string) (string, error) {
			if path == filename {
				return `token=` + fakeSecrets.GitHubPAT(), nil
			}

			return "", os.ErrNotExist
		}

		findings, err := scanner.ScanDiff(t.Context(), diff, getFileContent)
		require.NoError(t, err)
		assert.NotEmpty(t, findings)
		for _, finding := range findings {
			assert.Equal(t, filename, finding.File)
		}
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

	t.Run("filename with single space", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/my file.txt b/my file.txt
index 0000000..1111111 100644
--- a/my file.txt
+++ b/my file.txt
@@ -1 +1 @@
-old
+new`
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"my file.txt"}, files)
	})

	t.Run("filename with multiple spaces", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/path with  many   spaces.txt b/path with  many   spaces.txt
index 0000000..1111111 100644
--- a/path with  many   spaces.txt
+++ b/path with  many   spaces.txt
@@ -1 +1 @@
-old
+new`
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"path with  many   spaces.txt"}, files)
	})

	t.Run("filename containing literal b/ substring", func(t *testing.T) {
		t.Parallel()
		// Path "foo b/bar.txt" contains a " b/" substring; the parser must
		// still pick the correct split point so the full path is recovered.
		diff := `diff --git a/foo b/bar.txt b/foo b/bar.txt
index 0000000..1111111 100644
--- a/foo b/bar.txt
+++ b/foo b/bar.txt
@@ -1 +1 @@
-old
+new`
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"foo b/bar.txt"}, files)
	})

	t.Run("renamed file with spaces", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/old name.txt b/new name.txt
similarity index 100%
rename from old name.txt
rename to new name.txt`
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"new name.txt"}, files)
	})

	t.Run("renamed file across directories with spaces", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/dir one/old file.go b/dir two/new file.go
similarity index 90%
rename from dir one/old file.go
rename to dir two/new file.go`
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"dir two/new file.go"}, files)
	})

	t.Run("copied file with spaces", func(t *testing.T) {
		t.Parallel()
		diff := `diff --git a/orig file.go b/copied file.go
similarity index 100%
copy from orig file.go
copy to copied file.go`
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"copied file.go"}, files)
	})

	t.Run("CRLF line endings", func(t *testing.T) {
		t.Parallel()
		diff := "diff --git a/my file.txt b/my file.txt\r\n" +
			"index 0000000..1111111 100644\r\n" +
			"--- a/my file.txt\r\n" +
			"+++ b/my file.txt\r\n" +
			"@@ -1 +1 @@\r\n" +
			"-old\r\n" +
			"+new\r\n"
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"my file.txt"}, files)
	})

	t.Run("CRLF rename with spaces", func(t *testing.T) {
		t.Parallel()
		diff := "diff --git a/old name.txt b/new name.txt\r\n" +
			"similarity index 100%\r\n" +
			"rename from old name.txt\r\n" +
			"rename to new name.txt\r\n"
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"new name.txt"}, files)
	})

	t.Run("noprefix header", func(t *testing.T) {
		t.Parallel()
		// diff.noprefix=true causes git to omit the a/ and b/ prefixes.
		const name = "file.txt"
		diff := "diff --git " + name + " " + name + "\n" +
			"index 0000000..1111111 100644\n" +
			"--- " + name + "\n" +
			"+++ " + name + "\n" +
			"@@ -1 +1 @@\n" +
			"-old\n" +
			"+new\n"
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{name}, files)
	})

	t.Run("noprefix header with spaces", func(t *testing.T) {
		t.Parallel()
		const name = "my file.txt"
		diff := "diff --git " + name + " " + name + "\n" +
			"index 0000000..1111111 100644\n" +
			"--- " + name + "\n" +
			"+++ " + name + "\n" +
			"@@ -1 +1 @@\n" +
			"-old\n" +
			"+new\n"
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{name}, files)
	})

	t.Run("differing source and destination without rename line", func(t *testing.T) {
		t.Parallel()
		// "git diff file1 file2" produces a header with mismatched paths
		// and no rename/copy lines; the destination must still be returned.
		diff := `diff --git a/file1.txt b/file2.txt
index 0000000..1111111 100644
--- a/file1.txt
+++ b/file2.txt
@@ -1 +1 @@
-old
+new`
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"file2.txt"}, files)
	})

	t.Run("noprefix header with path starting with a slash component", func(t *testing.T) {
		t.Parallel()
		// With diff.noprefix and a file literally named "a/foo.go", the
		// header looks like `diff --git a/foo.go a/foo.go` and must not
		// be misinterpreted as a standard prefixed header.
		const name = "a/foo.go"
		diff := "diff --git " + name + " " + name + "\n" +
			"index 0000000..1111111 100644\n" +
			"--- " + name + "\n" +
			"+++ " + name + "\n" +
			"@@ -1 +1 @@\n" +
			"-old\n" +
			"+new\n"
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{name}, files)
	})

	t.Run("destination contains literal b/ substring with mismatched source", func(t *testing.T) {
		t.Parallel()
		// "git diff file1 'foo b/bar.txt'" - destination has " b/" inside
		// it. The fallback must split at the first " b/" so the embedded
		// separator is preserved in the destination.
		diff := `diff --git a/file1 b/foo b/bar.txt
index 0000000..1111111 100644
--- a/file1
+++ b/foo b/bar.txt
@@ -1 +1 @@
-old
+new`
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"foo b/bar.txt"}, files)
	})

	t.Run("C-quoted header with non-ASCII path", func(t *testing.T) {
		t.Parallel()
		// Git quotes paths with non-ASCII bytes by default. The unquoted
		// path here is "café.txt" (with the e-acute encoded as 0xC3 0xA9).
		diff := "diff --git \"a/caf\\303\\251.txt\" \"b/caf\\303\\251.txt\"\n" +
			"index 0000000..1111111 100644\n" +
			"--- \"a/caf\\303\\251.txt\"\n" +
			"+++ \"b/caf\\303\\251.txt\"\n" +
			"@@ -1 +1 @@\n" +
			"-old\n" +
			"+new\n"
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"café.txt"}, files)
	})

	t.Run("C-quoted rename to overrides header path", func(t *testing.T) {
		t.Parallel()
		diff := "diff --git \"a/old\\303\\251.txt\" \"b/new\\303\\251.txt\"\n" +
			"similarity index 100%\n" +
			"rename from \"old\\303\\251.txt\"\n" +
			"rename to \"new\\303\\251.txt\"\n"
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"newé.txt"}, files)
	})

	t.Run("noprefix header for path that begins with literal a slash and contains b slash", func(t *testing.T) {
		t.Parallel()
		// File literally named "a/foo b/bar". Under diff.noprefix the
		// header repeats the unescaped path on both sides; the parser
		// must prefer the noprefix-equal-halves interpretation rather
		// than misreading "a/" as a prefix.
		const name = "a/foo b/bar"
		diff := "diff --git " + name + " " + name + "\n" +
			"index 0000000..1111111 100644\n" +
			"--- " + name + "\n" +
			"+++ " + name + "\n" +
			"@@ -1 +1 @@\n" +
			"-old\n" +
			"+new\n"
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{name}, files)
	})

	t.Run("C-quoted noprefix header for path beginning with b slash", func(t *testing.T) {
		t.Parallel()
		// File literally named "b/café.txt" with diff.noprefix. The
		// parser must not strip the leading "b/" because the a-side
		// also lacks the "a/" prefix that distinguishes prefixed form.
		diff := "diff --git \"b/caf\\303\\251.txt\" \"b/caf\\303\\251.txt\"\n" +
			"index 0000000..1111111 100644\n" +
			"--- \"b/caf\\303\\251.txt\"\n" +
			"+++ \"b/caf\\303\\251.txt\"\n" +
			"@@ -1 +1 @@\n" +
			"-old\n" +
			"+new\n"
		files := ExtractChangedFiles(diff)
		assert.Equal(t, []string{"b/café.txt"}, files)
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

func TestExtractChangedFiles_MalformedHeader(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/file.txt\nsome content"
	files := ExtractChangedFiles(diff)
	assert.Empty(t, files)
}

func TestFakeSecrets_AWSAccessKey(t *testing.T) {
	t.Parallel()
	key := fakeSecrets.AWSAccessKey()
	assert.NotEmpty(t, key)
	assert.True(t, strings.HasPrefix(key, "AKIA"), "AWS access key should start with AKIA")
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
