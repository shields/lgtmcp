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

package review

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"msrl.dev/lgtmcp/internal/config"
	"msrl.dev/lgtmcp/internal/prompts"
	"msrl.dev/lgtmcp/internal/testutil"
)

var errTest = errors.New("test error")

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

type fileRetrievalTest struct {
	name          string
	filepath      string
	expectContent string
	shouldSucceed bool
	expectedError string
}

func runFileRetrievalTests(t *testing.T, reviewer *Reviewer, repoDir string, tests []fileRetrievalTest) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			funcCall := &genai.FunctionCall{
				Name: "get_file_content",
				Args: map[string]any{
					"filepath": tt.filepath,
				},
			}

			result := reviewer.handleFileRetrieval(funcCall, repoDir)

			var response map[string]any
			if result.FunctionResponse != nil {
				response = result.FunctionResponse.Response
			}

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
	funcCall := &genai.FunctionCall{
		Name: "get_file_content",
		Args: map[string]any{
			"filepath": "test.txt",
		},
	}

	result := reviewer.handleFileRetrieval(funcCall, nonGitDir)

	var response map[string]any
	if result.FunctionResponse != nil {
		response = result.FunctionResponse.Response
	}

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
			// Create function call.
			funcCall := &genai.FunctionCall{
				Name: "retrieve_file",
				Args: map[string]any{
					"filepath": tt.filepath,
				},
			}

			t.Logf("Testing with filepath: %s, repoDir: %s", tt.filepath, repoDir)
			result := reviewer.handleFileRetrieval(funcCall, repoDir)

			// Extract the response from the FunctionResponse.
			var response map[string]any
			if result.FunctionResponse != nil {
				response = result.FunctionResponse.Response
			}

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
				// Should either have an error or not be able to read the file.
				if errMsg, hasError := response["error"].(string); hasError {
					// Good - we got an error.
					t.Logf("Got expected error: %s", errMsg)
				} else if content, hasContent := response["content"].(string); hasContent {
					// Bad - we shouldn't be able to read sensitive files.
					assert.NotContains(t, content, "sensitive content",
						"SECURITY VULNERABILITY: Path traversal allowed access to %s", tt.filepath)
				}
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
		cfg.Gemini.Model = "gemini-1.5-pro"

		reviewer, err := New(cfg, testutil.NewTestLogger())
		require.NoError(t, err)
		assert.NotNil(t, reviewer)
	})

	t.Run("uses default model when not specified", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestConfig()
		// Cfg already has default model set to gemini-3.1-pro-preview.

		reviewer, err := New(cfg, testutil.NewTestLogger())
		require.NoError(t, err)
		assert.NotNil(t, reviewer)
	})
}

func TestHandleFileRetrieval(t *testing.T) {
	t.Parallel()
	r := &Reviewer{logger: testutil.NewTestLogger()}
	tmpDir := testutil.CreateTempGitRepo(t)

	// Create a test file.
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := "test file content"
	require.NoError(t, os.WriteFile(testFile, []byte(testContent), 0o600))

	t.Run("successful file retrieval", func(t *testing.T) {
		t.Parallel()
		funcCall := &genai.FunctionCall{
			Name: "get_file_content",
			Args: map[string]any{
				"filepath": "test.txt",
			},
		}

		response := r.handleFileRetrieval(funcCall, tmpDir)
		assert.NotNil(t, response)

		// Check the response contains the file content.
		if response.FunctionResponse != nil {
			assert.Equal(t, "get_file_content", response.FunctionResponse.Name)
			assert.Equal(t, testContent, response.FunctionResponse.Response["content"])
		}
	})

	t.Run("missing filepath parameter", func(t *testing.T) {
		t.Parallel()
		funcCall := &genai.FunctionCall{
			Name: "get_file_content",
			Args: map[string]any{},
		}

		response := r.handleFileRetrieval(funcCall, tmpDir)
		assert.NotNil(t, response)

		// Check for error response.
		if response.FunctionResponse != nil {
			assert.Contains(t, response.FunctionResponse.Response["error"], "filepath parameter must be a string")
		}
	})

	t.Run("path traversal attempt", func(t *testing.T) {
		t.Parallel()
		funcCall := &genai.FunctionCall{
			Name: "get_file_content",
			Args: map[string]any{
				"filepath": "../../../etc/passwd",
			},
		}

		response := r.handleFileRetrieval(funcCall, tmpDir)
		assert.NotNil(t, response)

		// Check for error response.
		if response.FunctionResponse != nil {
			assert.Contains(t, response.FunctionResponse.Response["error"], "path traversal not allowed")
		}
	})

	t.Run("file not found", func(t *testing.T) {
		t.Parallel()
		funcCall := &genai.FunctionCall{
			Name: "get_file_content",
			Args: map[string]any{
				"filepath": "nonexistent.txt",
			},
		}

		response := r.handleFileRetrieval(funcCall, tmpDir)
		assert.NotNil(t, response)

		// Check for error response.
		if response.FunctionResponse != nil {
			assert.Contains(t, response.FunctionResponse.Response["error"], "failed to read file")
		}
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
			client: &StubGeminiClient{
				CreateChatFunc: func(ctx context.Context, _ string, _ *genai.GenerateContentConfig) (GeminiChat, error) {
					// Check if context is canceled.
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					default:
						return &StubGeminiChat{}, nil
					}
				},
			},
			modelName:     "gemini-3.1-pro-preview",
			temperature:   0.2,
			promptManager: prompts.New("", ""),
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
			err: &genai.APIError{
				Code:    http.StatusRequestTimeout,
				Message: "Request Timeout",
			},
			expected: true,
		},
		{
			name: "APIError with 429 status code",
			err: &genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "You exceeded your current quota",
				Status:  "RESOURCE_EXHAUSTED",
			},
			expected: true,
		},
		{
			name: "APIError with 500 status code",
			err: &genai.APIError{
				Code:    http.StatusInternalServerError,
				Message: "Internal Server Error",
			},
			expected: true,
		},
		{
			name: "APIError with 501 status code (Not Implemented - NOT retryable)",
			err: &genai.APIError{
				Code:    http.StatusNotImplemented,
				Message: "Not Implemented",
			},
			expected: false,
		},
		{
			name: "APIError with 502 status code",
			err: &genai.APIError{
				Code:    http.StatusBadGateway,
				Message: "Bad Gateway",
			},
			expected: true,
		},
		{
			name: "APIError with 503 status code",
			err: &genai.APIError{
				Code:    http.StatusServiceUnavailable,
				Message: "Service Unavailable",
			},
			expected: true,
		},
		{
			name: "APIError with 504 status code (Gateway Timeout)",
			err: &genai.APIError{
				Code:    http.StatusGatewayTimeout,
				Message: "Gateway Timeout",
			},
			expected: true,
		},
		{
			name: "APIError with INTERNAL status",
			err: &genai.APIError{
				Code:   0, // No HTTP code.
				Status: "INTERNAL",
			},
			expected: true,
		},
		{
			name: "APIError with UNAVAILABLE status",
			err: &genai.APIError{
				Code:   0,
				Status: "UNAVAILABLE",
			},
			expected: true,
		},
		{
			name: "APIError with DEADLINE_EXCEEDED status",
			err: &genai.APIError{
				Code:   0,
				Status: "DEADLINE_EXCEEDED",
			},
			expected: true,
		},
		{
			name: "APIError with 403 (non-retryable)",
			err: &genai.APIError{
				Code:    http.StatusForbidden,
				Message: "Permission Denied",
			},
			expected: false,
		},
		{
			name: "APIError with 401 (non-retryable)",
			err: &genai.APIError{
				Code:    http.StatusUnauthorized,
				Message: "Unauthorized",
			},
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
			err: &genai.APIError{
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
			err: &genai.APIError{
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
			err: &genai.APIError{
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
			err: &genai.APIError{
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
			err: &genai.APIError{
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
			err: &genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Rate limit exceeded",
			},
			expected: 0,
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
			MaxRetries:        3,
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
			MaxRetries:        3,
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
			MaxRetries:        3,
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
			MaxRetries:        2,
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
			MaxRetries:        3,
			InitialBackoff:    "100ms",
			MaxBackoff:        "1s",
			BackoffMultiplier: 2.0,
		}

		reviewer := &Reviewer{retryConfig: cfg, logger: testutil.NewTestLogger()}
		callCount := 0

		ctx, cancel := context.WithCancel(t.Context())

		// Cancel context after first attempt.
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		err := reviewer.retryableOperation(ctx, func() error {
			callCount++

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

	t.Run("total returns sum of prompt, candidates, and thoughts", func(t *testing.T) {
		t.Parallel()
		usage := &tokenUsage{
			PromptTokens:     1000,
			CandidatesTokens: 500,
			ThoughtsTokens:   200,
			CachedTokens:     100, // Not included in total (separate metric).
		}

		assert.Equal(t, int32(1700), usage.total())
	})

	t.Run("cost calculation", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name             string
			modelName        string
			promptTokens     int32
			candidatesTokens int32
			thoughtsTokens   int32
			cachedTokens     int32
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
				name:             "flash model pricing",
				modelName:        "gemini-1.5-flash",
				promptTokens:     1_000_000, // 1M input = $0.075
				candidatesTokens: 1_000_000, // 1M output = $0.30
				expectedCost:     0.375,
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
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				usage := &tokenUsage{
					PromptTokens:     tt.promptTokens,
					CandidatesTokens: tt.candidatesTokens,
					ThoughtsTokens:   tt.thoughtsTokens,
					CachedTokens:     tt.cachedTokens,
				}

				cost := usage.cost(tt.modelName)
				assert.InDelta(t, tt.expectedCost, cost, 0.0001,
					"cost should be approximately $%.4f", tt.expectedCost)
			})
		}
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
			err: &genai.APIError{
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
			err: &genai.APIError{
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
			err: &genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Too many requests",
			},
			expected: false,
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
		MaxRetries:        3,
		InitialBackoff:    "10ms",
		MaxBackoff:        "100ms",
		BackoffMultiplier: 2.0,
	}

	reviewer := &Reviewer{retryConfig: cfg, logger: testutil.NewTestLogger()}
	callCount := 0

	// Create a quota exhaustion error.
	quotaErr := &genai.APIError{
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
	quotaErr := &genai.APIError{
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

func TestNewWithClient(t *testing.T) {
	t.Parallel()
	client := &StubGeminiClient{}
	pm := prompts.New("", "")
	retryConfig := &config.RetryConfig{MaxRetries: 3, InitialBackoff: "1s", MaxBackoff: "30s", BackoffMultiplier: 2}
	r := NewWithClient(client, "test-model", 0.5, retryConfig, pm)
	assert.NotNil(t, r)
	assert.Equal(t, "test-model", r.modelName)
	assert.InDelta(t, float32(0.5), r.temperature, 0.01)
	assert.Equal(t, retryConfig, r.retryConfig)
}

func TestIsTestMode(t *testing.T) {
	t.Run("test mode on", func(t *testing.T) {
		t.Setenv("LGTMCP_TEST_MODE", "true")
		assert.True(t, IsTestMode())
	})
	t.Run("test mode off", func(t *testing.T) {
		t.Setenv("LGTMCP_TEST_MODE", "false")
		assert.False(t, IsTestMode())
	})
	t.Run("test mode unset", func(t *testing.T) {
		t.Setenv("LGTMCP_TEST_MODE", "")
		assert.False(t, IsTestMode())
	})
}

func TestStubGeminiClient_DefaultPaths(t *testing.T) {
	t.Parallel()
	client := &StubGeminiClient{}

	// CreateChat default returns a StubGeminiChat.
	chat, err := client.CreateChat(t.Context(), "model", nil)
	require.NoError(t, err)
	assert.NotNil(t, chat)

	// GenerateContent default returns approval.
	resp, err := client.GenerateContent(t.Context(), "model", nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.Candidates)

	// StubGeminiChat default returns approval.
	stubChat := &StubGeminiChat{}
	resp, err = stubChat.SendMessage(t.Context(), genai.Part{Text: "hi"})
	require.NoError(t, err)
	assert.NotNil(t, resp)
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
			return nil, errors.Join(ErrQuotaExhausted, &genai.APIError{
				Code:    http.StatusTooManyRequests,
				Message: "Quota exceeded",
				Details: []map[string]any{{"@type": "type.googleapis.com/google.rpc.QuotaFailure"}},
			})
		}
		// Fallback model succeeds.
		return &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{Content: &genai.Content{
				Parts: []*genai.Part{{Text: `{"lgtm": true, "comments": "OK"}`}},
			}}},
		}, nil
	})

	r := &Reviewer{
		client:        client,
		modelName:     "primary-model",
		fallbackModel: "fallback-model",
		temperature:   0.2,
		promptManager: prompts.New("", ""),
		logger:        testutil.NewTestLogger(),
	}

	result, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.NoError(t, err)
	assert.True(t, result.LGTM)
	assert.Equal(t, 2, callCount)
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
		temperature:   0.2,
		promptManager: prompts.New("", ""),
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
		temperature:   0.2,
		promptManager: prompts.New("", ""),
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
		temperature:   0.2,
		promptManager: prompts.New("", ""),
		logger:        testutil.NewTestLogger(),
	}

	_, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.ErrorIs(t, err, ErrNoResponse)
}

// newStubClientWithGenerateContent creates a StubGeminiClient where chat
// always succeeds and GenerateContentFunc is provided by the caller.
func newStubClientWithGenerateContent(fn generateContentFn) *StubGeminiClient {
	return &StubGeminiClient{
		CreateChatFunc: func(_ context.Context, _ string, _ *genai.GenerateContentConfig) (GeminiChat, error) {
			return &StubGeminiChat{
				SendMessageFunc: func(_ context.Context, _ genai.Part) (*genai.GenerateContentResponse, error) {
					return &genai.GenerateContentResponse{
						Candidates: []*genai.Candidate{{Content: &genai.Content{
							Parts: []*genai.Part{{Text: "Analysis done"}},
						}}},
					}, nil
				},
			}, nil
		},
		GenerateContentFunc: fn,
	}
}

func TestReviewDiffWithModel_EmptyResponse(t *testing.T) {
	t.Parallel()
	client := newStubClientWithGenerateContent(
		func(
			_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
		) (*genai.GenerateContentResponse, error) {
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{Content: &genai.Content{
					Parts: []*genai.Part{{Text: ""}}, // Empty text.
				}}},
			}, nil
		},
	)

	r := &Reviewer{
		client:        client,
		modelName:     "test-model",
		temperature:   0.2,
		promptManager: prompts.New("", ""),
		logger:        testutil.NewTestLogger(),
	}

	_, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.ErrorIs(t, err, ErrEmptyResponse)
}

func TestReviewDiffWithModel_JSONParseError(t *testing.T) {
	t.Parallel()
	client := newStubClientWithGenerateContent(
		func(
			_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
		) (*genai.GenerateContentResponse, error) {
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{Content: &genai.Content{
					Parts: []*genai.Part{{Text: "not valid json"}},
				}}},
			}, nil
		},
	)

	r := &Reviewer{
		client:        client,
		modelName:     "test-model",
		temperature:   0.2,
		promptManager: prompts.New("", ""),
		logger:        testutil.NewTestLogger(),
	}

	_, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse review response")
}

func TestReviewDiffWithModel_ToolCallLoop(t *testing.T) {
	t.Parallel()
	callCount := 0
	client := &StubGeminiClient{
		CreateChatFunc: func(_ context.Context, _ string, _ *genai.GenerateContentConfig) (GeminiChat, error) {
			return &StubGeminiChat{
				SendMessageFunc: func(_ context.Context, _ genai.Part) (*genai.GenerateContentResponse, error) {
					callCount++
					if callCount == 1 {
						// First call: return a function call.
						return &genai.GenerateContentResponse{
							Candidates: []*genai.Candidate{{Content: &genai.Content{
								Parts: []*genai.Part{{
									FunctionCall: &genai.FunctionCall{
										Name: "get_file_content",
										Args: map[string]any{"filepath": "main.go"},
									},
								}},
							}}},
						}, nil
					}
					// Second call (function response): return text analysis.
					return &genai.GenerateContentResponse{
						Candidates: []*genai.Candidate{{Content: &genai.Content{
							Parts: []*genai.Part{{Text: "Analysis with file context"}},
						}}},
					}, nil
				},
			}, nil
		},
		GenerateContentFunc: func(
			_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
		) (*genai.GenerateContentResponse, error) {
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{Content: &genai.Content{
					Parts: []*genai.Part{{Text: `{"lgtm": true, "comments": "Good"}`}},
				}}},
			}, nil
		},
	}

	tmpDir := testutil.CreateTempGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main"), 0o600))

	r := &Reviewer{
		client:        client,
		modelName:     "test-model",
		temperature:   0.2,
		promptManager: prompts.New("", ""),
		logger:        testutil.NewTestLogger(),
	}

	fetchedFiles := []string{}
	result, err := r.ReviewDiff(t.Context(), "diff content", []string{"main.go"}, tmpDir,
		WithFileFetchCallback(func(path string) { fetchedFiles = append(fetchedFiles, path) }),
		WithInstructions("test instructions"),
	)
	require.NoError(t, err)
	assert.True(t, result.LGTM)
	assert.Equal(t, []string{"main.go"}, fetchedFiles)
}

func TestReviewDiffWithModel_ChatCreationError(t *testing.T) {
	t.Parallel()
	client := &StubGeminiClient{
		CreateChatFunc: func(_ context.Context, _ string, _ *genai.GenerateContentConfig) (GeminiChat, error) {
			return nil, fmt.Errorf("chat creation failed: %w", errTest)
		},
	}

	r := &Reviewer{
		client:        client,
		modelName:     "test-model",
		temperature:   0.2,
		promptManager: prompts.New("", ""),
		logger:        testutil.NewTestLogger(),
	}

	_, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create chat session")
}

func TestReviewDiffWithModel_InitialPromptError(t *testing.T) {
	t.Parallel()
	client := &StubGeminiClient{
		CreateChatFunc: func(_ context.Context, _ string, _ *genai.GenerateContentConfig) (GeminiChat, error) {
			return &StubGeminiChat{
				SendMessageFunc: func(_ context.Context, _ genai.Part) (*genai.GenerateContentResponse, error) {
					return nil, fmt.Errorf("send failed: %w", errTest)
				},
			}, nil
		},
	}

	r := &Reviewer{
		client:        client,
		modelName:     "test-model",
		temperature:   0.2,
		promptManager: prompts.New("", ""),
		logger:        testutil.NewTestLogger(),
	}

	_, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send message to Gemini")
}

func TestReviewDiffWithModel_ReviewPromptError(t *testing.T) {
	t.Parallel()
	client := newStubClientWithGenerateContent(func(
		_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
	) (*genai.GenerateContentResponse, error) {
		return nil, fmt.Errorf("generate failed: %w", errTest)
	})

	r := &Reviewer{
		client:        client,
		modelName:     "test-model",
		temperature:   0.2,
		promptManager: prompts.New("", ""),
		logger:        testutil.NewTestLogger(),
	}

	_, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get review response")
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
	quotaErr := &genai.APIError{
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
			MaxRetries:        3,
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
		return &genai.APIError{Code: http.StatusTooManyRequests, Message: "rate limited"}
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

func TestHandleFileRetrieval_NonStringFilepath(t *testing.T) {
	t.Parallel()
	r := &Reviewer{logger: testutil.NewTestLogger()}
	funcCall := &genai.FunctionCall{
		Name: "get_file_content",
		Args: map[string]any{"filepath": 123},
	}
	resp := r.handleFileRetrieval(funcCall, "/repo")
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.FunctionResponse)
}

func TestReviewDiffWithModel_TokenUsage(t *testing.T) {
	t.Parallel()
	client := &StubGeminiClient{
		CreateChatFunc: func(_ context.Context, _ string, _ *genai.GenerateContentConfig) (GeminiChat, error) {
			return &StubGeminiChat{
				SendMessageFunc: func(_ context.Context, _ genai.Part) (*genai.GenerateContentResponse, error) {
					return &genai.GenerateContentResponse{
						Candidates: []*genai.Candidate{{Content: &genai.Content{
							Parts: []*genai.Part{{Text: "Analysis"}},
						}}},
						UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
							PromptTokenCount:        100,
							CandidatesTokenCount:    50,
							CachedContentTokenCount: 10,
							ThoughtsTokenCount:      20,
						},
					}, nil
				},
			}, nil
		},
		GenerateContentFunc: func(
			_ context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig,
		) (*genai.GenerateContentResponse, error) {
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{Content: &genai.Content{
					Parts: []*genai.Part{{Text: `{"lgtm": true, "comments": "OK"}`}},
				}}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
					PromptTokenCount:     200,
					CandidatesTokenCount: 80,
				},
			}, nil
		},
	}

	r := &Reviewer{
		client:        client,
		modelName:     "gemini-3.1-pro-preview",
		temperature:   0.2,
		promptManager: prompts.New("", ""),
		logger:        testutil.NewTestLogger(),
	}

	result, err := r.ReviewDiff(t.Context(), "diff content", []string{"file.go"}, "/repo")
	require.NoError(t, err)
	require.NotNil(t, result.TokenUsage)
	assert.Equal(t, int32(300), result.TokenUsage.PromptTokens)
	assert.Equal(t, int32(130), result.TokenUsage.CandidatesTokens)
	assert.Equal(t, int32(10), result.TokenUsage.CachedTokens)
	assert.Equal(t, int32(20), result.TokenUsage.ThoughtsTokens)
	assert.Greater(t, result.CostUSD, float64(0))
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

	funcCall := &genai.FunctionCall{
		Name: "get_file_content",
		Args: map[string]any{"filepath": "escape/secret.txt"},
	}
	resp := reviewer.handleFileRetrieval(funcCall, repoDir)
	require.NotNil(t, resp.FunctionResponse)
	errMsg, ok := resp.FunctionResponse.Response["error"].(string)
	require.True(t, ok, "expected error response, got %+v", resp.FunctionResponse.Response)
	_, leaked := resp.FunctionResponse.Response["content"]
	assert.False(t, leaked, "must not return content for parent-symlink escape")
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

	funcCall := &genai.FunctionCall{
		Name: "get_file_content",
		Args: map[string]any{"filepath": "big.bin"},
	}
	resp := reviewer.handleFileRetrieval(funcCall, repoDir)
	require.NotNil(t, resp.FunctionResponse)
	errMsg, ok := resp.FunctionResponse.Response["error"].(string)
	require.True(t, ok, "expected error response, got %+v", resp.FunctionResponse.Response)
	assert.Contains(t, errMsg, "file too large")
	_, leaked := resp.FunctionResponse.Response["content"]
	assert.False(t, leaked, "must not return content when size limit is exceeded")
}
