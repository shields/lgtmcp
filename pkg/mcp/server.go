package mcp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

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
	s.logger.Info("Getting git diff", "directory", directory)
	diff, err := gitClient.GetDiff(ctx)
	s.logger.Info("Got diff result", "diff_length", len(diff), "error", err)
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
	findings, err := s.scanner.ScanDiff(ctx, diff, getFileContent)
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
	s.logger.Info("Calling Gemini for review", "changed_files", len(rc.changedFiles))
	reviewResult, err := s.reviewer.ReviewDiff(ctx, rc.diff, rc.changedFiles, rc.absPath)
	s.logger.Info("Gemini review complete", "error", err)

	return reviewResult, err
}

// HandleReviewOnly reviews code changes without committing.
func (s *Server) HandleReviewOnly(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.logger.Info("handleReviewOnly called", "tool", request.Params.Name, "arguments", request.Params.Arguments)

	// Parse arguments.
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		s.logger.Error("Invalid arguments format", "tool", request.Params.Name)

		return nil, ErrInvalidArguments
	}

	// Parse and validate directory.
	directory, err := s.parseDirectory(args)
	if err != nil {
		return nil, err
	}

	// Prepare for review (get diff, security scan, etc.)
	reviewCtx, earlyReturn, err := s.prepareReview(ctx, directory)
	if earlyReturn != nil {
		return earlyReturn, nil
	}
	if err != nil {
		return nil, err
	}

	// Perform the review.
	reviewResult, err := s.performReview(ctx, reviewCtx)
	if err != nil {
		return nil, fmt.Errorf("review failed: %w", err)
	}

	// Return review result (approved or not).
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
	s.logger.Info("handleReviewAndCommit called", "tool", request.Params.Name, "arguments", request.Params.Arguments)

	// Parse arguments.
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return nil, ErrInvalidArguments
	}

	// Parse and validate directory.
	directory, err := s.parseDirectory(args)
	if err != nil {
		return nil, err
	}

	// Parse commit message.
	commitMessage, ok := args["commit_message"].(string)
	if !ok {
		return nil, ErrCommitMessageNotString
	}

	// Prepare for review (get diff, security scan, etc.)
	reviewCtx, earlyReturn, err := s.prepareReview(ctx, directory)
	if earlyReturn != nil {
		return earlyReturn, nil
	}
	if err != nil {
		return nil, err
	}

	// Perform the review.
	reviewResult, err := s.performReview(ctx, reviewCtx)
	if err != nil {
		return nil, fmt.Errorf("review failed: %w", err)
	}

	// If not approved, return review comments.
	if !reviewResult.LGTM {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("Review Result: NOT APPROVED\n\n" + reviewResult.Comments),
			},
		}, nil
	}

	// Changes are approved - proceed to commit.
	approvalMsg := "Review Result: APPROVED (LGTM)\n\n" + reviewResult.Comments

	// Stage all changes.
	if stageErr := reviewCtx.gitClient.StageAll(ctx); stageErr != nil {
		return nil, fmt.Errorf("failed to stage changes: %w", stageErr)
	}

	// Commit the changes.
	commitHash, err := reviewCtx.gitClient.Commit(ctx, commitMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to commit: %w", err)
	}

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
