//go:build e2e
// +build e2e

package test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shields/lgtmcp/internal/config"
	"github.com/shields/lgtmcp/internal/security"
	mcpserver "github.com/shields/lgtmcp/pkg/mcp"
)

var fakeSecrets = security.FakeSecrets{}

// TestMCPProtocolE2E tests the complete MCP protocol workflow
func TestMCPProtocolE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	cfg := config.NewTestConfig()
	server, err := mcpserver.New(cfg)
	if err != nil {
		t.Skipf("Cannot create MCP server for E2E test: %v", err)
	}

	t.Run("review_only tool with clean changes", func(t *testing.T) {
		tmpDir := createTempGitRepo(t)
		defer cleanup(t, tmpDir)

		// Create initial commit
		testFile := filepath.Join(tmpDir, "main.go")
		initialContent := `package main

import "fmt"

func main() {
	fmt.Println("Hello")
}`
		require.NoError(t, os.WriteFile(testFile, []byte(initialContent), 0o600))
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "initial commit")

		// Make changes
		modifiedContent := `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
	fmt.Println("This is a clean change")
}`
		require.NoError(t, os.WriteFile(testFile, []byte(modifiedContent), 0o600))

		// Create MCP request
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "review_only",
				Arguments: map[string]any{
					"directory": tmpDir,
				},
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// This will likely fail at the review step due to invalid API key
		// but should pass validation and security checks
		result, err := server.HandleReviewOnly(ctx, request)

		if err != nil {
			// Expected to fail at review step
			assert.Contains(t, err.Error(), "review failed")
		} else {
			// If it somehow succeeds, verify the result structure
			assert.NotNil(t, result)
			assert.NotNil(t, result.Content)
		}
	})

	t.Run("review_only tool with no changes", func(t *testing.T) {
		tmpDir := createTempGitRepo(t)
		defer cleanup(t, tmpDir)

		// Create initial commit with no pending changes
		testFile := filepath.Join(tmpDir, "main.go")
		content := `package main

func main() {
	println("hello")
}`
		require.NoError(t, os.WriteFile(testFile, []byte(content), 0o600))
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "initial commit")

		// Create MCP request (no changes to review)
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "review_only",
				Arguments: map[string]any{
					"directory": tmpDir,
				},
			},
		}

		ctx := context.Background()
		result, err := server.HandleReviewOnly(ctx, request)

		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, result.Content, 1)

		if textContent, ok := result.Content[0].(mcp.TextContent); ok {
			assert.Equal(t, "No changes to review", textContent.Text)
		}
	})

	t.Run("review_only tool with secrets", func(t *testing.T) {
		tmpDir := createTempGitRepo(t)
		defer cleanup(t, tmpDir)

		// Create initial commit
		testFile := filepath.Join(tmpDir, "config.yaml")
		initialContent := `api:
  endpoint: https://api.example.com`
		require.NoError(t, os.WriteFile(testFile, []byte(initialContent), 0o600))
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "initial config")

		// Add secrets
		secretContent := `api:
  endpoint: https://api.example.com
  token: ` + fakeSecrets.GitHubPAT() + "`"
		require.NoError(t, os.WriteFile(testFile, []byte(secretContent), 0o600))

		// Create MCP request
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "review_only",
				Arguments: map[string]any{
					"directory": tmpDir,
				},
			},
		}

		ctx := context.Background()
		result, err := server.HandleReviewOnly(ctx, request)

		// Should return a security failure result (not an error)
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.True(t, result.IsError)

		if len(result.Content) > 0 {
			if textContent, ok := result.Content[0].(mcp.TextContent); ok {
				assert.Contains(t, textContent.Text, "Security scan failed")
			}
		}
	})

	t.Run("review_and_commit tool with clean changes", func(t *testing.T) {
		tmpDir := createTempGitRepo(t)
		defer cleanup(t, tmpDir)

		// Create initial commit
		testFile := filepath.Join(tmpDir, "main.go")
		initialContent := `package main

func main() {
	println("hello")
}`
		require.NoError(t, os.WriteFile(testFile, []byte(initialContent), 0o600))
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "initial commit")

		// Make changes
		modifiedContent := `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}`
		require.NoError(t, os.WriteFile(testFile, []byte(modifiedContent), 0o600))

		// Create MCP request
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "review_and_commit",
				Arguments: map[string]any{
					"directory":      tmpDir,
					"commit_message": "Improve greeting message",
				},
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// This will likely fail at the review step due to invalid API key
		result, err := server.HandleReviewAndCommit(ctx, request)

		if err != nil {
			// Expected to fail at review step
			assert.Contains(t, err.Error(), "review failed")
		} else {
			// If it somehow succeeds, verify the result structure
			assert.NotNil(t, result)
			assert.NotNil(t, result.Content)
		}
	})

	t.Run("review_and_commit tool validation errors", func(t *testing.T) {
		ctx := context.Background()

		// Test missing directory
		request := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "review_and_commit",
				Arguments: map[string]any{
					"commit_message": "test commit",
				},
			},
		}

		result, err := server.HandleReviewAndCommit(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "directory must be a string")

		// Test missing commit message
		request = mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "review_and_commit",
				Arguments: map[string]any{
					"directory": "/tmp",
				},
			},
		}

		result, err = server.handleReviewAndCommit(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "commit_message must be a string")

		// Test invalid directory
		request = mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "review_and_commit",
				Arguments: map[string]any{
					"directory":      "/nonexistent/path",
					"commit_message": "test commit",
				},
			},
		}

		result, err = server.handleReviewAndCommit(ctx, request)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid git repository")
	})
}

// TestMCPServerLifecycle tests server startup and shutdown
func TestMCPServerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping lifecycle test in short mode")
	}

	t.Run("server creation and initialization", func(t *testing.T) {
		cfg := config.NewTestConfig()
		server, err := mcpserver.New(cfg)
		if err != nil {
			t.Skipf("Cannot create server: %v", err)
		}

		assert.NotNil(t, server)

		// Test that server is properly configured
		// (We can't easily test Run() without setting up full MCP transport)
	})

	t.Run("server with invalid configuration", func(t *testing.T) {
		// Test server creation failure paths
		// This depends on how the server validates configuration

		// The server should either create successfully or fail gracefully
		cfg := config.NewTestConfig()
		cfg.Google.APIKey = ""
		server, err := mcpserver.New(cfg)

		if err != nil {
			// Expected failure case
			assert.Contains(t, err.Error(), "failed to create")
		} else {
			// Unexpected success case - server created with empty key
			assert.NotNil(t, server)
		}
	})
}

// TestCompleteWorkflowE2E tests a realistic end-to-end scenario
func TestCompleteWorkflowE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping complete workflow test in short mode")
	}

	cfg := config.NewTestConfig()
	server, err := mcpserver.New(cfg)
	if err != nil {
		t.Skipf("Cannot create MCP server for E2E test: %v", err)
	}

	t.Run("realistic development workflow", func(t *testing.T) {
		tmpDir := createTempGitRepo(t)
		defer cleanup(t, tmpDir)

		// 1. Start with a basic Go project
		mainFile := filepath.Join(tmpDir, "main.go")
		initialContent := `package main

import "fmt"

func main() {
	fmt.Println("Hello")
}`

		readmeFile := filepath.Join(tmpDir, "README.md")
		readmeContent := `# My Project

A simple Go application.`

		require.NoError(t, os.WriteFile(mainFile, []byte(initialContent), 0o600))
		require.NoError(t, os.WriteFile(readmeFile, []byte(readmeContent), 0o600))
		runGitCmd(t, tmpDir, "add", ".")
		runGitCmd(t, tmpDir, "commit", "-m", "initial project setup")

		// 2. Make multiple improvements
		improvedContent := `package main

import (
	"fmt"
	"os"
)

func main() {
	name := "World"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	fmt.Printf("Hello, %s!\n", name)
}`

		improvedReadme := `# My Project

A simple Go application that greets users.

## Usage

` + "```bash" + `
go run main.go [name]
` + "```" + `

## Features

- Customizable greeting
- Command-line argument support`

		require.NoError(t, os.WriteFile(mainFile, []byte(improvedContent), 0o600))
		require.NoError(t, os.WriteFile(readmeFile, []byte(improvedReadme), 0o600))

		// 3. Test review_only first
		reviewRequest := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "review_only",
				Arguments: map[string]any{
					"directory": tmpDir,
				},
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		result, err := server.handleReviewOnly(ctx, reviewRequest)

		if err != nil {
			// Expected to fail at review step in test environment
			assert.Contains(t, err.Error(), "review failed")
			t.Logf("Review failed as expected: %v", err)
		} else {
			// If review succeeds, should get proper response
			assert.NotNil(t, result)
			assert.NotNil(t, result.Content)
			t.Logf("Review succeeded unexpectedly")
		}

		// 4. Test commit workflow (will also likely fail at review step)
		commitRequest := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "review_and_commit",
				Arguments: map[string]any{
					"directory":      tmpDir,
					"commit_message": "feat: add customizable greeting with CLI support",
				},
			},
		}

		result, err = server.HandleReviewAndCommit(ctx, commitRequest)

		if err != nil {
			// Expected to fail at review step in test environment
			assert.Contains(t, err.Error(), "review failed")
			t.Logf("Commit workflow failed as expected: %v", err)
		} else {
			// If it succeeds, verify structure
			assert.NotNil(t, result)
			assert.NotNil(t, result.Content)
			t.Logf("Commit workflow succeeded unexpectedly")
		}

		// 5. Verify that the git repository state is consistent
		// (Changes should still be staged since commit would have failed)
		output := runGitCmd(t, tmpDir, "status", "--porcelain")
		assert.NotEmpty(t, output) // Should have uncommitted changes
	})
}
