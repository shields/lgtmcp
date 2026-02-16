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

// Package mcp implements the Model Context Protocol server for LGTMCP.
package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"msrl.dev/lgtmcp/internal/config"
	"msrl.dev/lgtmcp/internal/git"
	"msrl.dev/lgtmcp/internal/logging"
	"msrl.dev/lgtmcp/internal/progress"
	"msrl.dev/lgtmcp/internal/review"
	"msrl.dev/lgtmcp/internal/security"
)

var (
	// ErrDirectoryNotString indicates directory argument is not a string.
	ErrDirectoryNotString = errors.New("directory must be a string")
	// ErrInvalidArguments indicates invalid arguments format.
	ErrInvalidArguments = errors.New("invalid arguments format")
	// ErrCommitMessageNotString indicates commit_message argument is not a string.
	ErrCommitMessageNotString = errors.New("commit_message must be a string")
)

// Server represents the MCP server.
const (
	// Version is the version of the LGTMCP server.
	Version = "0.0.0-dev"
)

// Server implements the MCP server for LGTMCP.
type Server struct {
	mcpServer *server.MCPServer
	reviewer  *review.Reviewer
	scanner   *security.Scanner
	logger    logging.Logger
	config    *config.Config
}

// New creates a new MCP server instance.
func New(cfg *config.Config, logger logging.Logger) (*Server, error) {
	// Create MCP server with stdio transport.
	mcpServer := server.NewMCPServer(
		"lgtmcp",
		Version,
	)

	// Initialize components.
	reviewer, err := review.New(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create reviewer: %w", err)
	}

	scanner, err := security.New(cfg.Gitleaks.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to create security scanner: %w", err)
	}

	s := &Server{
		mcpServer: mcpServer,
		reviewer:  reviewer,
		scanner:   scanner,
		logger:    logger,
		config:    cfg,
	}

	// Register the review_and_commit tool.
	s.registerTools()

	return s, nil
}

// registerTools registers all MCP tools.
func (s *Server) registerTools() { //nolint:funcorder // Helper method
	// Register review_only tool.
	s.mcpServer.AddTool(mcp.Tool{
		Name: "review_only",
		Description: "Review code changes using Gemini and return feedback without committing. " +
			"Returns review comments and approval status.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"directory": map[string]any{
					"type":        "string",
					"description": "Path to the git repository directory to review",
				},
			},
			Required: []string{"directory"},
		},
	}, s.HandleReviewOnly)

	// Register review_and_commit tool.
	s.mcpServer.AddTool(mcp.Tool{
		Name: "review_and_commit",
		Description: "Review code changes using Gemini and commit if approved (LGTM). " +
			"Returns review comments if not approved or success message with commit hash if approved and committed.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"directory": map[string]any{
					"type":        "string",
					"description": "Path to the git repository directory to review",
				},
				"commit_message": map[string]any{
					"type":        "string",
					"description": "Commit message to use if changes are approved",
				},
			},
			Required: []string{"directory", "commit_message"},
		},
	}, s.HandleReviewAndCommit)
}

// parseDirectory extracts and validates the directory argument from the request.
func (*Server) parseDirectory(args map[string]any) (string, error) { //nolint:funcorder // Helper method
	directory, ok := args["directory"].(string)
	if !ok {
		return "", ErrDirectoryNotString
	}

	// Resolve to absolute path.
	absPath, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("failed to resolve directory path: %w", err)
	}

	return absPath, nil
}

// generateRequestID creates a short unique ID for request tracing.
func generateRequestID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate request ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// reviewContext holds the context needed for performing a review.
type reviewContext struct {
	gitClient         *git.Git
	diff              string
	absPath           string
	changedFiles      []string
	agentInstructions string
}

// createProgressReporter creates a progress reporter based on whether the request includes a progress token.
//
//nolint:funcorder // Helper method
func (s *Server) createProgressReporter(request mcp.CallToolRequest) progress.Reporter {
	if request.Params.Meta != nil && request.Params.Meta.ProgressToken != nil {
		return progress.NewMCPReporter(s.mcpServer, request.Params.Meta.ProgressToken)
	}
	return progress.NewNoOpReporter()
}

// formatReviewResponse formats the review result with usage statistics.
// If commitHash is provided, it adds a commit success message before the stats footer.
func formatReviewResponse(result *review.Result, commitHash string) string {
	var status string
	if result.LGTM {
		status = "Review Result: APPROVED (LGTM)"
	} else {
		status = "Review Result: NOT APPROVED"
	}

	var sb strings.Builder
	_, _ = sb.WriteString(status)
	_, _ = sb.WriteString("\n\n")
	_, _ = sb.WriteString(result.Comments)

	// Add commit success message if provided.
	if commitHash != "" {
		_, _ = sb.WriteString("\n\nChanges committed successfully!\nCommit: ")
		_, _ = sb.WriteString(commitHash)
	}

	// Add usage statistics footer if available.
	if result.TokenUsage != nil || result.DurationMS > 0 || result.CostUSD > 0 {
		_, _ = sb.WriteString("\n\n---\n")

		var parts []string

		// Duration.
		if result.DurationMS > 0 {
			seconds := float64(result.DurationMS) / 1000.0
			parts = append(parts, fmt.Sprintf("Duration: %.1fs", seconds))
		}

		// Token usage.
		if result.TokenUsage != nil {
			tokenPart := fmt.Sprintf("Tokens: %d (in: %d, out: %d)",
				result.TokenUsage.TotalTokens,
				result.TokenUsage.PromptTokens,
				result.TokenUsage.CandidatesTokens)
			parts = append(parts, tokenPart)
		}

		// Cost.
		if result.CostUSD > 0 {
			var costStr string
			if result.CostUSD < 0.01 {
				costStr = fmt.Sprintf("Cost: $%.4f", result.CostUSD)
			} else {
				costStr = fmt.Sprintf("Cost: $%.3f", result.CostUSD)
			}
			parts = append(parts, costStr)
		}

		_, _ = sb.WriteString(strings.Join(parts, " | "))
	}

	return sb.String()
}

// prepareReview handles common review preparation logic: getting diff, security scan, etc.
//
//nolint:funcorder // Helper method
func (s *Server) prepareReview(
	ctx context.Context, directory string, reporter progress.Reporter, totalSteps float64,
) (*reviewContext, *mcp.CallToolResult, error) {
	// Create a git client for this repository.
	var gitConfig *config.GitConfig
	if s.config != nil {
		gitConfig = &s.config.Git
	}
	gitClient, err := git.New(directory, gitConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid git repository: %w", err)
	}

	// Report progress: getting git diff.
	reporter.Report(ctx, 1, totalSteps, "Getting git diff...")

	// Get the diff of staged and unstaged changes.
	start := time.Now()
	diff, err := gitClient.GetDiff(ctx)
	diffDuration := time.Since(start)
	if err != nil {
		s.logger.Error("Git diff failed",
			"repo", filepath.Base(directory),
			"duration_ms", diffDuration.Milliseconds(),
			"error", err)
	} else {
		s.logger.Info("Git diff completed",
			"repo", filepath.Base(directory),
			"diff_size", len(diff),
			"duration_ms", diffDuration.Milliseconds())
	}
	if err != nil {
		// Check if it's the "no changes" error.
		if errors.Is(err, git.ErrNoChanges) {
			return nil, &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.NewTextContent("No changes to review"),
				},
			}, nil
		}

		return nil, nil, fmt.Errorf("failed to get diff: %w", err)
	}

	if diff == "" {
		return nil, &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("No changes to review"),
			},
		}, nil
	}

	// Report progress: security scan.
	reporter.Report(ctx, 2, totalSteps, "Running security scan...")

	// Security scan on the changed files using secure git client.
	getFileContent := func(path string) (string, error) {
		return gitClient.GetFileContent(ctx, path)
	}
	scanStart := time.Now()
	findings, err := s.scanner.ScanDiff(ctx, diff, getFileContent)
	scanDuration := time.Since(scanStart)
	if err != nil {
		s.logger.Error("Security scan failed",
			"duration_ms", scanDuration.Milliseconds(),
			"error", err)
	} else {
		s.logger.Info("Security scan completed",
			"duration_ms", scanDuration.Milliseconds(),
			"findings", len(findings))
	}
	if err != nil {
		return nil, nil, fmt.Errorf("security scan failed: %w", err)
	}

	if security.HasFindings(findings) {
		return nil, &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("Security scan failed:\n" + security.FormatFindings(findings)),
			},
			IsError: true,
		}, nil
	}

	// Extract list of changed files from the diff for Gemini's file retrieval.
	changedFiles := security.ExtractChangedFiles(diff)

	// Discover AGENTS.md files relevant to the changed files.
	var agentInstructions string
	agentFiles, err := gitClient.FindAgentFiles(changedFiles)
	if err != nil {
		s.logger.Warn("Failed to discover AGENTS.md files", "error", err)
	} else if len(agentFiles) > 0 {
		agentInstructions = git.FormatAgentInstructions(agentFiles)
		paths := make([]string, len(agentFiles))
		for i, f := range agentFiles {
			paths[i] = f.Path
		}
		s.logger.Info("Discovered AGENTS.md files", "files", paths)
	}

	return &reviewContext{
		gitClient:         gitClient,
		diff:              diff,
		changedFiles:      changedFiles,
		absPath:           directory,
		agentInstructions: agentInstructions,
	}, nil, nil
}

// performReview executes the review with Gemini.
//
//nolint:funcorder // Helper method
func (s *Server) performReview(
	ctx context.Context, rc *reviewContext, reporter progress.Reporter, totalSteps float64,
) (*review.Result, error) {
	start := time.Now()
	s.logger.Info("Starting Gemini review",
		"repo", filepath.Base(rc.absPath),
		"changed_files", len(rc.changedFiles),
		"diff_size", len(rc.diff))

	// Report progress: analyzing code context and fetching files.
	reporter.Report(ctx, 3, totalSteps, "Analyzing code context...")

	// Create file fetch callback that reports progress during file fetching.
	fileFetchCallback := func(path string) {
		reporter.Report(ctx, 3, totalSteps, "Fetching file: "+path)
	}

	reviewResult, err := s.reviewer.ReviewDiff(ctx, rc.diff, rc.changedFiles, rc.absPath,
		review.WithFileFetchCallback(fileFetchCallback),
		review.WithAgentInstructions(rc.agentInstructions))

	duration := time.Since(start)
	if err != nil {
		s.logger.Error("Gemini review failed",
			"duration_ms", duration.Milliseconds(),
			"error", err)
	} else {
		// Report progress: review generation complete.
		reporter.Report(ctx, 4, totalSteps, "Review complete")
		s.logger.Info("Gemini review completed",
			"duration_ms", duration.Milliseconds(),
			"approved", reviewResult.LGTM)
	}

	return reviewResult, err
}

// HandleReviewOnly reviews code changes without committing.
func (s *Server) HandleReviewOnly(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	requestID, err := generateRequestID()
	if err != nil {
		s.logger.Error("Failed to generate request ID", "error", err)
		return nil, err
	}
	start := time.Now()

	s.logger.Info("Review request started",
		"request_id", requestID,
		"tool", "review_only")

	// Create progress reporter based on whether client requested progress.
	reporter := s.createProgressReporter(request)

	// Parse arguments.
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		s.logger.Error("Invalid arguments format",
			"request_id", requestID,
			"tool", "review_only")
		return nil, ErrInvalidArguments
	}

	// Parse and validate directory.
	directory, err := s.parseDirectory(args)
	if err != nil {
		elapsed := time.Since(start)
		s.logger.Error("Failed to parse directory",
			"request_id", requestID,
			"total_duration_ms", elapsed.Milliseconds(),
			"error", err)
		return nil, err
	}

	s.logger.Info("Processing repository",
		"request_id", requestID,
		"repo", filepath.Base(directory))

	// review_only has 4 total steps (no staging/committing).
	const totalSteps = 4.0

	// Prepare for review (get diff, security scan, etc.)
	prepStart := time.Now()
	reviewCtx, earlyReturn, err := s.prepareReview(ctx, directory, reporter, totalSteps)
	prepDuration := time.Since(prepStart)

	s.logger.Info("Review preparation completed",
		"request_id", requestID,
		"duration_ms", prepDuration.Milliseconds(),
		"early_return", earlyReturn != nil,
		"error", err)

	if earlyReturn != nil {
		return earlyReturn, nil
	}
	if err != nil {
		elapsed := time.Since(start)
		s.logger.Error("Review preparation failed",
			"request_id", requestID,
			"total_duration_ms", elapsed.Milliseconds(),
			"error", err)
		return nil, err
	}

	// Perform the review.
	s.logger.Info("Starting review analysis",
		"request_id", requestID)

	reviewResult, err := s.performReview(ctx, reviewCtx, reporter, totalSteps)
	if err != nil {
		elapsed := time.Since(start)
		s.logger.Error("Review failed",
			"request_id", requestID,
			"total_duration_ms", elapsed.Milliseconds(),
			"error", err)
		return nil, fmt.Errorf("review failed: %w", err)
	}

	// Return review result (approved or not).
	elapsed := time.Since(start)
	s.logger.Info("Review completed",
		"request_id", requestID,
		"approved", reviewResult.LGTM,
		"total_duration_ms", elapsed.Milliseconds())

	// Format the response with usage statistics.
	responseText := formatReviewResponse(reviewResult, "")

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(responseText),
		},
	}, nil
}

// HandleReviewAndCommit handles the review_and_commit tool invocation.
func (s *Server) HandleReviewAndCommit(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	requestID, err := generateRequestID()
	if err != nil {
		s.logger.Error("Failed to generate request ID", "error", err)
		return nil, err
	}
	start := time.Now()

	// Log request start without exposing arguments
	s.logger.Info("Review and commit request started",
		"request_id", requestID,
		"tool", "review_and_commit",
		"arguments", "map[...]")

	// Create progress reporter based on whether client requested progress.
	reporter := s.createProgressReporter(request)

	// Parse arguments.
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return nil, ErrInvalidArguments
	}

	// Parse and validate directory.
	directory, err := s.parseDirectory(args)
	if err != nil {
		elapsed := time.Since(start)
		s.logger.Error("Failed to parse directory",
			"request_id", requestID,
			"total_duration_ms", elapsed.Milliseconds(),
			"error", err)
		return nil, err
	}

	s.logger.Info("Processing repository",
		"request_id", requestID,
		"repo", filepath.Base(directory))

	// Parse commit message.
	commitMessage, ok := args["commit_message"].(string)
	if !ok {
		return nil, ErrCommitMessageNotString
	}

	// review_and_commit has 6 total steps (includes staging/committing).
	const totalSteps = 6.0

	// Prepare for review (get diff, security scan, etc.)
	prepStart := time.Now()
	reviewCtx, earlyReturn, err := s.prepareReview(ctx, directory, reporter, totalSteps)
	prepDuration := time.Since(prepStart)

	s.logger.Info("Review preparation completed",
		"request_id", requestID,
		"duration_ms", prepDuration.Milliseconds(),
		"early_return", earlyReturn != nil,
		"error", err)

	if earlyReturn != nil {
		return earlyReturn, nil
	}
	if err != nil {
		elapsed := time.Since(start)
		s.logger.Error("Review preparation failed",
			"request_id", requestID,
			"total_duration_ms", elapsed.Milliseconds(),
			"error", err)
		return nil, err
	}

	// Perform the review.
	s.logger.Info("Starting review analysis",
		"request_id", requestID)

	reviewResult, err := s.performReview(ctx, reviewCtx, reporter, totalSteps)
	if err != nil {
		elapsed := time.Since(start)
		s.logger.Error("Review failed",
			"request_id", requestID,
			"total_duration_ms", elapsed.Milliseconds(),
			"error", err)
		return nil, fmt.Errorf("review failed: %w", err)
	}

	// If not approved, return review comments with usage stats.
	if !reviewResult.LGTM {
		elapsed := time.Since(start)
		s.logger.Info("Review rejected changes",
			"request_id", requestID,
			"total_duration_ms", elapsed.Milliseconds())

		responseText := formatReviewResponse(reviewResult, "")
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent(responseText),
			},
		}, nil
	}

	// Changes are approved - proceed to commit.
	// Report progress: staging changes.
	reporter.Report(ctx, 5, 6, "Staging changes...")

	// Stage all changes.
	stageStart := time.Now()
	if stageErr := reviewCtx.gitClient.StageAll(ctx); stageErr != nil {
		elapsed := time.Since(start)
		s.logger.Error("Failed to stage changes",
			"request_id", requestID,
			"total_duration_ms", elapsed.Milliseconds(),
			"error", stageErr)
		return nil, fmt.Errorf("failed to stage changes: %w", stageErr)
	}
	stageDuration := time.Since(stageStart)
	s.logger.Info("Changes staged",
		"request_id", requestID,
		"duration_ms", stageDuration.Milliseconds())

	// Report progress: committing changes.
	reporter.Report(ctx, 6, 6, "Committing changes...")

	// Commit the changes.
	commitStart := time.Now()
	commitHash, err := reviewCtx.gitClient.Commit(ctx, commitMessage)
	if err != nil {
		elapsed := time.Since(start)
		s.logger.Error("Failed to commit",
			"request_id", requestID,
			"total_duration_ms", elapsed.Milliseconds(),
			"error", err)
		return nil, fmt.Errorf("failed to commit: %w", err)
	}
	commitDuration := time.Since(commitStart)

	elapsed := time.Since(start)
	s.logger.Info("Review and commit completed",
		"request_id", requestID,
		"commit_hash", commitHash,
		"commit_duration_ms", commitDuration.Milliseconds(),
		"total_duration_ms", elapsed.Milliseconds())

	// Format response with usage stats and commit message.
	responseText := formatReviewResponse(reviewResult, commitHash)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(responseText),
		},
	}, nil
}

// Run starts the MCP server.
func (s *Server) Run(_ context.Context) error {
	s.logger.Info("Starting LGTMCP server", "version", Version)

	return server.ServeStdio(s.mcpServer)
}
