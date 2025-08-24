package review

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/shields/lgtmcp/internal/config"
	"github.com/shields/lgtmcp/internal/logging"
	"github.com/shields/lgtmcp/internal/prompts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func newTestLogger() logging.Logger {
	logger, err := logging.New(logging.Config{
		Output: "none", // Disable logging in tests.
	})
	if err != nil {
		panic(err)
	}
	return logger
}

func TestHandleFileRetrieval_GitIgnore(t *testing.T) {
	t.Parallel()
	// Create a temporary repository directory.
	repoDir := t.TempDir()

	// Initialize it as a git repository.
	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	err := cmd.Run()
	require.NoError(t, err, "Failed to initialize git repo")

	// Create a .gitignore file.
	gitignorePath := filepath.Join(repoDir, ".gitignore")
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
	err = os.WriteFile(gitignorePath, []byte(gitignoreContent), 0o644)
	require.NoError(t, err)

	// Create various test files.
	// 1. Normal file (not ignored).
	allowedFile := filepath.Join(repoDir, "allowed.txt")
	err = os.WriteFile(allowedFile, []byte("allowed content"), 0o644)
	require.NoError(t, err)

	// 2. Directly ignored file.
	secretsFile := filepath.Join(repoDir, "secrets.txt")
	err = os.WriteFile(secretsFile, []byte("secret content"), 0o644)
	require.NoError(t, err)

	// 3. Pattern-matched ignored file.
	keyFile := filepath.Join(repoDir, "private.key")
	err = os.WriteFile(keyFile, []byte("private key content"), 0o644)
	require.NoError(t, err)

	// 4. .env file (ignored).
	envFile := filepath.Join(repoDir, ".env")
	err = os.WriteFile(envFile, []byte("API_KEY=secret123"), 0o644)
	require.NoError(t, err)

	// 5. .env.local file (pattern ignored).
	envLocalFile := filepath.Join(repoDir, ".env.local")
	err = os.WriteFile(envLocalFile, []byte("DB_PASSWORD=secret456"), 0o644)
	require.NoError(t, err)

	// 6. File in ignored directory.
	err = os.MkdirAll(filepath.Join(repoDir, "node_modules"), 0o755)
	require.NoError(t, err)
	nodeModuleFile := filepath.Join(repoDir, "node_modules", "package.json")
	err = os.WriteFile(nodeModuleFile, []byte(`{"name": "test"}`), 0o644)
	require.NoError(t, err)

	// 7. File in config directory with .secret extension (pattern match).
	err = os.MkdirAll(filepath.Join(repoDir, "config"), 0o755)
	require.NoError(t, err)
	configSecretFile := filepath.Join(repoDir, "config", "database.secret")
	err = os.WriteFile(configSecretFile, []byte("db_password=secret"), 0o644)
	require.NoError(t, err)

	// 8. Normal file in config directory (not ignored).
	configNormalFile := filepath.Join(repoDir, "config", "app.json")
	err = os.WriteFile(configNormalFile, []byte(`{"port": 3000}`), 0o644)
	require.NoError(t, err)

	// 9. Build directory file (ignored).
	err = os.MkdirAll(filepath.Join(repoDir, "build"), 0o755)
	require.NoError(t, err)
	buildFile := filepath.Join(repoDir, "build", "output.js")
	err = os.WriteFile(buildFile, []byte("compiled code"), 0o644)
	require.NoError(t, err)

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, newTestLogger())
	require.NoError(t, err)

	tests := []struct {
		name          string
		filepath      string
		expectContent string
		shouldSucceed bool
		expectedError string
	}{
		{
			name:          "normal file access",
			filepath:      "allowed.txt",
			shouldSucceed: true,
			expectContent: "allowed content",
		},
		{
			name:          "directly ignored file - secrets.txt",
			filepath:      "secrets.txt",
			shouldSucceed: false,
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          "pattern ignored file - .key extension",
			filepath:      "private.key",
			shouldSucceed: false,
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          ".env file (ignored)",
			filepath:      ".env",
			shouldSucceed: false,
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          ".env.local file (pattern ignored)",
			filepath:      ".env.local",
			shouldSucceed: false,
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          "file in ignored directory - node_modules",
			filepath:      "node_modules/package.json",
			shouldSucceed: false,
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          "pattern ignored in subdirectory - config/*.secret",
			filepath:      "config/database.secret",
			shouldSucceed: false,
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
			shouldSucceed: false,
			expectedError: "access denied: file is gitignored",
		},
		{
			name:          ".gitignore file itself",
			filepath:      ".gitignore",
			shouldSucceed: true, // .gitignore itself should be readable
			expectContent: gitignoreContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Create function call.
			funcCall := &genai.FunctionCall{
				Name: "get_file_content",
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
						t.Fatalf("Got error instead of content: %s", errMsg)
					}
					t.Fatalf("Response missing content: %+v", response)
				}
				assert.Equal(t, tt.expectContent, content)
			} else {
				// Should have an error.
				errMsg, hasError := response["error"].(string)
				assert.True(t, hasError, "Expected error in response for %s, got: %+v", tt.filepath, response)
				if tt.expectedError != "" {
					assert.Contains(t, errMsg, tt.expectedError,
						"Expected error message to contain '%s' for file %s, got: %s",
						tt.expectedError, tt.filepath, errMsg)
				}
			}
		})
	}
}

func TestHandleFileRetrieval_NestedGitIgnore(t *testing.T) {
	t.Parallel()
	// Create a temporary repository directory.
	repoDir := t.TempDir()

	// Initialize it as a git repository.
	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	err := cmd.Run()
	require.NoError(t, err, "Failed to initialize git repo")

	// Create root .gitignore file.
	gitignorePath := filepath.Join(repoDir, ".gitignore")
	err = os.WriteFile(gitignorePath, []byte("root-secret.txt\n"), 0o644)
	require.NoError(t, err)

	// Create a subdirectory with its own .gitignore.
	subdir := filepath.Join(repoDir, "subdir")
	err = os.MkdirAll(subdir, 0o755)
	require.NoError(t, err)

	subdirGitignore := filepath.Join(subdir, ".gitignore")
	err = os.WriteFile(subdirGitignore, []byte("local-secret.txt\n"), 0o644)
	require.NoError(t, err)

	// Create test files.
	// 1. Root-level ignored file.
	rootSecret := filepath.Join(repoDir, "root-secret.txt")
	err = os.WriteFile(rootSecret, []byte("root secret"), 0o644)
	require.NoError(t, err)

	// 2. Subdirectory ignored file (by nested .gitignore).
	subdirSecret := filepath.Join(subdir, "local-secret.txt")
	err = os.WriteFile(subdirSecret, []byte("subdir secret"), 0o644)
	require.NoError(t, err)

	// 3. Normal file in subdirectory.
	subdirNormal := filepath.Join(subdir, "normal.txt")
	err = os.WriteFile(subdirNormal, []byte("normal content"), 0o644)
	require.NoError(t, err)

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, newTestLogger())
	require.NoError(t, err)

	tests := []struct {
		name          string
		filepath      string
		shouldSucceed bool
		expectContent string
	}{
		{
			name:          "root-level ignored file",
			filepath:      "root-secret.txt",
			shouldSucceed: false,
		},
		{
			name:          "nested gitignore - file ignored by subdir .gitignore",
			filepath:      "subdir/local-secret.txt",
			shouldSucceed: false,
		},
		{
			name:          "normal file in subdirectory",
			filepath:      "subdir/normal.txt",
			shouldSucceed: true,
			expectContent: "normal content",
		},
	}

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
				assert.True(t, hasContent, "Expected content in response")
				assert.Equal(t, tt.expectContent, content)
			} else {
				errMsg, hasError := response["error"].(string)
				assert.True(t, hasError, "Expected error in response")
				assert.Contains(t, errMsg, "access denied: file is gitignored")
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
	err := os.WriteFile(testFile, []byte("test content"), 0o644)
	require.NoError(t, err)

	cfg := config.NewTestConfig()
	reviewer, err := New(cfg, newTestLogger())
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
	// Create a temporary repository directory.
	repoDir := t.TempDir()

	// Initialize it as a git repository for gitignore checks to work
	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	err := cmd.Run()
	require.NoError(t, err, "Failed to initialize git repo")

	// Create a file in the repo.
	repoFile := filepath.Join(repoDir, "allowed.txt")
	err = os.WriteFile(repoFile, []byte("allowed content"), 0o644)
	require.NoError(t, err)

	// Create a sensitive file outside the repo.
	sensitiveDir := t.TempDir()
	sensitiveFile := filepath.Join(sensitiveDir, "sensitive.txt")
	err = os.WriteFile(sensitiveFile, []byte("sensitive content"), 0o644)
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
	reviewer, err := New(cfg, newTestLogger())
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
		reviewer, err := New(cfg, newTestLogger())

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
		reviewer, err := New(cfg, newTestLogger())

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
		reviewer, err := New(cfg, newTestLogger())

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

		reviewer, err := New(cfg, newTestLogger())
		require.NoError(t, err)
		assert.NotNil(t, reviewer)
	})

	t.Run("uses default model when not specified", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestConfig()
		// Cfg already has default model set to gemini-2.5-pro.

		reviewer, err := New(cfg, newTestLogger())
		require.NoError(t, err)
		assert.NotNil(t, reviewer)
	})
}

func TestHandleFileRetrieval(t *testing.T) {
	t.Parallel()
	r := &Reviewer{logger: newTestLogger()}
	tmpDir := t.TempDir()

	// Initialize it as a git repository for gitignore checks to work
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	err := cmd.Run()
	require.NoError(t, err, "Failed to initialize git repo")

	// Create a test file.
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := "test file content"
	err = os.WriteFile(testFile, []byte(testContent), 0o644)
	require.NoError(t, err)

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
			modelName:     "gemini-2.5-pro",
			temperature:   0.2,
			promptManager: prompts.New("", ""),
			logger:        newTestLogger(),
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

		reviewer := &Reviewer{retryConfig: cfg, logger: newTestLogger()}
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

		reviewer := &Reviewer{retryConfig: cfg, logger: newTestLogger()}
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

		reviewer := &Reviewer{retryConfig: cfg, logger: newTestLogger()}
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

		reviewer := &Reviewer{retryConfig: cfg, logger: newTestLogger()}
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

		reviewer := &Reviewer{retryConfig: cfg, logger: newTestLogger()}
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

		reviewer := &Reviewer{retryConfig: nil, logger: newTestLogger()}
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
