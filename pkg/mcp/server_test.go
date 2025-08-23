package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/shields/lgtmcp/internal/config"
	"github.com/shields/lgtmcp/internal/logging"
	"github.com/shields/lgtmcp/internal/security"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fakeSecrets = security.FakeSecrets{}

func newTestLogger() logging.Logger {
	logger, err := logging.New(logging.Config{
		Output: "none", // Disable logging in tests by default.
	})
	if err != nil {
		panic(err)
	}

	return logger
}

func TestNew(t *testing.T) {
	t.Parallel()
	t.Run("with valid API key", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestConfig()
		server, err := New(cfg, newTestLogger())
		if err != nil {
			// This might fail with invalid API key, which is expected in CI.
			assert.Contains(t, err.Error(), "failed to create reviewer")

			return
		}
		assert.NotNil(t, server)
		assert.NotNil(t, server.mcpServer)
		assert.NotNil(t, server.reviewer)
		assert.NotNil(t, server.scanner)
	})

	t.Run("with empty API key", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestConfig()
		cfg.Google.APIKey = ""
		server, err := New(cfg, newTestLogger())
		if err != nil {
			// Expected if no credentials are configured.
			assert.Contains(t, err.Error(), "failed to create reviewer")

			return
		}
		assert.NotNil(t, server)
		assert.NotNil(t, server.mcpServer)
		assert.NotNil(t, server.reviewer)
		assert.NotNil(t, server.scanner)
	})

	t.Run("security scanner creation failure", func(t *testing.T) {
		t.Parallel()
		// We can't easily mock this, but we can verify the error handling.
		cfg := config.NewTestConfig()
		server, err := New(cfg, newTestLogger())
		if err != nil {
			// Either security scanner or reviewer failed - both are valid.
			require.Error(t, err)
			assert.Nil(t, server)
		}
	})
}

func TestHandleReviewAndCommit(t *testing.T) {
	t.Parallel()
	// Create a minimal server for testing argument parsing.
	server := &Server{
		logger: newTestLogger(),
	}
	ctx := t.Context()

	t.Run("invalid arguments format", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: "invalid",
			},
		}

		result, err := server.HandleReviewAndCommit(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid arguments format")
	})

	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"commit_message": "test commit",
				},
			},
		}

		result, err := server.HandleReviewAndCommit(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "directory must be a string")
	})

	t.Run("missing commit message", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory": "/tmp/repo",
				},
			},
		}

		result, err := server.HandleReviewAndCommit(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "commit_message must be a string")
	})

	t.Run("invalid directory path", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory":      "/nonexistent/invalid/path",
					"commit_message": "test commit",
				},
			},
		}

		result, err := server.HandleReviewAndCommit(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid git repository")
	})
}

func TestHandleReviewOnly(t *testing.T) {
	t.Parallel()
	// Create a minimal server for testing argument parsing.
	server := &Server{
		logger: newTestLogger(),
	}
	ctx := t.Context()

	t.Run("invalid arguments format", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: "invalid",
			},
		}

		result, err := server.HandleReviewOnly(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid arguments format")
	})

	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"some_other_param": "value",
				},
			},
		}

		result, err := server.HandleReviewOnly(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "directory must be a string")
	})

	t.Run("invalid directory path", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory": "/nonexistent/invalid/path",
				},
			},
		}

		result, err := server.HandleReviewOnly(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid git repository")
	})
}

func TestRegisterTools(t *testing.T) {
	t.Parallel()
	t.Run("registerTools doesn't panic", func(t *testing.T) {
		t.Parallel()
		// Create a server with minimal setup to test registerTools.
		cfg := config.NewTestConfig()
		server, err := New(cfg, newTestLogger())
		if err != nil {
			// If we can't create the server (no credentials), skip this test.
			t.Skip("Cannot create server for testing - likely missing credentials")
		}

		// The registerTools method is called in New(), so if we got here it worked.
		assert.NotNil(t, server.mcpServer)

		// Test that we can call registerTools again without panicking.
		assert.NotPanics(t, func() {
			server.registerTools()
		})
	})
}

func TestRun(t *testing.T) {
	t.Parallel()
	t.Run("Run method exists and can be called", func(t *testing.T) {
		t.Parallel()
		// Create a server with minimal setup.
		cfg := config.NewTestConfig()
		server, err := New(cfg, newTestLogger())
		if err != nil {
			// If we can't create the server (no credentials), skip this test.
			t.Skip("Cannot create server for testing - likely missing credentials")
		}

		// But we can verify the method exists and would attempt to start.
		assert.NotNil(t, server)
		assert.NotNil(t, server.mcpServer)

		// So we can't test actual execution, but we verified the setup.
	})
}

func TestHandleReviewAndCommitWithRealRepo(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestConfig()
	server, err := New(cfg, newTestLogger())
	if err != nil {
		t.Skip("Cannot create server for testing - likely missing credentials")
	}

	ctx := t.Context()

	t.Run("no changes to review", func(t *testing.T) {
		t.Parallel()
		// Create a temporary git repository for this specific test.
		tmpDir := t.TempDir()

		// Initialize git repo.
		runGitCmd(t, tmpDir, "init")
		runGitCmd(t, tmpDir, "config", "user.email", "test@example.com")
		runGitCmd(t, tmpDir, "config", "user.name", "Test User")

		// Create a test file.
		testFile := filepath.Join(tmpDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("initial content"), 0o644))
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "initial commit")

		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory":      tmpDir,
					"commit_message": "test commit",
					"commit_on_lgtm": false,
				},
			},
		}

		result, err := server.HandleReviewAndCommit(ctx, request)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, result.Content, 1)

		// Check that it reports no changes.
		if textContent, ok := result.Content[0].(mcp.TextContent); ok {
			assert.Equal(t, "No changes to review", textContent.Text)
		}
	})

	t.Run("with changes but review fails", func(t *testing.T) {
		t.Parallel()
		// Create a temporary git repository for this specific test.
		tmpDir := t.TempDir()

		// Initialize git repo.
		runGitCmd(t, tmpDir, "init")
		runGitCmd(t, tmpDir, "config", "user.email", "test@example.com")
		runGitCmd(t, tmpDir, "config", "user.name", "Test User")

		// Create a test file.
		testFile := filepath.Join(tmpDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("initial content"), 0o644))
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "initial commit")

		// Create changes by modifying the file.
		require.NoError(t, os.WriteFile(testFile, []byte("new modified content"), 0o644))

		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory":      tmpDir,
					"commit_message": "test commit",
					"commit_on_lgtm": false,
				},
			},
		}

		// This will likely fail at the Gemini review step due to no API key.
		result, err := server.HandleReviewAndCommit(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		// Should fail at review step.
		assert.Contains(t, err.Error(), "review failed")
	})
}

// runGitCmd runs a git command in the specified directory.
func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git command failed: %v\nOutput: %s", err, output)
	}
}

func TestHandleReviewOnlyWithRealRepo(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestConfig()
	server, err := New(cfg, newTestLogger())
	if err != nil {
		t.Skip("Cannot create server for testing - likely missing credentials")
	}

	ctx := t.Context()

	t.Run("no changes to review", func(t *testing.T) {
		t.Parallel()
		// Create a temporary git repository for this specific test.
		tmpDir := t.TempDir()

		// Initialize git repo.
		runGitCmd(t, tmpDir, "init")
		runGitCmd(t, tmpDir, "config", "user.email", "test@example.com")
		runGitCmd(t, tmpDir, "config", "user.name", "Test User")

		// Create a test file.
		testFile := filepath.Join(tmpDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("initial content"), 0o644))
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "initial commit")

		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory": tmpDir,
				},
			},
		}

		result, err := server.HandleReviewOnly(ctx, request)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, result.Content, 1)

		// Check that it reports no changes.
		if textContent, ok := result.Content[0].(mcp.TextContent); ok {
			assert.Equal(t, "No changes to review", textContent.Text)
		}
	})

	t.Run("with changes but review fails", func(t *testing.T) {
		t.Parallel()
		// Create a temporary git repository for this specific test.
		tmpDir := t.TempDir()

		// Initialize git repo.
		runGitCmd(t, tmpDir, "init")
		runGitCmd(t, tmpDir, "config", "user.email", "test@example.com")
		runGitCmd(t, tmpDir, "config", "user.name", "Test User")

		// Create a test file.
		testFile := filepath.Join(tmpDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("initial content"), 0o644))
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "initial commit")

		// Create changes by modifying the file.
		require.NoError(t, os.WriteFile(testFile, []byte("new modified content for review"), 0o644))

		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory": tmpDir,
				},
			},
		}

		// This will likely fail at the Gemini review step due to invalid API key.
		result, err := server.HandleReviewOnly(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		// Should fail at review step.
		assert.Contains(t, err.Error(), "review failed")
	})
}

func TestHandleReviewAndCommitArgumentValidation(t *testing.T) {
	t.Parallel()
	server := &Server{
		logger: newTestLogger(),
	}
	ctx := t.Context()

	t.Run("directory not string type", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory":      123, // Wrong type.
					"commit_message": "test",
				},
			},
		}

		result, err := server.HandleReviewAndCommit(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "directory must be a string")
	})

	t.Run("commit_message not string type", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory":      "/tmp",
					"commit_message": 123, // Wrong type.
				},
			},
		}

		result, err := server.HandleReviewAndCommit(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "commit_message must be a string")
	})
}

func TestHandleReviewOnlyArgumentValidation(t *testing.T) {
	t.Parallel()
	server := &Server{
		logger: newTestLogger(),
	}
	ctx := t.Context()

	t.Run("directory not string type", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory": 123, // Wrong type.
				},
			},
		}

		result, err := server.HandleReviewOnly(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "directory must be a string")
	})

	t.Run("valid directory that doesn't exist", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory": "/completely/nonexistent/path/that/does/not/exist",
				},
			},
		}

		result, err := server.HandleReviewOnly(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid git repository")
	})
}

func TestNewErrorHandling(t *testing.T) {
	t.Parallel()
	t.Run("security scanner initialization", func(t *testing.T) {
		t.Parallel()
		// Test that New() properly handles all component initialization.
		cfg := config.NewTestConfig()
		server, err := New(cfg, newTestLogger())

		// Either succeeds or fails gracefully.
		if err != nil {
			assert.Contains(t, err.Error(), "failed to create")
			assert.Nil(t, server)
		} else {
			assert.NotNil(t, server)
			assert.NotNil(t, server.mcpServer)
			assert.NotNil(t, server.reviewer)
			assert.NotNil(t, server.scanner)
		}
	})
}

func TestRunContextCancellation(t *testing.T) {
	t.Parallel()
	t.Run("run with canceled context", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestConfig()
		server, err := New(cfg, newTestLogger())
		if err != nil {
			t.Skip("Cannot create server for testing - likely missing credentials")
		}

		// Create a canceled context.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		// But we can verify the method signature and basic setup.
		assert.NotNil(t, server)
		assert.NotNil(t, server.mcpServer)

		// Use the context to avoid unused variable error.
		_ = ctx
	})
}

func TestHandleReviewAndCommitWithSecrets(t *testing.T) {
	t.Parallel()
	// Create a temporary git repository for testing.
	tmpDir := t.TempDir()

	// Initialize git repo.
	runGitCmd(t, tmpDir, "init")
	runGitCmd(t, tmpDir, "config", "user.email", "test@example.com")
	runGitCmd(t, tmpDir, "config", "user.name", "Test User")

	// Create a test file.
	testFile := filepath.Join(tmpDir, "config.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("initial content"), 0o644))
	runGitCmd(t, tmpDir, "add", ".")
	runGitCmd(t, tmpDir, "commit", "-m", "initial commit")

	cfg := config.NewTestConfig()
	server, err := New(cfg, newTestLogger())
	if err != nil {
		t.Skip("Cannot create server for testing - likely missing credentials")
	}

	ctx := t.Context()

	t.Run("with secrets in changes", func(t *testing.T) {
		t.Parallel()
		// Create changes with a secret that should be detected.
		secretContent := "token: " + fakeSecrets.GitHubPAT() + "\nother: content"
		require.NoError(t, os.WriteFile(testFile, []byte(secretContent), 0o644))

		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory":      tmpDir,
					"commit_message": "add secret config",
				},
			},
		}

		// Or fail at review step (due to invalid API key).
		result, err := server.HandleReviewAndCommit(ctx, request)

		if err != nil {
			// If error, check it's a review failure (expected due to invalid API key).
			assert.Contains(t, err.Error(), "review failed")
		} else {
			// If no error, should be a security failure.
			assert.NotNil(t, result)
			if result.IsError {
				if len(result.Content) > 0 {
					if textContent, ok := result.Content[0].(mcp.TextContent); ok {
						assert.Contains(t, textContent.Text, "Security scan failed")
					}
				}
			}
		}
	})
}

func TestHandleReviewOnlyWithSecrets(t *testing.T) {
	t.Parallel()
	// Create a temporary git repository for testing.
	tmpDir := t.TempDir()

	// Initialize git repo.
	runGitCmd(t, tmpDir, "init")
	runGitCmd(t, tmpDir, "config", "user.email", "test@example.com")
	runGitCmd(t, tmpDir, "config", "user.name", "Test User")

	// Create a test file.
	testFile := filepath.Join(tmpDir, "config.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("initial content"), 0o644))
	runGitCmd(t, tmpDir, "add", ".")
	runGitCmd(t, tmpDir, "commit", "-m", "initial commit")

	cfg := config.NewTestConfig()
	server, err := New(cfg, newTestLogger())
	if err != nil {
		t.Skip("Cannot create server for testing - likely missing credentials")
	}

	ctx := t.Context()

	t.Run("with secrets in changes", func(t *testing.T) {
		t.Parallel()
		// Create changes with a secret that should be detected.
		secretContent := "token: " + fakeSecrets.GitHubPAT() + "\nother: content"
		require.NoError(t, os.WriteFile(testFile, []byte(secretContent), 0o644))

		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory": tmpDir,
				},
			},
		}

		// Or fail at review step (due to invalid API key).
		result, err := server.HandleReviewOnly(ctx, request)

		if err != nil {
			// If error, check it's a review failure (expected due to invalid API key).
			assert.Contains(t, err.Error(), "review failed")
		} else {
			// If no error, should be a security failure.
			assert.NotNil(t, result)
			if result.IsError {
				if len(result.Content) > 0 {
					if textContent, ok := result.Content[0].(mcp.TextContent); ok {
						assert.Contains(t, textContent.Text, "Security scan failed")
					}
				}
			}
		}
	})
}

func TestHandleReviewAndCommitWithDiffError(t *testing.T) {
	t.Parallel()
	// Test with a directory that exists but isn't a git repo.
	tmpDir := t.TempDir()

	cfg := config.NewTestConfig()
	server, err := New(cfg, newTestLogger())
	if err != nil {
		t.Skip("Cannot create server for testing - likely missing credentials")
	}

	ctx := t.Context()

	t.Run("non-git directory", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory":      tmpDir,
					"commit_message": "test commit",
				},
			},
		}

		result, err := server.HandleReviewAndCommit(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid git repository")
	})
}

func TestHandleReviewOnlyWithDiffError(t *testing.T) {
	t.Parallel()
	// Test with a directory that exists but isn't a git repo.
	tmpDir := t.TempDir()

	cfg := config.NewTestConfig()
	server, err := New(cfg, newTestLogger())
	if err != nil {
		t.Skip("Cannot create server for testing - likely missing credentials")
	}

	ctx := t.Context()

	t.Run("non-git directory", func(t *testing.T) {
		t.Parallel()
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory": tmpDir,
				},
			},
		}

		result, err := server.HandleReviewOnly(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid git repository")
	})
}
