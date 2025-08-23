package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/shields/lgtmcp/internal/config"
	"github.com/shields/lgtmcp/internal/git"
	"github.com/shields/lgtmcp/internal/logging"
	"github.com/shields/lgtmcp/internal/review"
	"github.com/shields/lgtmcp/internal/security"
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
func (s *Server) registerTools() {
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
func (*Server) parseDirectory(args map[string]any) (string, error) {
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
	gitClient    *git.Git
	diff         string
	absPath      string
	changedFiles []string
}

// prepareReview handles common review preparation logic: getting diff, security scan, etc.
func (s *Server) prepareReview(ctx context.Context, directory string) (*reviewContext, *mcp.CallToolResult, error) {
	// Create a git client for this repository.
	var gitConfig *config.GitConfig
	if s.config != nil {
		gitConfig = &s.config.Git
	}
	gitClient, err := git.New(directory, gitConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid git repository: %w", err)
	}

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

	return &reviewContext{
		gitClient:    gitClient,
		diff:         diff,
		changedFiles: changedFiles,
		absPath:      directory,
	}, nil, nil
}

// performReview executes the review with Gemini.
func (s *Server) performReview(ctx context.Context, rc *reviewContext) (*review.Result, error) {
	start := time.Now()
	s.logger.Info("Starting Gemini review",
		"repo", filepath.Base(rc.absPath),
		"changed_files", len(rc.changedFiles),
		"diff_size", len(rc.diff))

	reviewResult, err := s.reviewer.ReviewDiff(ctx, rc.diff, rc.changedFiles, rc.absPath)

	duration := time.Since(start)
	if err != nil {
		s.logger.Error("Gemini review failed",
			"duration_ms", duration.Milliseconds(),
			"error", err)
	} else {
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

	// Prepare for review (get diff, security scan, etc.)
	prepStart := time.Now()
	reviewCtx, earlyReturn, err := s.prepareReview(ctx, directory)
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

	reviewResult, err := s.performReview(ctx, reviewCtx)
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

	if reviewResult.LGTM {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("Review Result: APPROVED (LGTM)\n\n" + reviewResult.Comments),
			},
		}, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent("Review Result: NOT APPROVED\n\n" + reviewResult.Comments),
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

	// Prepare for review (get diff, security scan, etc.)
	prepStart := time.Now()
	reviewCtx, earlyReturn, err := s.prepareReview(ctx, directory)
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

	reviewResult, err := s.performReview(ctx, reviewCtx)
	if err != nil {
		elapsed := time.Since(start)
		s.logger.Error("Review failed",
			"request_id", requestID,
			"total_duration_ms", elapsed.Milliseconds(),
			"error", err)
		return nil, fmt.Errorf("review failed: %w", err)
	}

	// If not approved, return review comments.
	if !reviewResult.LGTM {
		elapsed := time.Since(start)
		s.logger.Info("Review rejected changes",
			"request_id", requestID,
			"total_duration_ms", elapsed.Milliseconds())

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("Review Result: NOT APPROVED\n\n" + reviewResult.Comments),
			},
		}, nil
	}

	// Changes are approved - proceed to commit.
	approvalMsg := "Review Result: APPROVED (LGTM)\n\n" + reviewResult.Comments

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

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(fmt.Sprintf("%s\n\nChanges committed successfully!\nCommit: %s", approvalMsg, commitHash)),
		},
	}, nil
}

// Run starts the MCP server.
func (s *Server) Run(_ context.Context) error {
	s.logger.Info("Starting LGTMCP server", "version", Version)

	return server.ServeStdio(s.mcpServer)
}
