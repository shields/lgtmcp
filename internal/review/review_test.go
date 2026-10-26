// Copyright © 2025-2026 Michael Shields
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

package review

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"msrl.dev/lgtmcp/internal/config"
	"msrl.dev/lgtmcp/internal/logging"
	"msrl.dev/lgtmcp/internal/prompts"
	"msrl.dev/lgtmcp/internal/testutil"
)

var errTest = errors.New("test error")

// testPricingTime is a fixed instant inside the Gemini Flash introductory
// pricing window, so cost assertions do not depend on the wall clock.
var testPricingTime = time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

// generateContentFn is a type alias to shorten long function signatures in tests.
type generateContentFn = func(
	context.Context, string, []*genai.Content, *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error)

func TestHandleFileRetrieval_GitIgnore(t *testing.T) {
	t.Parallel()
	repoDir := testutil.CreateTempGitRepo(t)

	gitignoreContent := `# Sensitive files
secrets.txt
*.key
*.pem
config/*.secret
node_modules/
.env
.env.*
build/
dist/
`
	err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte(gitignoreContent), 0o600)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(repoDir, "allowed.txt"), []byte("allowed content"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(repoDir, "secrets.txt"), []byte("secret content"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(repoDir, "private.key"), []byte("private key content"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(repoDir, ".env"), []byte("API_KEY=secret123"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(repoDir, ".env.local"), []byte("DB_PASSWORD=secret456"), 0o600)
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "node_modules"), 0o750))
	err = os.WriteFile(filepath.Join(repoDir, "node_modules", "package.json"), []byte(`{"name": "test"}`), 0o600)
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "config"), 0o750))
	err = os.WriteFile(filepath.Join(repoDir, "config", "database.secret"), []byte("db_password=secret"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(repoDir, "config", "app.json"), []byte(`{"port": 3000}`), 0o600)
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "build"), 0o750))
	err = os.WriteFile(filepath.Join(repoDir, "build", "output.js"), []byte("compiled code"), 0o600)
	require.NoError(t, err)

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, testutil.NewTestLogger())
	require.NoError(t, err)

	tests := []fileRetrievalTest{
		{
			name:          "normal file access",
			filepath:      "allowed.txt",
			shouldSucceed: true,
			expectContent: "allowed content",
		},
		{
			name:          "directly ignored file - secrets.txt",
			filepath:      "secrets.txt",
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          "pattern ignored file - .key extension",
			filepath:      "private.key",
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          ".env file (ignored)",
			filepath:      ".env",
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          ".env.local file (pattern ignored)",
			filepath:      ".env.local",
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          "file in ignored directory - node_modules",
			filepath:      "node_modules/package.json",
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          "pattern ignored in subdirectory - config/*.secret",
			filepath:      "config/database.secret",
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          "normal file in config directory",
			filepath:      "config/app.json",
			shouldSucceed: true,
			expectContent: `{"port": 3000}`,
		},
		{
			name:          "file in build directory (ignored)",
			filepath:      "build/output.js",
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          ".gitignore file itself",
			filepath:      ".gitignore",
			shouldSucceed: true,
			expectContent: gitignoreContent,
		},
	}

	runFileRetrievalTests(t, reviewer, repoDir, tests)
}

func TestHandleFileRetrieval_NestedGitIgnore(t *testing.T) {
	t.Parallel()
	repoDir := testutil.CreateTempGitRepo(t)

	err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("root-secret.txt\n"), 0o600)
	require.NoError(t, err)

	subdir := filepath.Join(repoDir, "subdir")
	require.NoError(t, os.MkdirAll(subdir, 0o750))
	err = os.WriteFile(filepath.Join(subdir, ".gitignore"), []byte("local-secret.txt\n"), 0o600)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(repoDir, "root-secret.txt"), []byte("root secret"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(subdir, "local-secret.txt"), []byte("subdir secret"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(subdir, "normal.txt"), []byte("normal content"), 0o600)
	require.NoError(t, err)

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, testutil.NewTestLogger())
	require.NoError(t, err)

	tests := []fileRetrievalTest{
		{
			name:          "root-level ignored file",
			filepath:      "root-secret.txt",
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          "nested gitignore - file ignored by subdir .gitignore",
			filepath:      "subdir/local-secret.txt",
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          "normal file in subdirectory",
			filepath:      "subdir/normal.txt",
			shouldSucceed: true,
			expectContent: "normal content",
		},
	}

	runFileRetrievalTests(t, reviewer, repoDir, tests)
}

func TestHandleFileRetrieval_NegatedGitIgnore(t *testing.T) {
	t.Parallel()
	repoDir := testutil.CreateTempGitRepo(t)

	// Ignore all .log files, but un-ignore important.log via negation.
	err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("*.log\n!important.log\n"), 0o600)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(repoDir, "debug.log"), []byte("debug output"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(repoDir, "important.log"), []byte("important output"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(repoDir, "app.txt"), []byte("app content"), 0o600)
	require.NoError(t, err)

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, testutil.NewTestLogger())
	require.NoError(t, err)

	tests := []fileRetrievalTest{
		{
			name:          "ignored by glob - debug.log",
			filepath:      "debug.log",
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          "negated ignore - important.log",
			filepath:      "important.log",
			shouldSucceed: true,
			expectContent: "important output",
		},
		{
			name:          "unrelated file - app.txt",
			filepath:      "app.txt",
			shouldSucceed: true,
			expectContent: "app content",
		},
	}

	runFileRetrievalTests(t, reviewer, repoDir, tests)
}

func TestHandleFileRetrieval_GitInfoExclude(t *testing.T) {
	t.Parallel()
	repoDir := testutil.CreateTempGitRepo(t)

	// Use .git/info/exclude instead of .gitignore.
	excludePath := filepath.Join(repoDir, ".git", "info", "exclude")
	require.NoError(t, os.MkdirAll(filepath.Dir(excludePath), 0o750))
	err := os.WriteFile(excludePath, []byte("excluded-by-info.txt\n"), 0o600)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(repoDir, "excluded-by-info.txt"), []byte("secret"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(repoDir, "normal.txt"), []byte("normal"), 0o600)
	require.NoError(t, err)

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, testutil.NewTestLogger())
	require.NoError(t, err)

	tests := []fileRetrievalTest{
		{
			name:          "excluded by .git/info/exclude",
			filepath:      "excluded-by-info.txt",
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          "normal file not excluded",
			filepath:      "normal.txt",
			shouldSucceed: true,
			expectContent: "normal",
		},
	}

	runFileRetrievalTests(t, reviewer, repoDir, tests)
}

// TestHandleFileRetrieval_GitEnvLeak ensures gitignore enforcement consults the
// target repository even when git-location environment variables (GIT_DIR,
// GIT_WORK_TREE, GIT_INDEX_FILE) are inherited, as happens when lgtmcp runs
// inside another git process such as a pre-commit hook. Without sanitizing the
// child process environment, git check-ignore would resolve a different
// repository and silently fail to honor the target repo's .gitignore.
//
// This test mutates the process environment and therefore must not run in
// parallel.
func TestHandleFileRetrieval_GitEnvLeak(t *testing.T) {
	repoDir := testutil.CreateTempGitRepo(t)

	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("secret.txt\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "secret.txt"), []byte("secret content"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "normal.txt"), []byte("normal content"), 0o600))

	// Point git-location vars at an unrelated repository to simulate the leak.
	otherRepo := testutil.CreateTempGitRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(otherRepo, ".git"))
	t.Setenv("GIT_WORK_TREE", otherRepo)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(otherRepo, ".git", "index"))

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, testutil.NewTestLogger())
	require.NoError(t, err)

	ignored := retrieveSingle(t, reviewer, repoDir, "secret.txt", nil)
	errMsg, ok := ignored["error"].(string)
	require.True(t, ok, "gitignored file must be denied despite leaked git env, got %+v", ignored)
	assert.Contains(t, errMsg, "access denied: file is gitignored")

	allowed := retrieveSingle(t, reviewer, repoDir, "normal.txt", nil)
	assert.Equal(t, "normal content", allowed["content"])
}

// TestHandleFileRetrieval_EchoesCallID verifies every function response,
// success or error, carries the call's ID and name so the API can match the
// response to its call, as Gemini 3 requires.
func TestHandleFileRetrieval_EchoesCallID(t *testing.T) {
	t.Parallel()

	repoDir := testutil.CreateTempGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "ok.txt"), []byte("ok"), 0o600))

	tests := []struct {
		name    string
		args    map[string]any
		wantKey string
	}{
		{name: "successful read", args: map[string]any{"filepaths": []any{"ok.txt"}}, wantKey: "files"},
		{name: "error response", args: map[string]any{"filepaths": "ok.txt"}, wantKey: errorKey},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &Reviewer{logger: testutil.NewTestLogger()}
			part := r.handleFileRetrieval(t.Context(), &genai.FunctionCall{
				ID:   "call-123",
				Name: "get_file_content",
				Args: tt.args,
			}, repoDir, nil, nil, newRetrievalBudget())
			require.NotNil(t, part.FunctionResponse)
			assert.Equal(t, "call-123", part.FunctionResponse.ID)
			assert.Equal(t, "get_file_content", part.FunctionResponse.Name)
			assert.Contains(t, part.FunctionResponse.Response, tt.wantKey)
		})
	}
}

type fileRetrievalTest struct {
	name          string
	filepath      string
	expectContent string
	shouldSucceed bool
	expectedError string
}

func retrieveFiles(
	t *testing.T, reviewer *Reviewer, repoDir string, paths []string, deleted map[string]bool,
	onFetch FileFetchCallback,
) []map[string]any {
	t.Helper()

	part := reviewer.handleFileRetrieval(t.Context(), fileCall("", paths...), repoDir, deleted, onFetch,
		newRetrievalBudget())
	require.NotNil(t, part.FunctionResponse)
	require.NotContains(t, part.FunctionResponse.Response, errorKey)

	return responseFiles(t, part.FunctionResponse)
}

func retrieveSingle(
	t *testing.T, reviewer *Reviewer, repoDir, path string, deleted map[string]bool,
) map[string]any {
	t.Helper()

	files := retrieveFiles(t, reviewer, repoDir, []string{path}, deleted, nil)
	require.Len(t, files, 1)
	assert.Equal(t, path, files[0]["filepath"])

	return files[0]
}

func runFileRetrievalTests(t *testing.T, reviewer *Reviewer, repoDir string, tests []fileRetrievalTest) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			response := retrieveSingle(t, reviewer, repoDir, tt.filepath, nil)

			if tt.shouldSucceed {
				content, hasContent := response["content"].(string)
				if !hasContent {
					if errMsg, hasError := response["error"].(string); hasError {
						t.Fatalf("Got error instead of content: %s", errMsg)
					}
					t.Fatalf("Response missing content: %+v", response)
				}
				assert.Equal(t, tt.expectContent, content)
			} else {
				errMsg, hasError := response["error"].(string)
				require.True(t, hasError, "Expected error in response for %s", tt.filepath)
				if tt.expectedError != "" {
					assert.Contains(t, errMsg, tt.expectedError)
				}
			}
		})
	}
}

func TestHandleFileRetrieval_GitCommandFailure(t *testing.T) {
	t.Parallel()
	// Create a temporary directory that is NOT a git repository
	nonGitDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(nonGitDir, "test.txt")
	err := os.WriteFile(testFile, []byte("test content"), 0o600)
	require.NoError(t, err)

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, testutil.NewTestLogger())
	require.NoError(t, err)

	// Test that when git check-ignore fails (not a git repo), access is denied
	response := retrieveSingle(t, reviewer, nonGitDir, "test.txt", nil)

	// Should have an error due to git command failure
	errMsg, hasError := response["error"].(string)
	assert.True(t, hasError, "Expected error when git command fails")
	assert.Contains(t, errMsg, "access denied: unable to verify gitignore status",
		"Should fail closed when git check-ignore cannot run")
}

func TestHandleFileRetrieval_PathTraversal(t *testing.T) {
	t.Parallel()
	repoDir := testutil.CreateTempGitRepo(t)

	err := os.WriteFile(filepath.Join(repoDir, "allowed.txt"), []byte("allowed content"), 0o600)
	require.NoError(t, err)

	// Create a sensitive file outside the repo.
	sensitiveDir := t.TempDir()
	sensitiveFile := filepath.Join(sensitiveDir, "sensitive.txt")
	err = os.WriteFile(sensitiveFile, []byte("sensitive content"), 0o600)
	require.NoError(t, err)

	// Calculate relative path from repo to sensitive file.
	// This will be something like "../../../var/folders/.../sensitive.txt".
	relPath, err := filepath.Rel(repoDir, sensitiveFile)
	require.NoError(t, err)

	// Create a symlink that points outside the repo.
	symlinkPath := filepath.Join(repoDir, "sneaky_link")
	err = os.Symlink(sensitiveFile, symlinkPath)
	require.NoError(t, err)

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, testutil.NewTestLogger())
	require.NoError(t, err)

	tests := []struct {
		name          string
		filepath      string
		expectContent string
		shouldSucceed bool
	}{
		{
			name:          "normal file access",
			filepath:      "allowed.txt",
			shouldSucceed: true,
			expectContent: "allowed content",
		},
		{
			name:          "path traversal with ..",
			filepath:      "../" + sensitiveFile,
			shouldSucceed: false,
		},
		{
			name:          "path traversal with relative path",
			filepath:      relPath,
			shouldSucceed: false,
		},
		{
			name:          "absolute path traversal - /etc/hosts",
			filepath:      "/etc/hosts",
			shouldSucceed: false,
		},
		{
			name:          "path with embedded ..",
			filepath:      "subdir/../../" + sensitiveFile,
			shouldSucceed: false,
		},
		{
			name:          "symlink traversal attack",
			filepath:      "sneaky_link", // Symlink pointing outside repo.
			shouldSucceed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			t.Logf("Testing with filepath: %s, repoDir: %s", tt.filepath, repoDir)
			response := retrieveSingle(t, reviewer, repoDir, tt.filepath, nil)

			if tt.shouldSucceed {
				content, hasContent := response["content"].(string)
				if !hasContent {
					// Debug: print what we got instead.
					if errMsg, hasError := response["error"].(string); hasError {
						t.Logf("Got error instead of content: %s", errMsg)
					}
					t.Logf("Response: %+v", response)
				}
				assert.True(t, hasContent, "Expected content in response")
				assert.Equal(t, tt.expectContent, content)
			} else {
				assert.Contains(t, response, "error", "%s must be denied", tt.filepath)
				assert.NotContains(t, response, "content",
					"SECURITY VULNERABILITY: Path traversal allowed access to %s", tt.filepath)
			}
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()
	t.Run("with API key - success path", func(t *testing.T) {
		t.Parallel()
		// This test doesn't actually call Gemini API during New().
		// So it should succeed regardless of API key validity.
		cfg := config.NewTestConfig()
		reviewer, err := New(cfg, testutil.NewTestLogger())

		// The New function should succeed even with invalid API key.
		// Since it doesn't validate the key during creation.
		require.NoError(t, err)
		assert.NotNil(t, reviewer)
		assert.NotNil(t, reviewer.client)
	})

	t.Run("without API key - expected failure", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestConfig()
		cfg.Google.APIKey = ""
		cfg.Google.UseADC = false // Explicitly disable ADC.
		reviewer, err := New(cfg, testutil.NewTestLogger())

		// Should fail without API key or ADC.
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no authentication method configured")
		assert.Nil(t, reviewer)
	})

	t.Run("with ADC enabled", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestConfig()
		cfg.Google.APIKey = ""
		cfg.Google.UseADC = true
		reviewer, err := New(cfg, testutil.NewTestLogger())

		// This will succeed or fail based on whether ADC is actually available.
		// Since we can't guarantee ADC is available in test environment,
		// we just check that it attempts to use ADC.
		if err != nil {
			// If it fails, it should be because ADC is not available, not because of config.
			assert.NotContains(t, err.Error(), "no authentication method configured")
			assert.Nil(t, reviewer)
		} else {
			// If it succeeds, reviewer should be created.
			assert.NotNil(t, reviewer)
			assert.NotNil(t, reviewer.client)
		}
	})

	t.Run("uses custom model from config", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestConfig()
		cfg.Gemini.Model = "gemini-3.1-pro-preview"

		reviewer, err := New(cfg, testutil.NewTestLogger())
		require.NoError(t, err)
		assert.NotNil(t, reviewer)
	})

	t.Run("uses default model when not specified", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestConfig()
		// Cfg already has default model set to gemini-3.8-flash.

		reviewer, err := New(cfg, testutil.NewTestLogger())
		require.NoError(t, err)
		assert.NotNil(t, reviewer)
	})
}

func TestHandleFileRetrieval(t *testing.T) {
	t.Parallel()
	r := &Reviewer{logger: testutil.NewTestLogger()}
	tmpDir := testutil.CreateTempGitRepo(t)

	testContent := "test file content"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(testContent), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "other.txt"), []byte("other content"), 0o600))

	t.Run("successful file retrieval", func(t *testing.T) {
		t.Parallel()
		part := r.handleFileRetrieval(t.Context(), &genai.FunctionCall{
			Name: "get_file_content",
			Args: map[string]any{"filepaths": []any{"test.txt"}},
		}, tmpDir, nil, nil, newRetrievalBudget())
		require.NotNil(t, part.FunctionResponse)
		assert.Equal(t, "get_file_content", part.FunctionResponse.Name)

		files := retrieveSingle(t, r, tmpDir, "test.txt", nil)
		assert.Equal(t, testContent, files["content"])
		assert.NotContains(t, files, errorKey)
	})

	t.Run("multiple paths return entries in request order", func(t *testing.T) {
		t.Parallel()
		files := retrieveFiles(t, r, tmpDir,
			[]string{"other.txt", "nonexistent.txt", "test.txt", "../outside.txt"}, nil, nil)
		require.Len(t, files, 4)

		assert.Equal(t, "other.txt", files[0]["filepath"])
		assert.Equal(t, "other content", files[0]["content"])
		assert.NotContains(t, files[0], errorKey)

		assert.Equal(t, "nonexistent.txt", files[1]["filepath"])
		assert.Contains(t, files[1]["error"], "failed to read file")
		assert.NotContains(t, files[1], "content")

		assert.Equal(t, "test.txt", files[2]["filepath"])
		assert.Equal(t, testContent, files[2]["content"])

		assert.Equal(t, "../outside.txt", files[3]["filepath"])
		assert.Contains(t, files[3]["error"], "path traversal not allowed")
	})

	t.Run("duplicate paths are each answered", func(t *testing.T) {
		t.Parallel()
		files := retrieveFiles(t, r, tmpDir, []string{"test.txt", "test.txt"}, nil, nil)
		require.Len(t, files, 2)
		assert.Equal(t, testContent, files[0]["content"])
		assert.Equal(t, testContent, files[1]["content"])
	})

	t.Run("paths beyond the per-turn limit are refused", func(t *testing.T) {
		t.Parallel()
		// A traversal path costs no git process but still counts against the
		// budget.
		paths := make([]string, maxFilesPerTurn+2)
		for i := range paths {
			paths[i] = "../outside.txt"
		}

		var fetched int
		files := retrieveFiles(t, r, tmpDir, paths, nil, func(string) { fetched++ })
		require.Len(t, files, len(paths))
		assert.Equal(t, maxFilesPerTurn, fetched, "refused paths are not fetched")
		assert.Contains(t, files[maxFilesPerTurn-1]["error"], "path traversal not allowed")
		for _, entry := range files[maxFilesPerTurn:] {
			assert.Equal(t, "../outside.txt", entry["filepath"])
			assert.Equal(t, errTooManyFilesMsg, entry["error"])
		}
	})

	t.Run("content beyond the per-turn budget is refused", func(t *testing.T) {
		t.Parallel()
		dir := testutil.CreateTempGitRepo(t)
		big := make([]byte, maxRetrievedBytesPerTurn/2+1)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "big1.bin"), big, 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "big2.bin"), big, 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "small.txt"), []byte("small"), 0o600))

		files := retrieveFiles(t, r, dir, []string{"big1.bin", "big2.bin", "small.txt"}, nil, nil)
		require.Len(t, files, 3)
		assert.Len(t, files[0]["content"], len(big))
		assert.Equal(t, errTurnBudgetMsg, files[1]["error"])
		assert.NotContains(t, files[1], "content")
		assert.Equal(t, "small", files[2]["content"], "a refused file does not consume the budget")
	})

	t.Run("a file over the size limit is too large even with part of the budget used", func(t *testing.T) {
		t.Parallel()
		dir := testutil.CreateTempGitRepo(t)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "small.txt"), []byte("small"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "huge.bin"), make([]byte, maxRetrievedFileSize+1), 0o600))

		files := retrieveFiles(t, r, dir, []string{"small.txt", "huge.bin"}, nil, nil)
		require.Len(t, files, 2)
		assert.Equal(t, "small", files[0]["content"])
		assert.Contains(t, files[1]["error"], "file too large")
	})

	t.Run("non-string entry is an error entry", func(t *testing.T) {
		t.Parallel()
		part := r.handleFileRetrieval(t.Context(), &genai.FunctionCall{
			Name: "get_file_content",
			Args: map[string]any{"filepaths": []any{42, "test.txt"}},
		}, tmpDir, nil, nil, newRetrievalBudget())
		require.NotNil(t, part.FunctionResponse)
		files, ok := part.FunctionResponse.Response["files"].([]any)
		require.True(t, ok)
		require.Len(t, files, 2)

		bad, ok := files[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "42", bad["filepath"])
		assert.Equal(t, "filepath must be a string", bad["error"])

		good, ok := files[1].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, testContent, good["content"])
	})

	invalidArgs := []struct {
		name string
		args map[string]any
	}{
		{name: "missing filepaths parameter", args: map[string]any{}},
		{name: "nil args", args: nil},
		{name: "empty array", args: map[string]any{"filepaths": []any{}}},
		{name: "string instead of array", args: map[string]any{"filepaths": "test.txt"}},
		{name: "legacy filepath parameter", args: map[string]any{"filepath": "test.txt"}},
		{name: "number instead of array", args: map[string]any{"filepaths": 42}},
	}
	for _, tt := range invalidArgs {
		t.Run(tt.name+" is a whole-call error", func(t *testing.T) {
			t.Parallel()
			part := r.handleFileRetrieval(t.Context(), &genai.FunctionCall{
				Name: "get_file_content",
				Args: tt.args,
			}, tmpDir, nil, nil, newRetrievalBudget())
			require.NotNil(t, part.FunctionResponse)
			assert.Contains(t, part.FunctionResponse.Response[errorKey], "filepaths parameter must be a non-empty array")
			assert.NotContains(t, part.FunctionResponse.Response, "files")
		})
	}

	t.Run("path traversal attempt", func(t *testing.T) {
		t.Parallel()
		files := retrieveSingle(t, r, tmpDir, "../../../etc/passwd", nil)
		assert.Contains(t, files["error"], "path traversal not allowed")
	})

	t.Run("only a parent component is traversal", func(t *testing.T) {
		t.Parallel()
		dir := testutil.CreateTempGitRepo(t)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "docs", "v1..v2.md"), []byte("notes"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "top.txt"), []byte("top"), 0o600))

		assert.Equal(t, "notes", retrieveSingle(t, r, dir, "docs/v1..v2.md", nil)["content"])
		assert.Contains(t, retrieveSingle(t, r, dir, "docs/../top.txt", nil)["error"],
			"path traversal not allowed")
	})

	t.Run("a directory is explained", func(t *testing.T) {
		t.Parallel()
		dir := testutil.CreateTempGitRepo(t)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs"), 0o750))

		for _, path := range []string{"docs", "docs/"} {
			assert.Equal(t, errDirectoryMsg, retrieveSingle(t, r, dir, path, nil)["error"], path)
		}
	})

	t.Run("file not found", func(t *testing.T) {
		t.Parallel()
		files := retrieveSingle(t, r, tmpDir, "nonexistent.txt", nil)
		assert.Contains(t, files["error"], "failed to read file")
	})

	t.Run("callback is invoked once per requested path", func(t *testing.T) {
		t.Parallel()
		var fetched []string
		retrieveFiles(t, r, tmpDir,
			[]string{"test.txt", "nonexistent.txt", "other.txt"}, nil,
			func(path string) { fetched = append(fetched, path) })
		assert.Equal(t, []string{"test.txt", "nonexistent.txt", "other.txt"}, fetched)
	})

	t.Run("callback is skipped for empty and non-string paths", func(t *testing.T) {
		t.Parallel()
		var fetched []string
		r.handleFileRetrieval(t.Context(), &genai.FunctionCall{
			Name: "get_file_content",
			Args: map[string]any{"filepaths": []any{"", 7, "test.txt"}},
		}, tmpDir, nil, func(path string) { fetched = append(fetched, path) }, newRetrievalBudget())
		assert.Equal(t, []string{"test.txt"}, fetched)
	})

	t.Run("deleted file returns explicit deleted-file message", func(t *testing.T) {
		t.Parallel()
		// The model asks for a path the caller flagged as a deletion. The
		// tool must short-circuit and tell the model the file was removed,
		// rather than letting it fall through to a generic ENOENT that
		// could be mistaken for a transient read failure.
		deleted := map[string]bool{"gone.txt": true}

		entry := retrieveSingle(t, r, tmpDir, "gone.txt", deleted)
		errMsg, ok := entry["error"].(string)
		require.True(t, ok, "expected error in entry, got %+v", entry)
		assert.Equal(t, errDeletedFileMsg, errMsg)
		assert.NotContains(t, errMsg, "failed to read file")
		assert.NotContains(t, errMsg, "no such file or directory")

		// And it must not leak content.
		assert.NotContains(t, entry, "content", "deleted-file entry must not include content")
	})

	t.Run("deleted-file short-circuit also applies for paths with redundant components", func(t *testing.T) {
		t.Parallel()
		// The deleted set is keyed by filepath.Clean'd paths; a request
		// for "./gone.txt" should still hit the short-circuit.
		deleted := map[string]bool{"gone.txt": true}

		entry := retrieveSingle(t, r, tmpDir, "./gone.txt", deleted)
		assert.Equal(t, errDeletedFileMsg, entry["error"])
	})

	t.Run("deleted and present files in one call", func(t *testing.T) {
		t.Parallel()
		deleted := map[string]bool{"gone.txt": true}

		files := retrieveFiles(t, r, tmpDir, []string{"gone.txt", "test.txt"}, deleted, nil)
		require.Len(t, files, 2)
		assert.Equal(t, errDeletedFileMsg, files[0]["error"])
		assert.Equal(t, testContent, files[1]["content"])
	})

	t.Run("existing file is unaffected by populated deleted set", func(t *testing.T) {
		t.Parallel()
		// Sanity check that a non-empty deleted set does not poison the
		// happy path: a request for a still-present file returns content.
		deleted := map[string]bool{"some-other-path.txt": true}

		entry := retrieveSingle(t, r, tmpDir, "test.txt", deleted)
		assert.Equal(t, testContent, entry["content"])
	})
}

func TestReviewDiff(t *testing.T) {
	t.Parallel()

	t.Run("successful review with stub", func(t *testing.T) {
		t.Parallel()

		// Use the clean testing helper.
		reviewer := WithStubResponse(true, "Changes look good, well-structured improvements")

		diff := `diff --git a/main.go b/main.go
index 0000000..1111111 100644
--- a/main.go
+++ b/main.go
@@ -1,5 +1,5 @@
 package main

 func main() {
-    println("hello")
+    println("Hello, World!")
 }`

		tmpDir := t.TempDir()
		result, err := reviewer.ReviewDiff(t.Context(), diff, []string{"main.go"}, tmpDir)

		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.True(t, result.LGTM)
		assert.Equal(t, "Changes look good, well-structured improvements", result.Comments)
	})

	t.Run("rejection with stub", func(t *testing.T) {
		t.Parallel()

		// Test rejection case.
		reviewer := WithStubResponse(false, "Found issues with error handling")

		diff := `diff --git a/main.go b/main.go
index 0000000..1111111 100644
--- a/main.go
+++ b/main.go
@@ -1,5 +1,5 @@
 package main

 func main() {
-    println("hello")
+    println("Hello, World!")
 }`

		tmpDir := t.TempDir()
		result, err := reviewer.ReviewDiff(t.Context(), diff, []string{"main.go"}, tmpDir)

		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.False(t, result.LGTM)
		assert.Equal(t, "Found issues with error handling", result.Comments)
	})
}

func TestResult(t *testing.T) {
	t.Parallel()

	t.Run("struct fields", func(t *testing.T) {
		t.Parallel()
		result := Result{
			LGTM:     true,
			Comments: "Looks good!",
		}

		assert.True(t, result.LGTM)
		assert.Equal(t, "Looks good!", result.Comments)
	})

	t.Run("zero-value optional fields are omitted", func(t *testing.T) {
		t.Parallel()

		encoded, err := json.Marshal(Result{})
		require.NoError(t, err)
		assert.JSONEq(t, `{"comments":"","lgtm":false}`, string(encoded))
	})
}

func TestReviewDiff_ErrorCases(t *testing.T) {
	t.Parallel()

	t.Run("empty diff", func(t *testing.T) {
		t.Parallel()

		reviewer := NewForTesting()
		result, err := reviewer.ReviewDiff(t.Context(), "", []string{}, "/tmp")

		// Should fail with empty diff validation error.
		require.Error(t, err)
		assert.Contains(t, err.Error(), "diff cannot be empty")
		assert.Nil(t, result)
	})

	t.Run("context cancellation", func(t *testing.T) {
		t.Parallel()

		// Create a reviewer that checks context.
		reviewer := &Reviewer{
			client: newStubClientWithGenerateContent(func(
				ctx context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
			) (*genai.GenerateContentResponse, error) {
				return nil, ctx.Err()
			}),
			modelName:     "gemini-3.1-pro-preview",
			promptManager: prompts.New(""),
			logger:        testutil.NewTestLogger(),
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancel() // Cancel immediately.

		diff := "sample diff content"
		result, err := reviewer.ReviewDiff(ctx, diff, []string{"file.go"}, "/tmp")

		// Should fail due to canceled context.
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "context canceled")
	})
}

func TestIsRetryableError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err      error
		name     string
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name: "APIError with 408 status code (Request Timeout)",
			err: genai.APIError{
				Code:    http.StatusRequestTimeout,
				Message: "Request Timeout",
			},
			expected: true,
		},
		{
			name: "APIError with 429 status code",
			err: genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "You exceeded your current quota",
				Status:  "RESOURCE_EXHAUSTED",
			},
			expected: true,
		},
		{
			name: "APIError with 500 status code",
			err: genai.APIError{
				Code:    http.StatusInternalServerError,
				Message: "Internal Server Error",
			},
			expected: true,
		},
		{
			name: "APIError with 501 status code (Not Implemented - NOT retryable)",
			err: genai.APIError{
				Code:    http.StatusNotImplemented,
				Message: "Not Implemented",
			},
			expected: false,
		},
		{
			name: "APIError with 502 status code",
			err: genai.APIError{
				Code:    http.StatusBadGateway,
				Message: "Bad Gateway",
			},
			expected: true,
		},
		{
			name: "APIError with 503 status code",
			err: genai.APIError{
				Code:    http.StatusServiceUnavailable,
				Message: "Service Unavailable",
			},
			expected: true,
		},
		{
			name: "APIError with 504 status code (Gateway Timeout)",
			err: genai.APIError{
				Code:    http.StatusGatewayTimeout,
				Message: "Gateway Timeout",
			},
			expected: true,
		},
		{
			name: "APIError with INTERNAL status",
			err: genai.APIError{
				Code:   0, // No HTTP code.
				Status: "INTERNAL",
			},
			expected: true,
		},
		{
			name: "APIError with UNAVAILABLE status",
			err: genai.APIError{
				Code:   0,
				Status: "UNAVAILABLE",
			},
			expected: true,
		},
		{
			name: "APIError with DEADLINE_EXCEEDED status",
			err: genai.APIError{
				Code:   0,
				Status: "DEADLINE_EXCEEDED",
			},
			expected: true,
		},
		{
			name: "APIError with 403 (non-retryable)",
			err: genai.APIError{
				Code:    http.StatusForbidden,
				Message: "Permission Denied",
			},
			expected: false,
		},
		{
			name: "APIError with 401 (non-retryable)",
			err: genai.APIError{
				Code:    http.StatusUnauthorized,
				Message: "Unauthorized",
			},
			expected: false,
		},
		{
			name: "APIError with retryable words only in Message",
			err: genai.APIError{
				Code:    http.StatusBadRequest,
				Message: "Error 503: INTERNAL, UNAVAILABLE, DEADLINE_EXCEEDED, RESOURCE_EXHAUSTED",
				Status:  "INVALID_ARGUMENT",
			},
			expected: false,
		},
		{
			name: "wrapped APIError with retryable words only in Message",
			err: fmt.Errorf("gemini request failed: %w", genai.APIError{
				Code:    http.StatusBadRequest,
				Message: "Error 503: UNAVAILABLE",
				Status:  "INVALID_ARGUMENT",
			}),
			expected: false,
		},
		{
			name: "joined APIError with retryable words only in Message",
			err: errors.Join(errTest, genai.APIError{
				Code:    http.StatusBadRequest,
				Message: "Error 503: UNAVAILABLE",
				Status:  "INVALID_ARGUMENT",
			}),
			expected: false,
		},
		// Fallback string matching tests.
		{
			name:     "request timeout 408 (string)",
			err:      errors.New("Error 408: Request Timeout"), //nolint:err113 // test case
			expected: true,
		},
		{
			name:     "rate limit error 429 (string)",
			err:      errors.New("Error 429, Message: You exceeded your current quota"), //nolint:err113 // test case
			expected: true,
		},
		{
			name:     "resource exhausted error (string)",
			err:      errors.New("Status: RESOURCE_EXHAUSTED, Details: []"), //nolint:err113 // test case
			expected: true,
		},
		{
			name:     "server error 500 (string)",
			err:      errors.New("Error 500: Internal Server Error"), //nolint:err113 // test case
			expected: true,
		},
		{
			name:     "server error 501 NOT retryable (string)",
			err:      errors.New("Error 501: Not Implemented"), //nolint:err113 // test case
			expected: false,
		},
		{
			name:     "server error 502 (string)",
			err:      errors.New("Error 502: Bad Gateway"), //nolint:err113 // test case
			expected: true,
		},
		{
			name:     "server error 503 (string)",
			err:      errors.New("Error 503: Service Unavailable"), //nolint:err113 // test case
			expected: true,
		},
		{
			name:     "server error 504 (string)",
			err:      errors.New("Error 504: Gateway Timeout"), //nolint:err113 // test case
			expected: true,
		},
		{
			name:     "internal error (string)",
			err:      errors.New("Status: INTERNAL"), //nolint:err113 // test case
			expected: true,
		},
		{
			name:     "unavailable error (string)",
			err:      errors.New("Status: UNAVAILABLE"), //nolint:err113 // test case
			expected: true,
		},
		{
			name:     "deadline exceeded error (string)",
			err:      errors.New("Status: DEADLINE_EXCEEDED"), //nolint:err113 // test case
			expected: true,
		},
		{
			name:     "non-retryable error (string)",
			err:      errors.New("invalid API key"), //nolint:err113 // test case
			expected: false,
		},
		{
			name:     "permission denied (string)",
			err:      errors.New("Error 403: Permission Denied"), //nolint:err113 // test case
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isRetryableError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractRetryDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err      error
		name     string
		expected time.Duration
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: 0,
		},
		{
			name: "APIError with retryDelay in Details",
			err: genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Rate limit exceeded",
				Details: []map[string]any{
					{
						"@type":      "type.googleapis.com/google.rpc.RetryInfo",
						"retryDelay": "15s",
					},
				},
			},
			expected: 15 * time.Second,
		},
		{
			name: "APIError with retryDelay 2s",
			err: genai.APIError{
				Code: http.StatusTooManyRequests,
				Details: []map[string]any{
					{
						"@type":      "type.googleapis.com/google.rpc.RetryInfo",
						"retryDelay": "2s",
					},
				},
			},
			expected: 2 * time.Second,
		},
		{
			name: "APIError with retryDelay 1m",
			err: genai.APIError{
				Code: http.StatusTooManyRequests,
				Details: []map[string]any{
					{
						"@type":      "type.googleapis.com/google.rpc.RetryInfo",
						"retryDelay": "1m",
					},
				},
			},
			expected: 1 * time.Minute,
		},
		{
			name: "APIError without retryDelay",
			err: genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Rate limit exceeded",
				Details: []map[string]any{
					{
						"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
						"reason": "RATE_LIMIT_EXCEEDED",
					},
				},
			},
			expected: 0,
		},
		{
			name: "APIError with invalid retryDelay",
			err: genai.APIError{
				Code: http.StatusTooManyRequests,
				Details: []map[string]any{
					{
						"@type":      "type.googleapis.com/google.rpc.RetryInfo",
						"retryDelay": "invalid",
					},
				},
			},
			expected: 0,
		},
		{
			name: "APIError with no Details",
			err: genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Rate limit exceeded",
			},
			expected: 0,
		},
		{
			name: "APIError with retryDelay only in Message",
			err: genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Rate limit exceeded, retryDelay:15s]",
			},
			expected: 0,
		},
		{
			name: "wrapped APIError with retryDelay only in Message",
			err: fmt.Errorf("gemini request failed: %w", genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Rate limit exceeded, retryDelay:15s]",
			}),
			expected: 0,
		},
		{
			name: "wrapped APIError with retryDelay in Details",
			err: fmt.Errorf("gemini request failed: %w", genai.APIError{
				Code: http.StatusTooManyRequests,
				Details: []map[string]any{
					{
						"@type":      "type.googleapis.com/google.rpc.RetryInfo",
						"retryDelay": "2s",
					},
				},
			}),
			expected: 2 * time.Second,
		},
		// Fallback string parsing tests.
		{
			name:     "error with retryDelay 15s (string)",
			err:      errors.New("Error 429... retryDelay:15s]"), //nolint:err113 // test case
			expected: 15 * time.Second,
		},
		{
			name:     "error with retryDelay 2s (string)",
			err:      errors.New("Details: [map[@type:type.googleapis.com/google.rpc.RetryInfo retryDelay:2s]]"), //nolint:err113,lll // test case
			expected: 2 * time.Second,
		},
		{
			name:     "error with retryDelay 1m (string)",
			err:      errors.New("retryDelay:1m }"), //nolint:err113 // test case
			expected: 1 * time.Minute,
		},
		{
			name:     "error without retryDelay (string)",
			err:      errors.New("Error 429: Rate limit exceeded"), //nolint:err113 // test case
			expected: 0,
		},
		{
			name:     "error with invalid retryDelay (string)",
			err:      errors.New("retryDelay:invalid]"), //nolint:err113 // test case
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := extractRetryDelay(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateBackoff(t *testing.T) {
	t.Parallel()

	cfg := &config.RetryConfig{
		InitialBackoff:    "1s",
		MaxBackoff:        "60s",
		BackoffMultiplier: 1.4,
	}

	tests := []struct {
		name        string
		attempt     int
		minExpected time.Duration
		maxExpected time.Duration
	}{
		{
			name:        "first attempt",
			attempt:     0,
			minExpected: 800 * time.Millisecond,  // 1s * 0.8 (with jitter).
			maxExpected: 1200 * time.Millisecond, // 1s * 1.2 (with jitter).
		},
		{
			name:        "second attempt",
			attempt:     1,
			minExpected: 1120 * time.Millisecond, // 1.4s * 0.8.
			maxExpected: 1680 * time.Millisecond, // 1.4s * 1.2.
		},
		{
			name:        "third attempt",
			attempt:     2,
			minExpected: 1568 * time.Millisecond, // 1.96s * 0.8.
			maxExpected: 2352 * time.Millisecond, // 1.96s * 1.2.
		},
		{
			name:        "large attempt number",
			attempt:     10,
			minExpected: 23 * time.Second, // ~28.9s * 0.8 (1.4^10 = 28.9s).
			maxExpected: 35 * time.Second, // ~28.9s * 1.2.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Test multiple times to account for randomness.
			for range 10 {
				result := calculateBackoff(tt.attempt, cfg)
				assert.GreaterOrEqual(t, result, tt.minExpected,
					"backoff should be at least %v", tt.minExpected)
				assert.LessOrEqual(t, result, tt.maxExpected,
					"backoff should be at most %v", tt.maxExpected)
			}
		})
	}
}

func TestRetryableOperation(t *testing.T) {
	t.Parallel()

	t.Run("successful on first attempt", func(t *testing.T) {
		t.Parallel()

		cfg := &config.RetryConfig{
			MaxRetries:        new(3),
			InitialBackoff:    "10ms",
			MaxBackoff:        "100ms",
			BackoffMultiplier: 2.0,
		}

		reviewer := &Reviewer{retryConfig: cfg, logger: testutil.NewTestLogger()}
		callCount := 0

		err := reviewer.retryableOperation(t.Context(), func() error {
			callCount++

			return nil // Success.
		}, "test_operation")

		require.NoError(t, err)
		assert.Equal(t, 1, callCount)
	})

	t.Run("successful after retries", func(t *testing.T) {
		t.Parallel()

		cfg := &config.RetryConfig{
			MaxRetries:        new(3),
			InitialBackoff:    "10ms",
			MaxBackoff:        "100ms",
			BackoffMultiplier: 2.0,
		}

		reviewer := &Reviewer{retryConfig: cfg, logger: testutil.NewTestLogger()}
		callCount := 0

		err := reviewer.retryableOperation(t.Context(), func() error {
			callCount++
			if callCount < 3 {
				return errors.New("Error 429: Rate limit exceeded") //nolint:err113 // test case
			}

			return nil // Success on third attempt.
		}, "test_operation")

		require.NoError(t, err)
		assert.Equal(t, 3, callCount)
	})

	t.Run("non-retryable error", func(t *testing.T) {
		t.Parallel()

		cfg := &config.RetryConfig{
			MaxRetries:        new(3),
			InitialBackoff:    "10ms",
			MaxBackoff:        "100ms",
			BackoffMultiplier: 2.0,
		}

		reviewer := &Reviewer{retryConfig: cfg, logger: testutil.NewTestLogger()}
		callCount := 0
		expectedErr := errors.New("invalid API key") //nolint:err113 // test case

		err := reviewer.retryableOperation(t.Context(), func() error {
			callCount++

			return expectedErr
		}, "test_operation")

		assert.Equal(t, expectedErr, err)
		assert.Equal(t, 1, callCount) // Should not retry.
	})

	t.Run("max retries exceeded", func(t *testing.T) {
		t.Parallel()

		cfg := &config.RetryConfig{
			MaxRetries:        new(2),
			InitialBackoff:    "10ms",
			MaxBackoff:        "100ms",
			BackoffMultiplier: 2.0,
		}

		reviewer := &Reviewer{retryConfig: cfg, logger: testutil.NewTestLogger()}
		callCount := 0

		err := reviewer.retryableOperation(t.Context(), func() error {
			callCount++

			return errors.New("Error 429: Rate limit exceeded") //nolint:err113 // test case
		}, "test_operation")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed after 3 attempts")
		assert.Equal(t, 3, callCount) // Initial + 2 retries.
	})

	t.Run("context cancellation", func(t *testing.T) {
		t.Parallel()

		cfg := &config.RetryConfig{
			MaxRetries:        new(3),
			InitialBackoff:    "100ms",
			MaxBackoff:        "1s",
			BackoffMultiplier: 2.0,
		}

		reviewer := &Reviewer{retryConfig: cfg, logger: testutil.NewTestLogger()}
		callCount := 0

		ctx, cancel := context.WithCancel(t.Context())

		// Cancel during the first attempt, so the backoff wait that follows
		// ends at once however slowly the test is scheduled.
		err := reviewer.retryableOperation(ctx, func() error {
			callCount++
			cancel()

			return errors.New("Error 429: Rate limit exceeded") //nolint:err113 // test case
		}, "test_operation")

		assert.Equal(t, context.Canceled, err)
		assert.Equal(t, 1, callCount) // Should stop after first attempt.
	})

	t.Run("no retry config", func(t *testing.T) {
		t.Parallel()

		reviewer := &Reviewer{retryConfig: nil, logger: testutil.NewTestLogger()}
		callCount := 0
		expectedErr := errors.New("Error 429: Rate limit exceeded") //nolint:err113 // test case

		err := reviewer.retryableOperation(t.Context(), func() error {
			callCount++

			return expectedErr
		}, "test_operation")

		assert.Equal(t, expectedErr, err)
		assert.Equal(t, 1, callCount) // Should not retry.
	})

	t.Run("caps oversized API retry delay at MaxBackoff", func(t *testing.T) {
		t.Parallel()

		cfg := &config.RetryConfig{
			MaxRetries:        new(1),
			InitialBackoff:    "10ms",
			MaxBackoff:        "50ms",
			BackoffMultiplier: 2.0,
		}

		reviewer := &Reviewer{retryConfig: cfg, logger: testutil.NewTestLogger()}
		callCount := 0

		start := time.Now()
		err := reviewer.retryableOperation(t.Context(), func() error {
			callCount++

			// A hostile/buggy retryDelay an hour out must be clamped to
			// MaxBackoff (50ms), not slept on verbatim.
			return errors.New("Error 429: rate limited retryDelay:3600s") //nolint:err113 // test case
		}, "test_operation")

		elapsed := time.Since(start)
		require.Error(t, err)
		assert.Equal(t, 2, callCount) // Initial + 1 retry.
		// Capped at 50ms; a generous ceiling well below the uncapped 3600s
		// keeps the assertion meaningful without being CI-flaky.
		assert.Less(t, elapsed, 10*time.Second)
	})
}

func TestTokenUsage(t *testing.T) {
	t.Parallel()

	t.Run("addFromResponse with nil response", func(t *testing.T) {
		t.Parallel()
		usage := &tokenUsage{}
		usage.addFromResponse(nil)

		assert.Equal(t, int32(0), usage.PromptTokens)
		assert.Equal(t, int32(0), usage.CandidatesTokens)
	})

	t.Run("addFromResponse with nil UsageMetadata", func(t *testing.T) {
		t.Parallel()
		usage := &tokenUsage{}
		usage.addFromResponse(&genai.GenerateContentResponse{
			UsageMetadata: nil,
		})

		assert.Equal(t, int32(0), usage.PromptTokens)
		assert.Equal(t, int32(0), usage.CandidatesTokens)
	})

	t.Run("addFromResponse accumulates tokens", func(t *testing.T) {
		t.Parallel()
		usage := &tokenUsage{}

		// First response.
		usage.addFromResponse(&genai.GenerateContentResponse{
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     100,
				CandidatesTokenCount: 50,
			},
		})

		assert.Equal(t, int32(100), usage.PromptTokens)
		assert.Equal(t, int32(50), usage.CandidatesTokens)

		// Second response should accumulate.
		usage.addFromResponse(&genai.GenerateContentResponse{
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     200,
				CandidatesTokenCount: 100,
			},
		})

		assert.Equal(t, int32(300), usage.PromptTokens)
		assert.Equal(t, int32(150), usage.CandidatesTokens)
	})

	t.Run("addFromResponse tracks all token types", func(t *testing.T) {
		t.Parallel()
		usage := &tokenUsage{}

		usage.addFromResponse(&genai.GenerateContentResponse{
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:        100,
				CandidatesTokenCount:    50,
				CachedContentTokenCount: 25,
				ThoughtsTokenCount:      10,
				ToolUsePromptTokenCount: 5,
			},
		})

		assert.Equal(t, int32(100), usage.PromptTokens)
		assert.Equal(t, int32(50), usage.CandidatesTokens)
		assert.Equal(t, int32(25), usage.CachedTokens)
		assert.Equal(t, int32(10), usage.ThoughtsTokens)
		assert.Equal(t, int32(5), usage.ToolUseTokens)
	})

	t.Run("total matches the TotalTokenCount definition", func(t *testing.T) {
		t.Parallel()
		usage := &tokenUsage{
			PromptTokens:     1000,
			CandidatesTokens: 500,
			ThoughtsTokens:   200,
			ToolUseTokens:    50,
			CachedTokens:     100, // Not included in total (separate metric).
		}

		// prompt + candidates + tool-use + thoughts, per the genai docs.
		assert.Equal(t, int32(1750), usage.total())
	})

	t.Run("cost calculation", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name             string
			modelName        string
			at               time.Time // Zero means testPricingTime.
			promptTokens     int32
			candidatesTokens int32
			thoughtsTokens   int32
			cachedTokens     int32
			toolUseTokens    int32
			expectedCost     float64
		}{
			{
				name:             "zero tokens",
				modelName:        "gemini-3.1-pro-preview",
				promptTokens:     0,
				candidatesTokens: 0,
				expectedCost:     0.0,
			},
			{
				name:             "1M input tokens only",
				modelName:        "gemini-3.1-pro-preview",
				promptTokens:     1_000_000,
				candidatesTokens: 0,
				expectedCost:     2.00, // $2.00 per 1M input
			},
			{
				name:             "1M output tokens only",
				modelName:        "gemini-3.1-pro-preview",
				promptTokens:     0,
				candidatesTokens: 1_000_000,
				expectedCost:     12.00, // $12.00 per 1M output
			},
			{
				name:             "mixed tokens",
				modelName:        "gemini-3.1-pro-preview",
				promptTokens:     500_000, // 0.5M input = $1.00
				candidatesTokens: 100_000, // 0.1M output = $1.20
				expectedCost:     2.20,
			},
			{
				name:             "typical review",
				modelName:        "gemini-3.1-pro-preview",
				promptTokens:     10_000, // 10K input = $0.02
				candidatesTokens: 1_000,  // 1K output = $0.012
				expectedCost:     0.032,
			},
			{
				name:             "gemini-3.8-flash introductory pricing",
				modelName:        "gemini-3.8-flash",
				promptTokens:     1_000_000, // 1M input = $0.75
				candidatesTokens: 1_000_000, // 1M output = $3.75
				expectedCost:     4.50,
			},
			{
				name:             "gemini-3.8-flash standard pricing once the introductory period ends",
				modelName:        "gemini-3.8-flash",
				at:               flashIntroductoryPricingEnd,
				promptTokens:     1_000_000, // 1M input = $1.50
				candidatesTokens: 1_000_000, // 1M output = $7.50
				expectedCost:     9.00,
			},
			{
				name:             "unknown model returns -1",
				modelName:        "unknown-model",
				promptTokens:     1_000_000,
				candidatesTokens: 1_000_000,
				expectedCost:     -1,
			},
			{
				name:             "thoughts tokens included in output cost",
				modelName:        "gemini-3.1-pro-preview",
				promptTokens:     0,
				candidatesTokens: 500_000, // 0.5M output = $6.00
				thoughtsTokens:   500_000, // 0.5M thoughts = $6.00
				expectedCost:     12.00,   // Total output cost
			},
			{
				name:             "cached tokens at 10% of input price",
				modelName:        "gemini-3.1-pro-preview",
				promptTokens:     0,
				candidatesTokens: 0,
				cachedTokens:     1_000_000, // 1M cached = $2.00 * 0.1 = $0.20
				expectedCost:     0.20,
			},
			{
				// Realistic cache hit: PromptTokens already includes the cached
				// tokens, so only the 600K non-cached remainder bills at the
				// full rate. The pre-fix formula double-counted and returned 2.08.
				name:             "cache hit discounts only the cached portion",
				modelName:        "gemini-3.1-pro-preview",
				promptTokens:     1_000_000, // 600K non-cached @ $2.00 = $1.20
				candidatesTokens: 0,
				cachedTokens:     400_000, // 400K cached @ $0.20 = $0.08
				expectedCost:     1.28,
			},
			{
				// Tool-use prompt tokens are reported separately from
				// PromptTokens and bill as input.
				name:          "tool-use tokens billed at input rate",
				modelName:     "gemini-3.1-pro-preview",
				toolUseTokens: 1_000_000,
				expectedCost:  2.00,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				usage := &tokenUsage{
					PromptTokens:     tt.promptTokens,
					CandidatesTokens: tt.candidatesTokens,
					ThoughtsTokens:   tt.thoughtsTokens,
					CachedTokens:     tt.cachedTokens,
					ToolUseTokens:    tt.toolUseTokens,
				}

				at := tt.at
				if at.IsZero() {
					at = testPricingTime
				}
				cost := usage.cost(tt.modelName, at)
				assert.InDelta(t, tt.expectedCost, cost, 0.0001,
					"cost should be approximately $%.4f", tt.expectedCost)
			})
		}
	})

	t.Run("costWithoutCaching ignores the cache discount", func(t *testing.T) {
		t.Parallel()
		usage := &tokenUsage{PromptTokens: 1_000_000, CachedTokens: 400_000}
		// All prompt tokens billed at the full input rate, no discount.
		assert.InDelta(t, 2.00, usage.costWithoutCaching("gemini-3.1-pro-preview", testPricingTime), 0.0001)
		// Unknown model returns -1, matching cost().
		assert.InDelta(t, -1.0, usage.costWithoutCaching("unknown-model", testPricingTime), 0.0001)
	})

	t.Run("savings is baseline minus actual", func(t *testing.T) {
		t.Parallel()
		// 400K cached of 1M prompt: baseline $2.00, actual $1.28, saved $0.72.
		usage := &tokenUsage{PromptTokens: 1_000_000, CachedTokens: 400_000}
		assert.InDelta(t, 0.72, usage.savings("gemini-3.1-pro-preview", testPricingTime), 0.0001)

		// No cached tokens: nothing saved.
		noCache := &tokenUsage{PromptTokens: 1_000_000}
		assert.InDelta(t, 0.0, noCache.savings("gemini-3.1-pro-preview", testPricingTime), 0.0001)

		// Unknown model: zero, not a negative number.
		assert.InDelta(t, 0.0, usage.savings("unknown-model", testPricingTime), 0.0001)
	})

	t.Run("cacheHitRate is cached over prompt", func(t *testing.T) {
		t.Parallel()
		usage := &tokenUsage{PromptTokens: 1_000_000, CachedTokens: 400_000}
		assert.InDelta(t, 0.4, usage.cacheHitRate(), 0.0001)

		// Divide-by-zero guard: no prompt tokens => 0, no panic.
		empty := &tokenUsage{}
		assert.InDelta(t, 0.0, empty.cacheHitRate(), 0.0001)
	})
}

func TestIsQuotaExhaustedError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err      error
		name     string
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name: "APIError with QuotaFailure in Details",
			err: genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "You exceeded your current quota",
				Status:  "RESOURCE_EXHAUSTED",
				Details: []map[string]any{
					{
						"@type": "type.googleapis.com/google.rpc.QuotaFailure",
						"violations": []map[string]any{
							{"quotaId": "GenerateRequestsPerDayPerProjectPerModel"},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "APIError with RESOURCE_EXHAUSTED but no QuotaFailure (rate limit)",
			err: genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Rate limit exceeded",
				Status:  "RESOURCE_EXHAUSTED",
				Details: []map[string]any{
					{
						"@type": "type.googleapis.com/google.rpc.RetryInfo",
					},
				},
			},
			expected: false, // Rate limit, not quota exhaustion.
		},
		{
			name:     "QuotaFailure type in error string",
			err:      errors.New("Details: [@type:type.googleapis.com/google.rpc.QuotaFailure]"), //nolint:err113 // test case
			expected: true,
		},
		{
			name: "exceeded your current quota without QuotaFailure type (rate limit)",
			//nolint:err113 // test case
			err:      errors.New("Error 429: You exceeded your current quota"),
			expected: false, // No QuotaFailure type, so this is a rate limit.
		},
		{
			name: "Regular 429 without quota info",
			err: genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Too many requests",
			},
			expected: false,
		},
		{
			name: "APIError with QuotaFailure type only in Message",
			err: genai.APIError{
				Code:    http.StatusBadRequest,
				Message: "invalid argument: " + quotaFailureType,
				Status:  "INVALID_ARGUMENT",
			},
			expected: false,
		},
		{
			name: "429 whose body the SDK could not decode",
			err: genai.APIError{
				Code:    http.StatusTooManyRequests,
				Status:  "429 Too Many Requests",
				Message: `[{"error": {"code": "429", "details": [{"@type": "` + quotaFailureType + `"}]}}]`,
			},
			expected: true,
		},
		{
			name: "429 with details naming the QuotaFailure type only in Message",
			err: genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "rate limited; not a " + quotaFailureType,
				Status:  "RESOURCE_EXHAUSTED",
				Details: []map[string]any{{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "1s"}},
			},
			expected: false,
		},
		{
			name: "wrapped APIError with QuotaFailure type only in Message",
			err: fmt.Errorf("gemini request failed: %w", genai.APIError{
				Code:    http.StatusBadRequest,
				Message: "invalid argument: " + quotaFailureType,
			}),
			expected: false,
		},
		{
			name: "wrapped APIError with QuotaFailure in Details",
			err: fmt.Errorf("gemini request failed: %w", genai.APIError{
				Code:    http.StatusTooManyRequests,
				Details: []map[string]any{{"@type": quotaFailureType}},
			}),
			expected: true,
		},
		{
			name: "joined APIError with QuotaFailure in Details",
			err: errors.Join(errTest, genai.APIError{
				Code:    http.StatusTooManyRequests,
				Details: []map[string]any{{"@type": quotaFailureType}},
			}),
			expected: true,
		},
		{
			name:     "Non-quota error",
			err:      errors.New("internal server error"), //nolint:err113 // test case
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isQuotaExhaustedError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRetryableOperationQuotaFailure(t *testing.T) {
	t.Parallel()

	cfg := &config.RetryConfig{
		MaxRetries:        new(3),
		InitialBackoff:    "10ms",
		MaxBackoff:        "100ms",
		BackoffMultiplier: 2.0,
	}

	reviewer := &Reviewer{retryConfig: cfg, logger: testutil.NewTestLogger()}
	callCount := 0

	// Create a quota exhaustion error.
	quotaErr := genai.APIError{
		Code:    http.StatusTooManyRequests,
		Message: "You exceeded your current quota",
		Status:  "RESOURCE_EXHAUSTED",
		Details: []map[string]any{
			{
				"@type": "type.googleapis.com/google.rpc.QuotaFailure",
				"violations": []map[string]any{
					{"quotaId": "GenerateRequestsPerDayPerProjectPerModel"},
				},
			},
		},
	}

	err := reviewer.retryableOperation(t.Context(), func() error {
		callCount++
		return quotaErr
	}, "test_operation")

	// Should return ErrQuotaExhausted immediately without retrying.
	require.Error(t, err)
	require.ErrorIs(t, err, ErrQuotaExhausted, "should return ErrQuotaExhausted")
	assert.Equal(t, 1, callCount, "operation should only be called once (no retries)")
}

func TestIsRetryableErrorWithQuotaFailure(t *testing.T) {
	t.Parallel()

	// Quota exhaustion should NOT be retryable (should fallback instead).
	quotaErr := genai.APIError{
		Code:    http.StatusTooManyRequests,
		Message: "You exceeded your current quota",
		Status:  "RESOURCE_EXHAUSTED",
		Details: []map[string]any{
			{
				"@type": "type.googleapis.com/google.rpc.QuotaFailure",
				"violations": []map[string]any{
					{"quotaId": "GenerateRequestsPerDayPerProjectPerModel"},
				},
			},
		},
	}

	result := isRetryableError(quotaErr)
	assert.False(t, result, "quota exhaustion should not be retryable")
}

func TestWithFileFetchCallback(t *testing.T) {
	t.Parallel()
	called := false
	cb := func(_ string) { called = true }
	opt := WithFileFetchCallback(cb)
	opts := &Options{}
	opt(opts)
	assert.NotNil(t, opts.FileFetchCallback)
	opts.FileFetchCallback("test.go")
	assert.True(t, called)
}

func TestWithInstructions(t *testing.T) {
	t.Parallel()
	opt := WithInstructions("review carefully")
	opts := &Options{}
	opt(opts)
	assert.Equal(t, "review carefully", opts.Instructions)
}

func TestStubGeminiClient_DefaultPaths(t *testing.T) {
	t.Parallel()
	client := &StubGeminiClient{}

	// GenerateContent default returns approval.
	resp, err := client.GenerateContent(t.Context(), "model", nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.Candidates)
}

func TestReviewDiff_FallbackOnQuota(t *testing.T) {
	t.Parallel()
	callCount := 0
	client := newStubClientWithGenerateContent(func(
		_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
	) (*genai.GenerateContentResponse, error) {
		callCount++
		if callCount == 1 {
			// Primary model returns quota error.
			return nil, errors.Join(ErrQuotaExhausted, genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Quota exceeded",
				Details: []map[string]any{{"@type": "type.googleapis.com/google.rpc.QuotaFailure"}},
			})
		}
		// Fallback model succeeds.
		return textResponse(`{"lgtm": true, "comments": "OK"}`), nil
	})

	r := &Reviewer{
		client:        client,
		modelName:     "primary-model",
		fallbackModel: "fallback-model",
		promptManager: prompts.New(""),
		logger:        testutil.NewTestLogger(),
	}

	result, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.NoError(t, err)
	assert.True(t, result.LGTM)
	assert.Equal(t, 2, callCount)
}

// TestReviewDiff_FallbackAggregatesSpend verifies that when the primary model
// is exhausted mid-review, the tokens it consumed before failing are folded
// into the reported totals and cost alongside the fallback model's spend, each
// priced at its own rate.
func TestReviewDiff_FallbackAggregatesSpend(t *testing.T) {
	t.Parallel()
	client, requests := recordingClient(func(n int, req recordedRequest) (*genai.GenerateContentResponse, error) {
		if req.model == "gemini-3.1-pro-preview" {
			if n == 0 {
				// The primary model spends 100 prompt tokens on a file request
				// before its next request hits the daily quota.
				return withUsage(callResponse(fileCall("c1", "a.go")), 100, 0), nil
			}

			return nil, errors.Join(ErrQuotaExhausted, genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Quota exceeded",
				Details: []map[string]any{{"@type": quotaFailureType}},
			})
		}

		return withUsage(verdictResponse(), 200, 80), nil
	})

	r := newTestReviewer(client)
	r.modelName = "gemini-3.1-pro-preview"
	r.fallbackModel = "gemini-3.7-flash"

	result, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, result.TokenUsage)
	assert.Len(t, *requests, 3)

	// Prompt tokens span both attempts: primary (100) plus fallback (200);
	// candidates come from the fallback only.
	assert.Equal(t, int32(300), result.TokenUsage.PromptTokens)
	assert.Equal(t, int32(80), result.TokenUsage.CandidatesTokens)
	assert.Equal(t, "gemini-3.7-flash", result.Model)

	// Cost folds the primary attempt's spend (priced at the primary model's
	// rate) into the fallback's, so it exceeds the fallback-only cost. The
	// expected figures are priced at the wall clock, as ReviewDiff does.
	now := time.Now()
	primaryOnly := (&tokenUsage{PromptTokens: 100}).cost("gemini-3.1-pro-preview", now)
	fallbackOnly := (&tokenUsage{PromptTokens: 200, CandidatesTokens: 80}).cost("gemini-3.7-flash", now)
	assert.InDelta(t, primaryOnly+fallbackOnly, result.CostUSD, 1e-9)
	assert.Greater(t, result.CostUSD, fallbackOnly)
}

func TestReviewDiff_FallbackNone(t *testing.T) {
	t.Parallel()
	client := newStubClientWithGenerateContent(func(
		_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
	) (*genai.GenerateContentResponse, error) {
		return nil, errors.Join(ErrQuotaExhausted, fmt.Errorf("quota exceeded: %w", errTest))
	})

	r := &Reviewer{
		client:        client,
		modelName:     "primary-model",
		fallbackModel: config.FallbackModelNone,
		promptManager: prompts.New(""),
		logger:        testutil.NewTestLogger(),
	}

	_, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrQuotaExhausted)
}

func TestReviewDiff_FallbackSameModel(t *testing.T) {
	t.Parallel()
	client := newStubClientWithGenerateContent(func(
		_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
	) (*genai.GenerateContentResponse, error) {
		return nil, errors.Join(ErrQuotaExhausted, fmt.Errorf("quota exceeded: %w", errTest))
	})

	r := &Reviewer{
		client:        client,
		modelName:     "same-model",
		fallbackModel: "same-model",
		promptManager: prompts.New(""),
		logger:        testutil.NewTestLogger(),
	}

	_, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrQuotaExhausted)
}

func TestReviewDiffWithModel_NoResponse(t *testing.T) {
	t.Parallel()
	client := newStubClientWithGenerateContent(func(
		_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
	) (*genai.GenerateContentResponse, error) {
		return &genai.GenerateContentResponse{}, nil // Empty candidates.
	})

	r := &Reviewer{
		client:        client,
		modelName:     "test-model",
		promptManager: prompts.New(""),
		logger:        testutil.NewTestLogger(),
	}

	_, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.ErrorIs(t, err, ErrNoResponse)
}

func newStubClientWithGenerateContent(fn generateContentFn) *StubGeminiClient {
	return &StubGeminiClient{GenerateContentFunc: fn}
}

type recordedRequest struct {
	model    string
	contents []*genai.Content
	config   *genai.GenerateContentConfig
}

func recordingClient(
	respond func(n int, req recordedRequest) (*genai.GenerateContentResponse, error),
) (*StubGeminiClient, *[]recordedRequest) {
	var requests []recordedRequest

	return &StubGeminiClient{
		GenerateContentFunc: func(
			_ context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig,
		) (*genai.GenerateContentResponse, error) {
			// The reviewer keeps appending to its history after this call returns.
			req := recordedRequest{model: model, contents: slices.Clone(contents), config: cfg}
			n := len(requests)
			requests = append(requests, req)

			return respond(n, req)
		},
	}, &requests
}

func scriptedClient(
	t *testing.T, responses ...*genai.GenerateContentResponse,
) (*StubGeminiClient, *[]recordedRequest) {
	t.Helper()

	return recordingClient(func(n int, _ recordedRequest) (*genai.GenerateContentResponse, error) {
		if n >= len(responses) {
			t.Errorf("unexpected request %d; the script has %d responses", n+1, len(responses))

			return nil, errTest
		}

		return responses[n], nil
	})
}

func contentResponse(parts ...*genai.Part) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: parts}}},
	}
}

func verdictResponse() *genai.GenerateContentResponse {
	return textResponse(stubReviewJSON)
}

func fileCall(id string, paths ...string) *genai.FunctionCall {
	args := make([]any, len(paths))
	for i, p := range paths {
		args[i] = p
	}

	return &genai.FunctionCall{ID: id, Name: "get_file_content", Args: map[string]any{"filepaths": args}}
}

func callResponse(calls ...*genai.FunctionCall) *genai.GenerateContentResponse {
	parts := make([]*genai.Part, len(calls))
	for i, call := range calls {
		parts[i] = &genai.Part{FunctionCall: call}
	}

	return contentResponse(parts...)
}

func withUsage(resp *genai.GenerateContentResponse, prompt, candidates int32) *genai.GenerateContentResponse {
	resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:     prompt,
		CandidatesTokenCount: candidates,
	}

	return resp
}

func newTestReviewer(client GeminiClient) *Reviewer {
	return &Reviewer{
		client:        client,
		modelName:     "test-model",
		promptManager: prompts.New(""),
		logger:        testutil.NewTestLogger(),
	}
}

func functionResponses(content *genai.Content) []*genai.FunctionResponse {
	var responses []*genai.FunctionResponse
	for _, part := range content.Parts {
		if part.FunctionResponse != nil {
			responses = append(responses, part.FunctionResponse)
		}
	}

	return responses
}

func responseFiles(t *testing.T, resp *genai.FunctionResponse) []map[string]any {
	t.Helper()

	list, ok := resp.Response["files"].([]any)
	require.True(t, ok, "response must hold a files list, got %+v", resp.Response)
	files := make([]map[string]any, len(list))
	for i, entry := range list {
		files[i], ok = entry.(map[string]any)
		require.True(t, ok, "files entry %d must be an object, got %+v", i, entry)
	}

	return files
}

func assertRequestConfig(t *testing.T, cfg *genai.GenerateContentConfig, thinking *genai.ThinkingConfig) {
	t.Helper()

	require.NotNil(t, cfg)
	require.Len(t, cfg.Tools, 1)
	assert.Same(t, fileRetrievalTool, cfg.Tools[0])
	assert.Equal(t, "application/json", cfg.ResponseMIMEType)
	assert.Same(t, reviewResponseSchema, cfg.ResponseSchema)
	assert.Equal(t, thinking, cfg.ThinkingConfig)
}

func assertToolsDisabled(t *testing.T, cfg *genai.GenerateContentConfig) {
	t.Helper()

	require.NotNil(t, cfg.ToolConfig)
	require.NotNil(t, cfg.ToolConfig.FunctionCallingConfig)
	assert.Equal(t, genai.FunctionCallingConfigModeNone, cfg.ToolConfig.FunctionCallingConfig.Mode)
}

type capturingLogger struct {
	logging.Logger

	mu    sync.Mutex
	infos map[string][]any
}

func newCapturingLogger() *capturingLogger {
	return &capturingLogger{Logger: testutil.NewTestLogger(), infos: map[string][]any{}}
}

func (l *capturingLogger) Info(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos[msg] = args
}

func (l *capturingLogger) field(msg, key string) (any, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	args := l.infos[msg]
	for i := 0; i+1 < len(args); i += 2 {
		if args[i] == key {
			return args[i+1], true
		}
	}

	return nil, false
}

func TestReviewDiffWithModel_NilContent(t *testing.T) {
	t.Parallel()

	nilContent := func(reason genai.FinishReason) *genai.GenerateContentResponse {
		return &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{Content: nil, FinishReason: reason}},
		}
	}

	noParts := func(reason genai.FinishReason) *genai.GenerateContentResponse {
		return &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{Content: &genai.Content{Role: genai.RoleModel}, FinishReason: reason}},
		}
	}

	tests := []struct {
		name      string
		responses []*genai.GenerateContentResponse
		reason    genai.FinishReason
	}{
		{
			name:      "initial request",
			responses: []*genai.GenerateContentResponse{nilContent(genai.FinishReasonSafety)},
			reason:    genai.FinishReasonSafety,
		},
		{
			name:      "content without parts",
			responses: []*genai.GenerateContentResponse{noParts(genai.FinishReasonMaxTokens)},
			reason:    genai.FinishReasonMaxTokens,
		},
		{
			name: "after a tool turn",
			responses: []*genai.GenerateContentResponse{
				callResponse(fileCall("c1", "a.go")), nilContent(genai.FinishReasonMaxTokens),
			},
			reason: genai.FinishReasonMaxTokens,
		},
		{
			name: "final verdict request",
			responses: []*genai.GenerateContentResponse{
				withUsage(callResponse(fileCall("c1", "a.go")), maxPromptTokens, 0),
				nilContent(genai.FinishReasonOther),
			},
			reason: genai.FinishReasonOther,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client, requests := scriptedClient(t, tt.responses...)

			result, err := newTestReviewer(client).ReviewDiff(
				t.Context(), "diff content", []string{"file.go"}, t.TempDir())
			require.ErrorIs(t, err, ErrEmptyResponse)
			assert.Contains(t, err.Error(), string(tt.reason))
			assert.Nil(t, result)
			assert.Len(t, *requests, len(tt.responses))
		})
	}
}

func TestReviewDiffWithModel_PromptBlocked(t *testing.T) {
	t.Parallel()
	client, requests := scriptedClient(t, &genai.GenerateContentResponse{
		PromptFeedback: &genai.GenerateContentResponsePromptFeedback{BlockReason: genai.BlockedReasonSafety},
	})

	result, err := newTestReviewer(client).ReviewDiff(
		t.Context(), "diff content", []string{"file.go"}, t.TempDir())
	require.ErrorIs(t, err, ErrNoResponse)
	assert.Contains(t, err.Error(), string(genai.BlockedReasonSafety))
	assert.Nil(t, result)
	assert.Len(t, *requests, 1)
}

func TestReviewDiffWithModel_EmptyResponse(t *testing.T) {
	t.Parallel()
	client := newStubClientWithGenerateContent(
		func(
			_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
		) (*genai.GenerateContentResponse, error) {
			return textResponse(""), nil
		},
	)

	r := &Reviewer{
		client:        client,
		modelName:     "test-model",
		promptManager: prompts.New(""),
		logger:        testutil.NewTestLogger(),
	}

	_, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.ErrorIs(t, err, ErrEmptyResponse)
}

func TestReviewDiffWithModel_JSONParseError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response string
	}{
		{name: "malformed JSON", response: "not valid json"},
		{
			name:     "duplicate object name",
			response: `{"lgtm":true,"lgtm":false,"comments":"ambiguous"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newStubClientWithGenerateContent(
				func(
					_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
				) (*genai.GenerateContentResponse, error) {
					return textResponse(tt.response), nil
				},
			)

			r := &Reviewer{
				client:        client,
				modelName:     "test-model",
				promptManager: prompts.New(""),
				logger:        testutil.NewTestLogger(),
			}

			_, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "failed to parse review response")
		})
	}
}

func TestReviewDiffWithModel_ToolCallLoop(t *testing.T) {
	t.Parallel()
	client, requests := scriptedClient(t,
		callResponse(fileCall("call-1", "main.go")),
		verdictResponse(),
	)

	tmpDir := testutil.CreateTempGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main"), 0o600))

	var fetchedFiles []string
	result, err := newTestReviewer(client).ReviewDiff(
		t.Context(), "diff content", []string{"main.go"}, tmpDir,
		WithFileFetchCallback(func(path string) { fetchedFiles = append(fetchedFiles, path) }),
		WithInstructions("test instructions"),
	)
	require.NoError(t, err)
	assert.True(t, result.LGTM)
	assert.Equal(t, []string{"main.go"}, fetchedFiles)
	require.Len(t, *requests, 2)

	reply := (*requests)[1].contents[len((*requests)[1].contents)-1]
	assert.Equal(t, genai.RoleUser, reply.Role)
	responses := functionResponses(reply)
	require.Len(t, responses, 1)
	assert.Equal(t, "call-1", responses[0].ID)
	assert.Equal(t, "get_file_content", responses[0].Name)
	files := responseFiles(t, responses[0])
	require.Len(t, files, 1)
	assert.Equal(t, "main.go", files[0]["filepath"])
	assert.Equal(t, "package main", files[0]["content"])
}

func TestReviewDiffWithModel_PromptSections(t *testing.T) {
	t.Parallel()
	client, requests := scriptedClient(t, verdictResponse())

	result, err := newTestReviewer(client).ReviewDiff(
		t.Context(), "diff content", []string{"pkg/a.go"}, t.TempDir(),
		WithInstructions("INSTRUCTIONS-MARKER"),
		WithDirectoryListing("pkg/\n- LISTING-MARKER\n"),
	)
	require.NoError(t, err)
	assert.True(t, result.LGTM)
	require.Len(t, *requests, 1)

	prompt := (*requests)[0].contents[0].Parts[0].Text
	instructions := strings.Index(prompt, "INSTRUCTIONS-MARKER")
	criteria := strings.Index(prompt, "Review criteria:")
	header := strings.Index(prompt, "Contents of the directories this change touches")
	listing := strings.Index(prompt, "LISTING-MARKER")
	diff := strings.Index(prompt, "Git diff to review:")
	require.NotEqual(t, -1, instructions, "the instructions must reach the prompt")
	require.NotEqual(t, -1, header, "the listing header must be in the prompt")
	require.NotEqual(t, -1, listing, "the directory listing must reach the prompt")
	assert.Less(t, instructions, criteria, "instructions come before the review criteria")
	assert.Less(t, header, listing, "the listing sits under its header")
	assert.Less(t, listing, diff, "the listing comes before the diff")
}

func TestReviewDiffWithModel_HistoryGrows(t *testing.T) {
	t.Parallel()

	signature := []byte("opaque-signature")
	firstTurn := &genai.Content{Parts: []*genai.Part{
		{Text: "weighing the change", Thought: true},
		{FunctionCall: fileCall("c1", "a.go"), ThoughtSignature: signature},
	}}
	secondTurn := &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
		{FunctionCall: fileCall("c2", "b.go")},
	}}
	client, requests := scriptedClient(t,
		&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{Content: firstTurn}}},
		&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{Content: secondTurn}}},
		verdictResponse(),
	)

	tmpDir := testutil.CreateTempGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package a"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "b.go"), []byte("package b"), 0o600))

	result, err := newTestReviewer(client).ReviewDiff(
		t.Context(), "diff content", []string{"a.go", "b.go"}, tmpDir)
	require.NoError(t, err)
	assert.True(t, result.LGTM)
	require.Len(t, *requests, 3)

	wantRoles := []string{genai.RoleUser, genai.RoleModel, genai.RoleUser, genai.RoleModel, genai.RoleUser}
	for i, req := range *requests {
		n := 1 + 2*i
		require.Len(t, req.contents, n, "request %d history length", i+1)
		for j, content := range req.contents {
			assert.Equal(t, wantRoles[j], content.Role, "request %d content %d role", i+1, j+1)
		}
	}

	last := (*requests)[2].contents
	assert.Same(t, firstTurn, last[1], "model turn is resent verbatim")
	assert.Equal(t, genai.RoleModel, firstTurn.Role)
	require.Len(t, last[1].Parts, 2)
	assert.True(t, last[1].Parts[0].Thought)
	assert.Equal(t, signature, last[1].Parts[1].ThoughtSignature)
	assert.Same(t, secondTurn, last[3])

	first := functionResponses(last[2])
	require.Len(t, first, 1)
	assert.Equal(t, "c1", first[0].ID)
	assert.Equal(t, "package a", responseFiles(t, first[0])[0]["content"])
	second := functionResponses(last[4])
	require.Len(t, second, 1)
	assert.Equal(t, "c2", second[0].ID)
	assert.Equal(t, "package b", responseFiles(t, second[0])[0]["content"])
}

// TestReviewDiffWithModel_ParallelFunctionCalls: the Gemini API rejects a
// reply whose function response count does not match the call count.
func TestReviewDiffWithModel_ParallelFunctionCalls(t *testing.T) {
	t.Parallel()
	client, requests := scriptedClient(t,
		callResponse(fileCall("first", "a.go"), fileCall("second", "b.go", "missing.go")),
		verdictResponse(),
	)

	tmpDir := testutil.CreateTempGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package a"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "b.go"), []byte("package b"), 0o600))

	var fetchedFiles []string
	result, err := newTestReviewer(client).ReviewDiff(
		t.Context(), "diff content", []string{"a.go", "b.go"}, tmpDir,
		WithFileFetchCallback(func(path string) { fetchedFiles = append(fetchedFiles, path) }),
	)
	require.NoError(t, err)
	assert.True(t, result.LGTM)
	assert.Equal(t, []string{"a.go", "b.go", "missing.go"}, fetchedFiles)
	require.Len(t, *requests, 2)

	history := (*requests)[1].contents
	reply := history[len(history)-1]
	require.Len(t, reply.Parts, 2, "one reply message carrying one response part per function call")
	responses := functionResponses(reply)
	require.Len(t, responses, 2)

	assert.Equal(t, "first", responses[0].ID)
	assert.Equal(t, "package a", responseFiles(t, responses[0])[0]["content"])

	assert.Equal(t, "second", responses[1].ID)
	secondFiles := responseFiles(t, responses[1])
	require.Len(t, secondFiles, 2)
	assert.Equal(t, "package b", secondFiles[0]["content"])
	assert.Contains(t, secondFiles[1]["error"], "failed to read file")
}

func TestReviewDiffWithModel_DeletedFile(t *testing.T) {
	t.Parallel()
	client, requests := scriptedClient(t,
		callResponse(fileCall("c1", "gone.go", "kept.go")),
		verdictResponse(),
	)

	tmpDir := testutil.CreateTempGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "kept.go"), []byte("package kept"), 0o600))

	_, err := newTestReviewer(client).ReviewDiff(
		t.Context(), "diff content", []string{"gone.go", "kept.go"}, tmpDir,
		WithDeletedFiles([]string{"gone.go"}),
	)
	require.NoError(t, err)
	require.Len(t, *requests, 2)

	history := (*requests)[1].contents
	responses := functionResponses(history[len(history)-1])
	require.Len(t, responses, 1)
	files := responseFiles(t, responses[0])
	require.Len(t, files, 2)
	assert.Equal(t, errDeletedFileMsg, files[0]["error"])
	assert.Equal(t, "package kept", files[1]["content"])
}

func TestReviewDiffWithModel_RetrievalBudgetSpansParallelCalls(t *testing.T) {
	t.Parallel()
	paths := make([]string, maxFilesPerTurn/2+1)
	for i := range paths {
		paths[i] = "../outside.txt"
	}
	client, requests := scriptedClient(t,
		callResponse(fileCall("first", paths...), fileCall("second", paths...)),
		verdictResponse(),
	)

	_, err := newTestReviewer(client).ReviewDiff(t.Context(), "diff content", []string{"a.go"}, t.TempDir())
	require.NoError(t, err)
	require.Len(t, *requests, 2)

	history := (*requests)[1].contents
	responses := functionResponses(history[len(history)-1])
	require.Len(t, responses, 2)
	first, second := responseFiles(t, responses[0]), responseFiles(t, responses[1])
	require.Len(t, first, len(paths))
	require.Len(t, second, len(paths))

	for _, entry := range first {
		assert.NotEqual(t, errTooManyFilesMsg, entry["error"])
	}
	var served, refused int
	for _, entry := range second {
		if entry["error"] == errTooManyFilesMsg {
			refused++
		} else {
			served++
		}
	}
	assert.Equal(t, maxFilesPerTurn-len(paths), served)
	assert.Equal(t, 2*len(paths)-maxFilesPerTurn, refused)
}

func TestReviewDiffWithModel_RetrievalBudgetResetsEachTurn(t *testing.T) {
	t.Parallel()
	paths := make([]string, maxFilesPerTurn*3/5)
	for i := range paths {
		paths[i] = "../outside.txt"
	}
	client, requests := scriptedClient(t,
		callResponse(fileCall("first", paths...)),
		callResponse(fileCall("second", paths...)),
		verdictResponse(),
	)

	_, err := newTestReviewer(client).ReviewDiff(t.Context(), "diff content", []string{"a.go"}, t.TempDir())
	require.NoError(t, err)
	require.Len(t, *requests, 3)

	history := (*requests)[2].contents
	for _, reply := range []*genai.Content{history[len(history)-3], history[len(history)-1]} {
		responses := functionResponses(reply)
		require.Len(t, responses, 1)
		for _, entry := range responseFiles(t, responses[0]) {
			assert.NotEqual(t, errTooManyFilesMsg, entry["error"], "every turn gets a fresh budget")
		}
	}
}

// TestReviewDiffWithModel_ToolTurnLimit: a model that requests files on every
// turn must not loop (and bill) forever.
func TestReviewDiffWithModel_ToolTurnLimit(t *testing.T) {
	t.Parallel()
	client, requests := recordingClient(func(_ int, req recordedRequest) (*genai.GenerateContentResponse, error) {
		if req.config.ToolConfig != nil {
			return verdictResponse(), nil
		}

		return callResponse(fileCall("a", "main.go"), fileCall("b", "main.go")), nil
	})

	tmpDir := testutil.CreateTempGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main"), 0o600))

	var fetched int
	result, err := newTestReviewer(client).ReviewDiff(
		t.Context(), "diff content", []string{"main.go"}, tmpDir,
		WithFileFetchCallback(func(string) { fetched++ }),
	)
	require.NoError(t, err)
	assert.True(t, result.LGTM)
	assert.Equal(t, "test-model", result.Model)

	// The initial prompt, one request per completed tool turn, and the final
	// verdict request.
	require.Len(t, *requests, 1+maxToolTurns+1)
	assert.Equal(t, 2*maxToolTurns, fetched, "refused calls are not fetched")

	for _, req := range (*requests)[:len(*requests)-1] {
		assertRequestConfig(t, req.config, nil)
		assert.Nil(t, req.config.ToolConfig)
	}

	final := (*requests)[len(*requests)-1]
	assertRequestConfig(t, final.config, nil)
	assertToolsDisabled(t, final.config)

	refusals := functionResponses(final.contents[len(final.contents)-1])
	require.Len(t, refusals, 2, "every pending call is answered")
	for i, id := range []string{"a", "b"} {
		assert.Equal(t, id, refusals[i].ID)
		assert.Equal(t, map[string]any{errorKey: errRetrievalLimitMsg}, refusals[i].Response)
	}
}

func TestReviewDiffWithModel_PromptTokenBudget(t *testing.T) {
	t.Parallel()

	t.Run("budget reached", func(t *testing.T) {
		t.Parallel()
		client, requests := scriptedClient(t,
			withUsage(callResponse(fileCall("c1", "a.go")), maxPromptTokens-100, 5),
			withUsage(callResponse(fileCall("c2", "b.go")), 100, 5),
			verdictResponse(),
		)

		result, err := newTestReviewer(client).ReviewDiff(
			t.Context(), "diff content", []string{"a.go", "b.go"}, t.TempDir())
		require.NoError(t, err)
		assert.True(t, result.LGTM)
		require.Len(t, *requests, 3)

		assert.Nil(t, (*requests)[1].config.ToolConfig)
		final := (*requests)[2]
		assertToolsDisabled(t, final.config)
		refusals := functionResponses(final.contents[len(final.contents)-1])
		require.Len(t, refusals, 1)
		assert.Equal(t, "c2", refusals[0].ID)
		assert.Equal(t, map[string]any{errorKey: errRetrievalLimitMsg}, refusals[0].Response)
	})

	t.Run("budget not reached", func(t *testing.T) {
		t.Parallel()
		client, requests := scriptedClient(t,
			withUsage(callResponse(fileCall("c1", "a.go")), maxPromptTokens-1, 5),
			verdictResponse(),
		)

		result, err := newTestReviewer(client).ReviewDiff(
			t.Context(), "diff content", []string{"a.go"}, t.TempDir())
		require.NoError(t, err)
		assert.True(t, result.LGTM)
		require.Len(t, *requests, 2)
		assert.Nil(t, (*requests)[1].config.ToolConfig)
	})

	t.Run("a verdict is accepted over budget", func(t *testing.T) {
		t.Parallel()
		client, requests := scriptedClient(t, withUsage(verdictResponse(), maxPromptTokens*2, 5))

		result, err := newTestReviewer(client).ReviewDiff(
			t.Context(), "diff content", []string{"a.go"}, t.TempDir())
		require.NoError(t, err)
		assert.True(t, result.LGTM)
		assert.Len(t, *requests, 1)
	})
}

func TestReviewDiffWithModel_ToolCallAfterLimit(t *testing.T) {
	t.Parallel()
	client, requests := scriptedClient(t,
		withUsage(callResponse(fileCall("c1", "a.go")), maxPromptTokens, 5),
		callResponse(fileCall("c2", "b.go")),
	)

	result, err := newTestReviewer(client).ReviewDiff(
		t.Context(), "diff content", []string{"a.go", "b.go"}, t.TempDir())
	require.ErrorIs(t, err, ErrToolCallAfterLimit)
	assert.Nil(t, result)
	assert.Len(t, *requests, 2)
}

func TestReviewDiff_RequestConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		level genai.ThinkingLevel
		want  *genai.ThinkingConfig
	}{
		{
			name:  "configured level is sent on every request",
			level: genai.ThinkingLevelHigh,
			want:  &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelHigh},
		},
		{
			name:  "unset level omits the config",
			level: "",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client, requests := scriptedClient(t,
				callResponse(fileCall("c1", "a.go")),
				callResponse(fileCall("c2", "b.go")),
				verdictResponse(),
			)

			r := newTestReviewer(client)
			r.thinkingLevel = tt.level
			_, err := r.ReviewDiff(t.Context(), "diff content", []string{"a.go", "b.go"}, t.TempDir())
			require.NoError(t, err)
			require.Len(t, *requests, 3)

			for _, req := range *requests {
				assertRequestConfig(t, req.config, tt.want)
				assert.Nil(t, req.config.ToolConfig)
			}
		})
	}
}

func TestReviewDiffWithModel_RequestErrors(t *testing.T) {
	t.Parallel()

	quotaErr := genai.APIError{
		Code:    http.StatusTooManyRequests,
		Message: "Quota exceeded",
		Details: []map[string]any{{"@type": quotaFailureType}},
	}
	tests := []struct {
		name      string
		responses []*genai.GenerateContentResponse
		failure   error
		wantIs    error
		wantOp    string
	}{
		{
			name:    "initial prompt",
			failure: errTest,
			wantIs:  errTest,
			wantOp:  "review_prompt",
		},
		{
			name:      "function response",
			responses: []*genai.GenerateContentResponse{callResponse(fileCall("c1", "a.go"))},
			failure:   errTest,
			wantIs:    errTest,
			wantOp:    "function_response",
		},
		{
			name: "final verdict",
			responses: []*genai.GenerateContentResponse{
				withUsage(callResponse(fileCall("c1", "a.go")), maxPromptTokens, 0),
			},
			failure: errTest,
			wantIs:  errTest,
			wantOp:  "final_verdict",
		},
		{
			name:      "quota exhausted mid-review",
			responses: []*genai.GenerateContentResponse{callResponse(fileCall("c1", "a.go"))},
			failure:   quotaErr,
			wantIs:    ErrQuotaExhausted,
			wantOp:    "function_response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client, requests := recordingClient(func(n int, _ recordedRequest) (*genai.GenerateContentResponse, error) {
				if n < len(tt.responses) {
					return tt.responses[n], nil
				}

				return nil, tt.failure
			})

			result, err := newTestReviewer(client).ReviewDiff(
				t.Context(), "diff content", []string{"a.go"}, t.TempDir())
			require.ErrorIs(t, err, tt.wantIs)
			assert.Contains(t, err.Error(), "gemini request "+tt.wantOp+" failed")
			assert.Nil(t, result)
			assert.Len(t, *requests, len(tt.responses)+1)
		})
	}
}

func TestReviewDiffWithModel_RetriesResendConversation(t *testing.T) {
	t.Parallel()
	client, requests := recordingClient(func(n int, _ recordedRequest) (*genai.GenerateContentResponse, error) {
		switch n {
		case 0:
			return callResponse(fileCall("c1", "a.go")), nil
		case 1:
			return nil, genai.APIError{Code: http.StatusServiceUnavailable, Message: "overloaded"}
		default:
			return verdictResponse(), nil
		}
	})

	r := newTestReviewer(client)
	r.retryConfig = &config.RetryConfig{
		MaxRetries:        new(2),
		InitialBackoff:    "1ms",
		MaxBackoff:        "2ms",
		BackoffMultiplier: 1,
	}

	result, err := r.ReviewDiff(t.Context(), "diff content", []string{"a.go"}, t.TempDir())
	require.NoError(t, err)
	assert.True(t, result.LGTM)
	require.Len(t, *requests, 3)
	assert.Len(t, (*requests)[1].contents, 3)
	assert.Equal(t, (*requests)[1].contents, (*requests)[2].contents)
}

func TestReviewDiffWithModel_TokenUsage(t *testing.T) {
	t.Parallel()
	first := callResponse(fileCall("c1", "a.go"))
	first.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        100,
		CandidatesTokenCount:    50,
		CachedContentTokenCount: 10,
		ThoughtsTokenCount:      20,
	}
	client, _ := scriptedClient(t, first, withUsage(verdictResponse(), 200, 80))

	logger := newCapturingLogger()
	r := newTestReviewer(client)
	r.modelName = "gemini-3.1-pro-preview"
	r.logger = logger

	result, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, result.TokenUsage)
	assert.Equal(t, int32(300), result.TokenUsage.PromptTokens)
	assert.Equal(t, int32(130), result.TokenUsage.CandidatesTokens)
	assert.Equal(t, int32(10), result.TokenUsage.CachedTokens)
	assert.Equal(t, int32(20), result.TokenUsage.ThoughtsTokens)
	assert.Greater(t, result.CostUSD, float64(0))
	// The 10 cached tokens flow through to a positive savings on the result.
	assert.Greater(t, result.CacheSavingsUSD, float64(0))

	requests, ok := logger.field("Token usage", "requests")
	require.True(t, ok, "the token usage log line must report the request count")
	assert.Equal(t, 2, requests)
}

func TestRetryableOperation_NilConfig(t *testing.T) {
	t.Parallel()
	r := &Reviewer{retryConfig: nil, logger: testutil.NewTestLogger()}
	called := false
	err := r.retryableOperation(t.Context(), func() error {
		called = true
		return nil
	}, "test")
	require.NoError(t, err)
	assert.True(t, called)
}

func TestRetryableOperation_NilConfigQuotaError(t *testing.T) {
	t.Parallel()
	r := &Reviewer{retryConfig: nil, logger: testutil.NewTestLogger()}
	quotaErr := genai.APIError{
		Code:    http.StatusTooManyRequests,
		Message: "quota exceeded",
		Details: []map[string]any{{"@type": "type.googleapis.com/google.rpc.QuotaFailure"}},
	}
	err := r.retryableOperation(t.Context(), func() error {
		return quotaErr
	}, "test")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrQuotaExhausted)
}

func TestRetryableOperation_ContextCancelled(t *testing.T) {
	t.Parallel()
	r := &Reviewer{
		retryConfig: &config.RetryConfig{
			MaxRetries:        new(3),
			InitialBackoff:    "100ms",
			MaxBackoff:        "1s",
			BackoffMultiplier: 1.5,
		},
		logger: testutil.NewTestLogger(),
	}
	ctx, cancel := context.WithCancel(t.Context())

	attempts := 0
	err := r.retryableOperation(ctx, func() error {
		attempts++
		if attempts == 1 {
			cancel() // Cancel context after first attempt.
		}
		return genai.APIError{Code: http.StatusTooManyRequests, Message: "rate limited"}
	}, "test")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestCalculateBackoff_InvalidStrings(t *testing.T) {
	t.Parallel()
	cfg := &config.RetryConfig{
		InitialBackoff:    "invalid",
		MaxBackoff:        "invalid",
		BackoffMultiplier: 1.5,
	}
	backoff := calculateBackoff(0, cfg)
	// Should fall back to defaults: 1s initial, 60s max.
	assert.Greater(t, backoff, 500*time.Millisecond)
	assert.LessOrEqual(t, backoff, 2*time.Second)
}

func TestCalculateBackoff_CappedAtMax(t *testing.T) {
	t.Parallel()
	cfg := &config.RetryConfig{
		InitialBackoff:    "1s",
		MaxBackoff:        "5s",
		BackoffMultiplier: 10,
	}
	backoff := calculateBackoff(10, cfg)
	assert.LessOrEqual(t, backoff, 6*time.Second) // 5s max + jitter.
}

// TestHandleFileRetrieval_TOCTOUHappyPath is a regression test for the
// os.Root-based open added to close a TOCTOU window between the symlink
// validation and the file read. A deterministic race test is impractical, so
// we assert that the happy paths still work: regular files in the repo root
// and in a subdirectory, plus larger content that exercises the limited read
// path, all succeed through the rooted open.
func TestHandleFileRetrieval_TOCTOUHappyPath(t *testing.T) {
	t.Parallel()
	repoDir := testutil.CreateTempGitRepo(t)

	rootFile := filepath.Join(repoDir, "root.txt")
	require.NoError(t, os.WriteFile(rootFile, []byte("root content"), 0o600))

	subDir := filepath.Join(repoDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0o750))
	subFile := filepath.Join(subDir, "nested.txt")
	require.NoError(t, os.WriteFile(subFile, []byte("nested content"), 0o600))

	// Larger payload to ensure io.ReadAll handles multi-buffer reads.
	bigContent := strings.Repeat("payload data ", 4096)
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "big.txt"), []byte(bigContent), 0o600))

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, testutil.NewTestLogger())
	require.NoError(t, err)

	tests := []fileRetrievalTest{
		{
			name:          "regular file at repo root",
			filepath:      "root.txt",
			shouldSucceed: true,
			expectContent: "root content",
		},
		{
			name:          "regular file in subdirectory",
			filepath:      "sub/nested.txt",
			shouldSucceed: true,
			expectContent: "nested content",
		},
		{
			name:          "large regular file",
			filepath:      "big.txt",
			shouldSucceed: true,
			expectContent: bigContent,
		},
	}

	runFileRetrievalTests(t, reviewer, repoDir, tests)
}

// TestHandleFileRetrieval_ParentSymlinkEscape ensures that an in-repo
// directory entry that is a symlink to outside the repo cannot be used to
// read files there. os.Root rejects path resolution that escapes the root,
// even via parent components, but git check-ignore may also fail first; we
// only require that no file contents are returned.
func TestHandleFileRetrieval_ParentSymlinkEscape(t *testing.T) {
	t.Parallel()
	repoDir := testutil.CreateTempGitRepo(t)

	outsideDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("secret content"), 0o600))

	// Plant an in-repo directory name that is actually a symlink pointing
	// to a directory outside the repository.
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(repoDir, "escape")))

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, testutil.NewTestLogger())
	require.NoError(t, err)

	entry := retrieveSingle(t, reviewer, repoDir, "escape/secret.txt", nil)
	errMsg, ok := entry["error"].(string)
	require.True(t, ok, "expected error entry, got %+v", entry)
	assert.NotContains(t, entry, "content", "must not return content for parent-symlink escape")
	assert.NotContains(t, errMsg, "secret content")
}

// TestHandleFileRetrieval_FileSizeLimit ensures that a file larger than the
// maximum allowed size is rejected rather than read into memory, so the
// review process cannot be OOM'd by an attacker-supplied or runaway request.
func TestHandleFileRetrieval_FileSizeLimit(t *testing.T) {
	t.Parallel()
	repoDir := testutil.CreateTempGitRepo(t)

	bigFile := filepath.Join(repoDir, "big.bin")
	// Write maxRetrievedFileSize+1 bytes so the limit is just exceeded.
	require.NoError(t, os.WriteFile(bigFile, make([]byte, maxRetrievedFileSize+1), 0o600))

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, testutil.NewTestLogger())
	require.NoError(t, err)

	entry := retrieveSingle(t, reviewer, repoDir, "big.bin", nil)
	errMsg, ok := entry["error"].(string)
	require.True(t, ok, "expected error entry, got %+v", entry)
	assert.Contains(t, errMsg, "file too large")
	assert.NotContains(t, entry, "content", "must not return content when size limit is exceeded")
}

// TestHandleFileRetrieval_SymlinkToIgnoredFile ensures an in-repo symlink
// cannot launder gitignored content: check-ignore matches the requested name
// only, so without re-checking the resolved target, "config-link -> .env"
// would expose the ignored .env. A symlink to a non-ignored in-repo file must
// still be followed (the documented follow-symlink contract).
func TestHandleFileRetrieval_SymlinkToIgnoredFile(t *testing.T) {
	t.Parallel()
	repoDir := testutil.CreateTempGitRepo(t)

	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte(".env\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".env"), []byte("API_KEY=secret123"), 0o600))
	require.NoError(t, os.Symlink(".env", filepath.Join(repoDir, "config-link")))

	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "real.txt"), []byte("real content"), 0o600))
	require.NoError(t, os.Symlink("real.txt", filepath.Join(repoDir, "alias.txt")))

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, testutil.NewTestLogger())
	require.NoError(t, err)

	tests := []fileRetrievalTest{
		{
			name:          "symlink to gitignored file is denied",
			filepath:      "config-link",
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          "symlink to non-ignored file is followed",
			filepath:      "alias.txt",
			shouldSucceed: true,
			expectContent: "real content",
		},
	}

	runFileRetrievalTests(t, reviewer, repoDir, tests)
}

// TestHandleFileRetrieval_GitDirectory: ignore rules never cover .git, whose
// config can hold remote URLs with embedded credentials.
func TestHandleFileRetrieval_GitDirectory(t *testing.T) {
	t.Parallel()
	repoDir := testutil.CreateTempGitRepo(t)

	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "normal.txt"), []byte("normal content"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(".git", "config"), filepath.Join(repoDir, "config-link")))
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, ".github"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".github", "ci.yml"), []byte("ci content"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "vendor", "dep", ".git"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(repoDir, "vendor", "dep", ".git", "config"), []byte("nested config"), 0o600))

	reviewer := &Reviewer{logger: testutil.NewTestLogger()}
	tests := []fileRetrievalTest{
		{name: "repository config", filepath: ".git/config", expectedError: errGitDirMsg},
		{name: "repository HEAD", filepath: ".git/HEAD", expectedError: errGitDirMsg},
		{name: "the directory itself", filepath: ".git", expectedError: errGitDirMsg},
		{name: "redundant components", filepath: "./.git/config", expectedError: errGitDirMsg},
		{name: "different case", filepath: ".GIT/config", expectedError: errGitDirMsg},
		{name: "nested repository", filepath: "vendor/dep/.git/config", expectedError: errGitDirMsg},
		{name: "symlink into .git", filepath: "config-link", expectedError: errGitDirMsg},
		{name: "ordinary file", filepath: "normal.txt", shouldSucceed: true, expectContent: "normal content"},
		{
			name:          "directory with a similar name",
			filepath:      ".github/ci.yml",
			shouldSucceed: true,
			expectContent: "ci content",
		},
	}

	runFileRetrievalTests(t, reviewer, repoDir, tests)
}

// TestReviewDiff_NoFallbackWhenUnset ensures an empty fallback model (as on a
// hand-constructed Reviewer) disables fallback instead of issuing a request
// with an empty model name.
func TestReviewDiff_NoFallbackWhenUnset(t *testing.T) {
	t.Parallel()
	callCount := 0
	client := newStubClientWithGenerateContent(func(
		_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
	) (*genai.GenerateContentResponse, error) {
		callCount++
		return nil, errors.Join(ErrQuotaExhausted, fmt.Errorf("quota exceeded: %w", errTest))
	})

	r := &Reviewer{
		client:        client,
		modelName:     "primary-model",
		fallbackModel: "",
		promptManager: prompts.New(""),
		logger:        testutil.NewTestLogger(),
	}

	_, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.ErrorIs(t, err, ErrQuotaExhausted)
	assert.Equal(t, 1, callCount, "must not retry with an empty fallback model")
}

func TestReviewDiffWithModel_ThoughtPartSkipped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parts []*genai.Part
	}{
		{
			name: "leading thought",
			parts: []*genai.Part{
				{Text: "thinking about the verdict", Thought: true},
				{Text: `{"lgtm": true, "comments": "OK"}`},
			},
		},
		{
			name: "trailing thought",
			parts: []*genai.Part{
				{Text: `{"lgtm": true, "comments": "OK"}`},
				{Text: "internal reasoning summary", Thought: true},
			},
		},
		{
			name: "verdict split across parts",
			parts: []*genai.Part{
				{Text: "thinking", Thought: true},
				{Text: `{"lgtm": true, `},
				{Text: `"comments": "OK"}`},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client, _ := scriptedClient(t, contentResponse(tt.parts...))

			result, err := newTestReviewer(client).ReviewDiff(
				t.Context(), "diff content", []string{"file.go"}, "/repo")
			require.NoError(t, err)
			assert.True(t, result.LGTM)
			assert.Equal(t, "OK", result.Comments)
		})
	}

	t.Run("only thought text is empty", func(t *testing.T) {
		t.Parallel()
		client, _ := scriptedClient(t, contentResponse(&genai.Part{Text: "just thinking", Thought: true}))

		_, err := newTestReviewer(client).ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
		require.ErrorIs(t, err, ErrEmptyResponse)
	})
}

// TestNew_ThinkingLevel verifies the config's thinking_level reaches the
// Reviewer as the SDK enum, with the same default config.Load applies.
func TestNew_ThinkingLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured string
		want       genai.ThinkingLevel
	}{
		{name: "config value maps to the SDK enum", configured: "low", want: genai.ThinkingLevelLow},
		{name: "unset defaults to high", configured: "", want: genai.ThinkingLevelHigh},
		{name: "none omits the level", configured: config.ThinkingLevelNone, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.NewTestConfig()
			cfg.Gemini.ThinkingLevel = tt.configured

			reviewer, err := New(cfg, testutil.NewTestLogger())
			require.NoError(t, err)
			assert.Equal(t, tt.want, reviewer.thinkingLevel)
		})
	}
}

// TestPricingFor verifies introductory rates apply only before their end
// instant and that models without one always bill at the standard rate.
func TestPricingFor(t *testing.T) {
	t.Parallel()

	before := flashIntroductoryPricingEnd.Add(-time.Second)
	tests := []struct {
		name  string
		model string
		at    time.Time
		want  modelPricing
		known bool
	}{
		{
			name:  "introductory rate before the cutoff",
			model: "gemini-3.8-flash",
			at:    before,
			want:  modelPricing{InputPrice: 0.75, OutputPrice: 3.75},
			known: true,
		},
		{
			name:  "standard rate from the cutoff instant",
			model: "gemini-3.8-flash",
			at:    flashIntroductoryPricingEnd,
			want:  modelPricing{InputPrice: 1.50, OutputPrice: 7.50},
			known: true,
		},
		{
			name:  "3.7 flash shares the introductory rate",
			model: "gemini-3.7-flash",
			at:    before,
			want:  modelPricing{InputPrice: 0.75, OutputPrice: 3.75},
			known: true,
		},
		{
			name:  "3.6 flash shares the introductory rate",
			model: "gemini-3.6-flash",
			at:    before,
			want:  modelPricing{InputPrice: 0.75, OutputPrice: 3.75},
			known: true,
		},
		{
			name:  "model without an introductory rate",
			model: "gemini-3.1-pro-preview",
			at:    before,
			want:  modelPricing{InputPrice: 2.00, OutputPrice: 12.00},
			known: true,
		},
		{
			name:  "unknown model",
			model: "unknown-model",
			at:    before,
			known: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, known := pricingFor(tt.model, tt.at)
			assert.Equal(t, tt.known, known)
			assert.InDelta(t, tt.want.InputPrice, got.InputPrice, 1e-9)
			assert.InDelta(t, tt.want.OutputPrice, got.OutputPrice, 1e-9)
		})
	}
}

// TestReviewDiff_FallbackCarriesThinkingConfig verifies the documented
// contract that a quota-triggered fallback attempt sends the same thinking
// level as the primary attempt, on every request.
func TestReviewDiff_FallbackCarriesThinkingConfig(t *testing.T) {
	t.Parallel()

	want := &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelHigh}
	client, requests := recordingClient(func(n int, req recordedRequest) (*genai.GenerateContentResponse, error) {
		if req.model == "primary-model" {
			if n == 0 {
				return callResponse(fileCall("c1", "a.go")), nil
			}

			return nil, errors.Join(ErrQuotaExhausted, genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Quota exceeded",
				Details: []map[string]any{{"@type": quotaFailureType}},
			})
		}

		if len(req.contents) == 1 {
			return callResponse(fileCall("c2", "a.go")), nil
		}

		return verdictResponse(), nil
	})

	r := newTestReviewer(client)
	r.modelName = "primary-model"
	r.fallbackModel = "fallback-model"
	r.thinkingLevel = genai.ThinkingLevelHigh

	result, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "fallback-model", result.Model)

	seen := map[string]int{}
	for _, req := range *requests {
		seen[req.model]++
		assertRequestConfig(t, req.config, want)
	}
	assert.Equal(t, map[string]int{"primary-model": 2, "fallback-model": 2}, seen)
}

// TestApplyAggregateSpend_PricesAsOfInstant verifies the aggregation wrapper
// prices each model's spend as of the instant it is given, so a review is
// billed at the rate in effect when it finished.
func TestApplyAggregateSpend_PricesAsOfInstant(t *testing.T) {
	t.Parallel()

	spends := []modelSpend{{model: "gemini-3.8-flash", usage: tokenUsage{PromptTokens: 1_000_000}}}

	introductory := &Result{}
	applyAggregateSpend(introductory, spends, flashIntroductoryPricingEnd.Add(-time.Second))
	assert.InDelta(t, 0.75, introductory.CostUSD, 1e-9)

	standard := &Result{}
	applyAggregateSpend(standard, spends, flashIntroductoryPricingEnd)
	assert.InDelta(t, 1.50, standard.CostUSD, 1e-9)
}
