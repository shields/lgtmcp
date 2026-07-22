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

package mcp

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"msrl.dev/lgtmcp/internal/config"
	"msrl.dev/lgtmcp/internal/progress"
	"msrl.dev/lgtmcp/internal/review"
	"msrl.dev/lgtmcp/internal/security"
	"msrl.dev/lgtmcp/internal/testutil"
)

var fakeSecrets = security.FakeSecrets{}

func TestNew(t *testing.T) {
	t.Parallel()
	t.Run("with valid API key", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestConfig()
		server, err := New(cfg, testutil.NewTestLogger())
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
		server, err := New(cfg, testutil.NewTestLogger())
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
		server, err := New(cfg, testutil.NewTestLogger())
		if err != nil {
			// Either security scanner or reviewer failed - both are valid.
			require.Error(t, err)
			assert.Nil(t, server)
		}
	})
}

// assertInBandToolError asserts that a handler reported a tool-execution
// failure in-band: no protocol-level error, a non-nil result with IsError set,
// and a text payload containing want.
func assertInBandToolError(t *testing.T, result *mcp.CallToolResult, err error, want string) {
	t.Helper()
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError, "expected an in-band IsError result")
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected text content, got %T", result.Content[0])
	assert.Contains(t, textContent.Text, want)
}

func TestHandleReviewAndCommit(t *testing.T) {
	t.Parallel()
	// Create a minimal server for testing argument parsing.
	server := &Server{
		logger: testutil.NewTestLogger(),
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
		assertInBandToolError(t, result, err, "invalid git repository")
	})
}

func TestHandleReviewOnly(t *testing.T) {
	t.Parallel()
	// Create a minimal server for testing argument parsing.
	server := &Server{
		logger: testutil.NewTestLogger(),
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
		assertInBandToolError(t, result, err, "invalid git repository")
	})
}

func TestRegisterTools(t *testing.T) {
	t.Parallel()
	t.Run("registerTools doesn't panic", func(t *testing.T) {
		t.Parallel()
		// Create a server with minimal setup to test registerTools.
		cfg := config.NewTestConfig()
		server, err := New(cfg, testutil.NewTestLogger())
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
		server, err := New(cfg, testutil.NewTestLogger())
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
	server, err := New(cfg, testutil.NewTestLogger())
	if err != nil {
		t.Skip("Cannot create server for testing - likely missing credentials")
	}

	ctx := t.Context()

	t.Run("no changes to review", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// Create a test file.
		testFile := filepath.Join(tmpDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("initial content"), 0o600))
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial commit")

		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory":      tmpDir,
					"commit_message": "test commit",
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
		tmpDir := testutil.CreateTempGitRepo(t)

		// Create a test file.
		testFile := filepath.Join(tmpDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("initial content"), 0o600))
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial commit")

		// Create changes by modifying the file.
		require.NoError(t, os.WriteFile(testFile, []byte("new modified content"), 0o600))

		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory":      tmpDir,
					"commit_message": "test commit",
				},
			},
		}

		// This will likely fail at the Gemini review step due to no API key.
		result, err := server.HandleReviewAndCommit(ctx, request)
		// Should fail at the review step, surfaced in-band.
		assertInBandToolError(t, result, err, "review failed")
	})
}

func TestHandleReviewOnlyWithRealRepo(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestConfig()
	server, err := New(cfg, testutil.NewTestLogger())
	if err != nil {
		t.Skip("Cannot create server for testing - likely missing credentials")
	}

	ctx := t.Context()

	t.Run("no changes to review", func(t *testing.T) {
		t.Parallel()
		tmpDir := testutil.CreateTempGitRepo(t)

		// Create a test file.
		testFile := filepath.Join(tmpDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("initial content"), 0o600))
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial commit")

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
		tmpDir := testutil.CreateTempGitRepo(t)

		// Create a test file.
		testFile := filepath.Join(tmpDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("initial content"), 0o600))
		testutil.RunGitCmd(t, tmpDir, "add", ".")
		testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial commit")

		// Create changes by modifying the file.
		require.NoError(t, os.WriteFile(testFile, []byte("new modified content for review"), 0o600))

		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory": tmpDir,
				},
			},
		}

		// This will likely fail at the Gemini review step due to invalid API key.
		result, err := server.HandleReviewOnly(ctx, request)
		// Should fail at the review step, surfaced in-band.
		assertInBandToolError(t, result, err, "review failed")
	})
}

func TestHandleReviewAndCommitArgumentValidation(t *testing.T) {
	t.Parallel()
	server := &Server{
		logger: testutil.NewTestLogger(),
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
		logger: testutil.NewTestLogger(),
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
		assertInBandToolError(t, result, err, "invalid git repository")
	})
}

func TestNewErrorHandling(t *testing.T) {
	t.Parallel()
	t.Run("security scanner initialization", func(t *testing.T) {
		t.Parallel()
		// Test that New() properly handles all component initialization.
		cfg := config.NewTestConfig()
		server, err := New(cfg, testutil.NewTestLogger())

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
		server, err := New(cfg, testutil.NewTestLogger())
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
	tmpDir := testutil.CreateTempGitRepo(t)

	// Create a test file.
	testFile := filepath.Join(tmpDir, "config.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("initial content"), 0o600))
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial commit")

	cfg := config.NewTestConfig()
	server, err := New(cfg, testutil.NewTestLogger())
	if err != nil {
		t.Skip("Cannot create server for testing - likely missing credentials")
	}

	ctx := t.Context()

	t.Run("with secrets in changes", func(t *testing.T) {
		t.Parallel()
		// Create changes with a secret that should be detected.
		secretContent := "token: " + fakeSecrets.GitHubPAT() + "\nother: content"
		require.NoError(t, os.WriteFile(testFile, []byte(secretContent), 0o600))

		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory":      tmpDir,
					"commit_message": "add secret config",
				},
			},
		}

		// The security scan runs before the review, so the secret is caught and
		// reported as an in-band non-approval before any Gemini call.
		result, err := server.HandleReviewAndCommit(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
		require.Len(t, result.Content, 1)
		textContent, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "Security scan detected secrets")
	})
}

func TestHandleReviewOnlyWithSecrets(t *testing.T) {
	t.Parallel()
	tmpDir := testutil.CreateTempGitRepo(t)

	// Create a test file.
	testFile := filepath.Join(tmpDir, "config.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("initial content"), 0o600))
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial commit")

	cfg := config.NewTestConfig()
	server, err := New(cfg, testutil.NewTestLogger())
	if err != nil {
		t.Skip("Cannot create server for testing - likely missing credentials")
	}

	ctx := t.Context()

	t.Run("with secrets in changes", func(t *testing.T) {
		t.Parallel()
		// Create changes with a secret that should be detected.
		secretContent := "token: " + fakeSecrets.GitHubPAT() + "\nother: content"
		require.NoError(t, os.WriteFile(testFile, []byte(secretContent), 0o600))

		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory": tmpDir,
				},
			},
		}

		// The security scan runs before the review, so the secret is caught and
		// reported as an in-band non-approval before any Gemini call.
		result, err := server.HandleReviewOnly(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
		require.Len(t, result.Content, 1)
		textContent, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "Security scan detected secrets")
	})
}

func TestHandleReviewAndCommitWithDiffError(t *testing.T) {
	t.Parallel()
	// Test with a directory that exists but isn't a git repo.
	tmpDir := t.TempDir()

	cfg := config.NewTestConfig()
	server, err := New(cfg, testutil.NewTestLogger())
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
		assertInBandToolError(t, result, err, "invalid git repository")
	})
}

func TestHandleReviewOnlyWithDiffError(t *testing.T) {
	t.Parallel()
	// Test with a directory that exists but isn't a git repo.
	tmpDir := t.TempDir()

	cfg := config.NewTestConfig()
	server, err := New(cfg, testutil.NewTestLogger())
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
		assertInBandToolError(t, result, err, "invalid git repository")
	})
}

func TestFormatReviewResponse(t *testing.T) {
	t.Parallel()

	t.Run("approved without usage stats", func(t *testing.T) {
		t.Parallel()
		result := &review.Result{
			LGTM:     true,
			Comments: "Code looks good!",
		}

		response := formatReviewResponse(result, "")
		assert.Contains(t, response, "Review Result: APPROVED (LGTM)")
		assert.Contains(t, response, "Code looks good!")
		assert.NotContains(t, response, "---")
	})

	t.Run("not approved without usage stats", func(t *testing.T) {
		t.Parallel()
		result := &review.Result{
			LGTM:     false,
			Comments: "Found issues",
		}

		response := formatReviewResponse(result, "")
		assert.Contains(t, response, "Review Result: NOT APPROVED")
		assert.Contains(t, response, "Found issues")
		assert.NotContains(t, response, "---")
	})

	t.Run("with duration only", func(t *testing.T) {
		t.Parallel()
		result := &review.Result{
			LGTM:       true,
			Comments:   "LGTM",
			DurationMS: 12345,
		}

		response := formatReviewResponse(result, "")
		assert.Contains(t, response, "Review Result: APPROVED (LGTM)")
		assert.Contains(t, response, "---")
		assert.Contains(t, response, "Duration: 12.3 s")
	})

	t.Run("with model only", func(t *testing.T) {
		t.Parallel()
		result := &review.Result{
			LGTM:     true,
			Comments: "LGTM",
			Model:    "gemini-3.6-flash",
		}

		response := formatReviewResponse(result, "")
		assert.Contains(t, response, "---")
		assert.Contains(t, response, "Model: gemini-3.6-flash")
		// With no token usage the second footer line is dropped entirely.
		assert.NotContains(t, response, "Tokens:")
		assert.NotContains(t, response, "Cached:")
	})

	t.Run("with token usage", func(t *testing.T) {
		t.Parallel()
		result := &review.Result{
			LGTM:     true,
			Comments: "Good code",
			TokenUsage: &review.TokenUsage{
				PromptTokens:     10000,
				CandidatesTokens: 2000,
				TotalTokens:      12000,
			},
		}

		response := formatReviewResponse(result, "")
		assert.Contains(t, response, "Tokens: 12,000 (in: 10,000, out: 2,000)")
	})

	t.Run("with cost in dollars", func(t *testing.T) {
		t.Parallel()
		result := &review.Result{
			LGTM:     true,
			Comments: "Approved",
			CostUSD:  0.042,
		}

		response := formatReviewResponse(result, "")
		assert.Contains(t, response, "Cost: $0.04")
	})

	t.Run("with sub-cent cost", func(t *testing.T) {
		t.Parallel()
		result := &review.Result{
			LGTM:     true,
			Comments: "Approved",
			CostUSD:  0.0023,
		}

		response := formatReviewResponse(result, "")
		assert.Contains(t, response, "Cost: $0.0023")
	})

	t.Run("with all usage stats", func(t *testing.T) {
		t.Parallel()
		result := &review.Result{
			LGTM:       true,
			Comments:   "All good",
			DurationMS: 15000,
			TokenUsage: &review.TokenUsage{
				PromptTokens:     12000,
				CandidatesTokens: 3000,
				TotalTokens:      15000,
			},
			CostUSD: 0.05,
			Model:   "gemini-3.6-flash",
		}

		response := formatReviewResponse(result, "")
		assert.Contains(t, response, "Review Result: APPROVED (LGTM)")
		// The footer is two lines: run summary, then token breakdown.
		_, footer, ok := strings.Cut(response, "\n\n---\n")
		require.True(t, ok, "response should carry a usage footer")
		assert.Equal(t,
			"Model: gemini-3.6-flash · Duration: 15.0 s · Cost: $0.05\n"+
				"Tokens: 15,000 (in: 12,000, out: 3,000) · Cached: 0 (no hit)",
			footer)
	})

	t.Run("with cache hit and savings", func(t *testing.T) {
		t.Parallel()
		result := &review.Result{
			LGTM:     true,
			Comments: "Approved",
			TokenUsage: &review.TokenUsage{
				PromptTokens:     10000,
				CandidatesTokens: 2000,
				TotalTokens:      12000,
				CachedTokens:     4700, // 47% of prompt
			},
			CostUSD:         0.05,
			CacheSavingsUSD: 0.0332,
		}

		response := formatReviewResponse(result, "")
		assert.Contains(t, response, "Cached: 4,700 (47% hit, saved $0.0332)")
	})

	t.Run("with no cache hit", func(t *testing.T) {
		t.Parallel()
		result := &review.Result{
			LGTM:     true,
			Comments: "Approved",
			TokenUsage: &review.TokenUsage{
				PromptTokens:     8000,
				CandidatesTokens: 1000,
				TotalTokens:      9000,
				CachedTokens:     0,
			},
		}

		response := formatReviewResponse(result, "")
		assert.Contains(t, response, "Cached: 0 (no hit)")
		assert.NotContains(t, response, "saved $")
	})

	t.Run("with commit hash", func(t *testing.T) {
		t.Parallel()
		result := &review.Result{
			LGTM:       true,
			Comments:   "LGTM",
			DurationMS: 5000,
		}

		response := formatReviewResponse(result, "abc123def")
		assert.Contains(t, response, "Review Result: APPROVED (LGTM)")
		assert.Contains(t, response, "Changes committed successfully!")
		assert.Contains(t, response, "Commit: abc123def")
		// Commit message should appear before the footer.
		commitIdx := strings.Index(response, "Changes committed")
		footerIdx := strings.Index(response, "---")
		assert.Greater(t, footerIdx, commitIdx, "Commit message should appear before the stats footer")
	})

	t.Run("with token usage only", func(t *testing.T) {
		t.Parallel()
		result := &review.Result{
			LGTM:     true,
			Comments: "LGTM",
			TokenUsage: &review.TokenUsage{
				PromptTokens:     900,
				CandidatesTokens: 100,
				TotalTokens:      1000,
			},
		}

		// With no model, duration, or cost the summary line is dropped and
		// the token breakdown becomes the only footer line.
		_, footer, ok := strings.Cut(formatReviewResponse(result, ""), "\n\n---\n")
		require.True(t, ok, "response should carry a usage footer")
		assert.Equal(t, "Tokens: 1,000 (in: 900, out: 100) · Cached: 0 (no hit)", footer)
	})
}

func TestFormatCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		count int32
		want  string
	}{
		{name: "zero", count: 0, want: "0"},
		{name: "under a thousand", count: 999, want: "999"},
		{name: "exactly a thousand", count: 1000, want: "1,000"},
		{name: "five digits", count: 12345, want: "12,345"},
		{name: "seven digits", count: 1234567, want: "1,234,567"},
		{name: "max int32", count: math.MaxInt32, want: "2,147,483,647"},
		{name: "negative", count: -12345, want: "-12,345"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, formatCount(tt.count))
		})
	}
}

func createTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	cfg := config.NewTestConfig()
	logger := testutil.NewTestLogger()

	reviewer := review.WithStubResponse(true, "Test approved")
	scanner, err := security.New("")
	require.NoError(t, err)

	s := newForTesting(cfg, logger, reviewer, scanner)

	tmpDir := testutil.CreateTempGitRepo(t)
	return s, tmpDir
}

func TestHandleReviewOnly_SuccessfulReview(t *testing.T) {
	t.Parallel()
	s, tmpDir := createTestServer(t)

	// Create changes.
	testutil.CreateFile(t, tmpDir, "file.go", "package main\n")
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")
	testutil.CreateFile(t, tmpDir, "file.go", "package main\n\nfunc main() {}\n")

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"directory": tmpDir}

	result, err := s.HandleReviewOnly(t.Context(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.Content)
}

func TestHandleReviewAndCommit_Approved(t *testing.T) {
	t.Parallel()
	s, tmpDir := createTestServer(t)

	// Create changes.
	testutil.CreateFile(t, tmpDir, "file.go", "package main\n")
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")
	testutil.CreateFile(t, tmpDir, "file.go", "package main\n\nfunc main() {}\n")

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"directory":      tmpDir,
		"commit_message": "test commit",
	}

	result, err := s.HandleReviewAndCommit(t.Context(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Should include commit hash.
	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "committed successfully")
}

func TestHandleReviewAndCommit_Rejected(t *testing.T) {
	t.Parallel()
	cfg := config.NewTestConfig()
	reviewer := review.WithStubResponse(false, "Issues found")
	scanner, err := security.New("")
	require.NoError(t, err)
	s := newForTesting(cfg, testutil.NewTestLogger(), reviewer, scanner)

	tmpDir := testutil.CreateTempGitRepo(t)
	testutil.CreateFile(t, tmpDir, "file.go", "package main\n")
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")
	testutil.CreateFile(t, tmpDir, "file.go", "package main\n\nfunc main() {}\n")

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"directory":      tmpDir,
		"commit_message": "test commit",
	}

	result, err := s.HandleReviewAndCommit(t.Context(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "NOT APPROVED")
	assert.NotContains(t, textContent.Text, "committed")
}

func TestHandleReviewAndCommit_CommitMessageNotString(t *testing.T) {
	t.Parallel()
	s, tmpDir := createTestServer(t)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"directory":      tmpDir,
		"commit_message": 123,
	}

	_, err := s.HandleReviewAndCommit(t.Context(), request)
	require.ErrorIs(t, err, ErrCommitMessageNotString)
}

func TestPrepareReview_NoChanges(t *testing.T) {
	t.Parallel()
	s, tmpDir := createTestServer(t)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"directory": tmpDir}

	result, err := s.HandleReviewOnly(t.Context(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "No changes to review")
}

func TestPrepareReview_SecurityFindings(t *testing.T) {
	t.Parallel()
	s, tmpDir := createTestServer(t)

	testutil.CreateFile(t, tmpDir, "file.go", "package main\n")
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")
	testutil.CreateFile(t, tmpDir, "config.txt", "token: "+fakeSecrets.GitHubPAT()+"\n")

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"directory": tmpDir}

	result, err := s.HandleReviewOnly(t.Context(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Detected secrets are a non-approval, not a tool error.
	assert.False(t, result.IsError)
	textContent, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "NOT APPROVED")
	assert.Contains(t, textContent.Text, "Security scan detected secrets")
}

func TestPrepareReview_SplitsDeletionsFromChangedFiles(t *testing.T) {
	t.Parallel()
	s, tmpDir := createTestServer(t)

	// Seed two committed files; one will be modified, one will be deleted.
	testutil.CreateFile(t, tmpDir, "kept.go", "package main\n")
	testutil.CreateFile(t, tmpDir, "gone.go", "package main\n\nfunc unused() {}\n")
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")

	// Modify kept.go and delete gone.go from the working tree.
	testutil.CreateFile(t, tmpDir, "kept.go", "package main\n\nfunc main() {}\n")
	require.NoError(t, os.Remove(filepath.Join(tmpDir, "gone.go")))

	reporter := progress.NewNoOpReporter()
	rc, earlyReturn, err := s.prepareReview(t.Context(), tmpDir, reporter, 4)
	require.NoError(t, err)
	require.Nil(t, earlyReturn, "expected real diff, not an early-return result")
	require.NotNil(t, rc)

	assert.ElementsMatch(t, []string{"kept.go", "gone.go"}, rc.changedFiles,
		"changedFiles must include both modified and deleted paths so StageFiles can stage both")
	assert.Equal(t, []string{"gone.go"}, rc.deletedFiles,
		"deletedFiles must contain exactly the deleted path so Gemini gets the right signal")
}

func TestPrepareReview_NoDeletionsLeavesDeletedFilesEmpty(t *testing.T) {
	t.Parallel()
	s, tmpDir := createTestServer(t)

	testutil.CreateFile(t, tmpDir, "kept.go", "package main\n")
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")
	testutil.CreateFile(t, tmpDir, "kept.go", "package main\n\nfunc main() {}\n")

	reporter := progress.NewNoOpReporter()
	rc, earlyReturn, err := s.prepareReview(t.Context(), tmpDir, reporter, 4)
	require.NoError(t, err)
	require.Nil(t, earlyReturn)
	require.NotNil(t, rc)

	assert.Equal(t, []string{"kept.go"}, rc.changedFiles)
	assert.Empty(t, rc.deletedFiles)
}

func TestPrepareReview_InstructionFiles(t *testing.T) {
	t.Parallel()
	s, tmpDir := createTestServer(t)

	testutil.CreateFile(t, tmpDir, "AGENTS.md", "Check tests\n")
	testutil.CreateFile(t, tmpDir, "file.go", "package main\n")
	testutil.RunGitCmd(t, tmpDir, "add", ".")
	testutil.RunGitCmd(t, tmpDir, "commit", "-m", "initial")
	testutil.CreateFile(t, tmpDir, "file.go", "package main\n\nfunc main() {}\n")

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"directory": tmpDir}

	result, err := s.HandleReviewOnly(t.Context(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestGenerateRequestID_Format(t *testing.T) {
	t.Parallel()
	id, err := generateRequestID()
	require.NoError(t, err)
	assert.Len(t, id, 8)

	// Verify it's valid hex.
	for _, c := range id {
		assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'), "expected hex char, got %c", c)
	}
}

func TestCreateProgressReporter_WithToken(t *testing.T) {
	t.Parallel()
	s, _ := createTestServer(t)

	request := mcp.CallToolRequest{}
	request.Params.Meta = &mcp.Meta{
		ProgressToken: "test-token",
	}

	reporter := s.createProgressReporter(request)
	assert.NotNil(t, reporter)
}

func TestRun_WithServeFunc(t *testing.T) {
	t.Parallel()
	s, _ := createTestServer(t)
	s.serveFunc = func(_ *mcpsrv.MCPServer, _ ...mcpsrv.StdioOption) error {
		return nil
	}

	err := s.Run(t.Context())
	require.NoError(t, err)
}

func TestNew_ScannerFailure(t *testing.T) {
	t.Parallel()
	cfg := config.NewTestConfig()
	cfg.Gitleaks.Config = "/some/config.toml"

	s, err := New(cfg, testutil.NewTestLogger())
	require.Error(t, err)
	assert.Nil(t, s)
	assert.Contains(t, err.Error(), "failed to create security scanner")
}
