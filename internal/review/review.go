// Package review provides code review functionality using Google Gemini.
package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/shields/lgtmcp/internal/config"
	"github.com/shields/lgtmcp/internal/logging"
	"github.com/shields/lgtmcp/internal/prompts"
	"google.golang.org/genai"
)

var (
	// ErrNoResponse indicates no response was received from Gemini.
	ErrNoResponse = errors.New("no response from Gemini")
	// ErrEmptyResponse indicates an empty response was received from Gemini.
	ErrEmptyResponse = errors.New("empty response from Gemini")
	// ErrEmptyDiff indicates that the diff is empty.
	ErrEmptyDiff = errors.New("diff cannot be empty")
	// ErrNoAuthMethod indicates no authentication method is configured.
	ErrNoAuthMethod = errors.New("no authentication method configured")
)

// Result represents the result of a code review.
type Result struct {
	Comments string `json:"comments"`
	LGTM     bool   `json:"lgtm"`
}

// GeminiClient abstracts the Gemini API operations for testing.
type GeminiClient interface {
	CreateChat(ctx context.Context, modelName string, genConfig *genai.GenerateContentConfig) (GeminiChat, error)
	GenerateContent(ctx context.Context, modelName string, contents []*genai.Content,
		genConfig *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
}

// GeminiChat abstracts the chat session operations.
type GeminiChat interface {
	SendMessage(ctx context.Context, part genai.Part) (*genai.GenerateContentResponse, error)
}

// RealGeminiClient implements GeminiClient using the actual genai.Client.
type RealGeminiClient struct {
	client *genai.Client
}

// CreateChat creates a new chat session.
func (c *RealGeminiClient) CreateChat(
	ctx context.Context, modelName string, genConfig *genai.GenerateContentConfig,
) (GeminiChat, error) {
	chat, err := c.client.Chats.Create(ctx, modelName, genConfig, nil)
	if err != nil {
		return nil, err
	}

	return &RealGeminiChat{chat: chat}, nil
}

// GenerateContent generates content using the Models API.
func (c *RealGeminiClient) GenerateContent(
	ctx context.Context, modelName string, contents []*genai.Content, genConfig *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error) {
	return c.client.Models.GenerateContent(ctx, modelName, contents, genConfig)
}

// RealGeminiChat implements GeminiChat using the actual chat session.
type RealGeminiChat struct {
	chat *genai.Chat
}

// SendMessage sends a message to the chat session.
func (c *RealGeminiChat) SendMessage(ctx context.Context, part genai.Part) (*genai.GenerateContentResponse, error) {
	return c.chat.SendMessage(ctx, part)
}

// Reviewer handles code review using Gemini.
type Reviewer struct {
	client        GeminiClient
	retryConfig   *config.RetryConfig
	modelName     string
	temperature   float32
	promptManager *prompts.Manager
	logger        logging.Logger
}

// New creates a new Reviewer instance.
// New creates a new Reviewer with the Gemini API client.
// New creates a new Reviewer with the Gemini API client.
func New(cfg *config.Config, logger logging.Logger) (*Reviewer, error) {
	ctx := context.Background()

	// Create client configuration.
	clientConfig := &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
	}

	// Handle authentication based on configuration.
	switch {
	case cfg.Google.APIKey != "":
		// Use API key if provided.
		clientConfig.APIKey = cfg.Google.APIKey
		logger.Info("Using API key authentication")
	case cfg.Google.UseADC:
		// Use Application Default Credentials.
		// The genai client will automatically use ADC when no API key is provided
		// and no explicit credentials are set.
		logger.Info("Using Application Default Credentials")
		// Note: We don't need to explicitly set credentials here.
		// The SDK will automatically use ADC when APIKey is empty.
	default:
		// This shouldn't happen due to config validation, but handle it anyway.
		return nil, ErrNoAuthMethod
	}

	client, err := genai.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	return &Reviewer{
		client:      &RealGeminiClient{client: client},
		modelName:   cfg.Gemini.Model,
		temperature: cfg.Gemini.Temperature,
		retryConfig: cfg.Gemini.Retry,
		promptManager: prompts.New(
			cfg.Prompts.ReviewPromptPath,
			cfg.Prompts.ContextGatheringPromptPath,
		),
		logger: logger,
	}, nil
}

// NewWithClient creates a new Reviewer with a custom client (for testing).
//
//nolint:lll // Long function signature
func NewWithClient(client GeminiClient, modelName string, temperature float32, retryConfig *config.RetryConfig, promptManager *prompts.Manager) *Reviewer {
	return &Reviewer{
		client:        client,
		modelName:     modelName,
		temperature:   temperature,
		retryConfig:   retryConfig,
		promptManager: promptManager,
	}
}

// ReviewDiff reviews a git diff and returns the review result.
// isRetryableError checks if the error is retryable (rate limit or server errors).
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check if it's a genai.APIError.
	var apiErr *genai.APIError
	if errors.As(err, &apiErr) {
		// Check HTTP status code.
		switch apiErr.Code {
		case http.StatusRequestTimeout: // 408.
			return true
		case http.StatusTooManyRequests: // 429.
			return true
		case http.StatusInternalServerError: // 500.
			return true
		case http.StatusBadGateway: // 502.
			return true
		case http.StatusServiceUnavailable: // 503.
			return true
		case http.StatusGatewayTimeout: // 504.
			return true
		case http.StatusNotImplemented: // 501 - NOT retryable.
			return false
		default:
			// Fall through to check Status field
		}

		// Also check the Status field for gRPC-style status codes.
		switch apiErr.Status {
		case "RESOURCE_EXHAUSTED", "INTERNAL", "UNAVAILABLE", "DEADLINE_EXCEEDED":
			return true
		default:
			return false
		}
	}

	// Fallback to string matching for non-APIError errors.
	errStr := err.Error()

	// Rate limit errors (429).
	if strings.Contains(errStr, "Error 429") || strings.Contains(errStr, "RESOURCE_EXHAUSTED") {
		return true
	}

	// Request timeout (408).
	if strings.Contains(errStr, "Error 408") {
		return true
	}

	// Server errors (500, 502, 503, 504 but NOT 501).
	serverErrors := []string{
		"Error 500", "Error 502", "Error 503", "Error 504",
		"INTERNAL", "UNAVAILABLE", "DEADLINE_EXCEEDED",
	}
	for _, code := range serverErrors {
		if strings.Contains(errStr, code) {
			return true
		}
	}

	return false
}

// extractRetryDelay attempts to extract a retry delay from the error details.
func extractRetryDelay(err error) time.Duration {
	if err == nil {
		return 0
	}

	// Check if it's a genai.APIError with Details.
	var apiErr *genai.APIError
	if errors.As(err, &apiErr) && apiErr.Details != nil {
		// Look for RetryInfo in the Details field.
		for _, detail := range apiErr.Details {
			// Check if this detail has a @type field indicating RetryInfo.
			if typeVal, ok := detail["@type"].(string); ok && strings.Contains(typeVal, "RetryInfo") {
				// Look for retryDelay field.
				if retryDelayVal, ok := detail["retryDelay"].(string); ok {
					if duration, parseErr := time.ParseDuration(retryDelayVal); parseErr == nil {
						return duration
					}
				}
			}
		}
	}

	// Fallback to string parsing for non-APIError errors or when Details is not structured.
	errStr := err.Error()

	// Look for retryDelay in the error details (e.g., "retryDelay:15s").
	if idx := strings.Index(errStr, "retryDelay:"); idx != -1 {
		start := idx + len("retryDelay:")
		end := strings.IndexAny(errStr[start:], " ]}")
		if end == -1 {
			end = len(errStr) - start
		}

		delayStr := errStr[start : start+end]
		if duration, err := time.ParseDuration(delayStr); err == nil {
			return duration
		}
	}

	return 0
}

// calculateBackoff calculates the exponential backoff with jitter.
func calculateBackoff(attempt int, retryConfig *config.RetryConfig) time.Duration {
	// Parse initial and max backoff.
	initialBackoff, err := time.ParseDuration(retryConfig.InitialBackoff)
	if err != nil || initialBackoff == 0 {
		initialBackoff = time.Second
	}

	maxBackoff, err := time.ParseDuration(retryConfig.MaxBackoff)
	if err != nil || maxBackoff == 0 {
		maxBackoff = 60 * time.Second
	}

	// Calculate exponential backoff.
	backoff := float64(initialBackoff) * math.Pow(retryConfig.BackoffMultiplier, float64(attempt))

	// Add jitter (±20%).
	jitter := (rand.Float64() * 0.4) - 0.2 //nolint:gosec // math/rand is sufficient for jitter
	backoff *= (1 + jitter)

	// Cap at max backoff.
	if backoff > float64(maxBackoff) {
		backoff = float64(maxBackoff)
	}

	return time.Duration(backoff)
}

// retryableOperation performs an operation with retry logic.
func (r *Reviewer) retryableOperation( //nolint:funcorder // Helper method used by ReviewDiff
	ctx context.Context,
	operation func() error,
	operationName string,
) error {
	if r.retryConfig == nil || r.retryConfig.MaxRetries <= 0 {
		// No retry configured, just run the operation once.
		return operation()
	}

	var lastErr error
	for attempt := 0; attempt <= r.retryConfig.MaxRetries; attempt++ {
		// Check context cancellation.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Execute the operation.
		err := operation()
		if err == nil {
			return nil // Success!
		}

		lastErr = err

		// Check if error is retryable.
		if !isRetryableError(err) {
			return err // Non-retryable error.
		}

		// Don't retry if we've exhausted attempts.
		if attempt >= r.retryConfig.MaxRetries {
			break
		}

		// Calculate backoff duration.
		var backoff time.Duration

		// First, check if the API provided a retry delay.
		if apiDelay := extractRetryDelay(err); apiDelay > 0 {
			backoff = apiDelay
			r.logger.Debug("Using API-provided retry delay",
				"operation", operationName,
				"attempt", attempt+1,
				"delay", backoff)
		} else {
			// Use exponential backoff with jitter.
			backoff = calculateBackoff(attempt, r.retryConfig)
			r.logger.Debug("Using calculated backoff",
				"operation", operationName,
				"attempt", attempt+1,
				"delay", backoff)
		}

		// Wait before retrying.
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()

			return ctx.Err()
		case <-timer.C:
			// Continue to next attempt.
		}

		r.logger.Info("Retrying operation after rate limit",
			"operation", operationName,
			"attempt", attempt+2,
			"max_attempts", r.retryConfig.MaxRetries+1)
	}

	return fmt.Errorf("operation %s failed after %d attempts: %w", operationName, r.retryConfig.MaxRetries+1, lastErr)
}

// ReviewDiff performs a code review on the provided diff.
func (r *Reviewer) ReviewDiff(
	ctx context.Context, diff string, changedFiles []string, repoPath string,
) (*Result, error) {
	// Validate inputs.
	if diff == "" {
		return nil, ErrEmptyDiff
	}

	// Phase 1: Let Gemini analyze the code with tool support for file retrieval.
	contextPrompt, err := r.promptManager.BuildContextGatheringPrompt(diff, changedFiles)
	if err != nil {
		return nil, fmt.Errorf("failed to build context gathering prompt: %w", err)
	}

	// Configure the model with tools for context gathering.
	toolConfig := &genai.GenerateContentConfig{
		Temperature: &r.temperature,
	}

	// Define the file retrieval tool.
	fileRetrievalTool := &genai.Tool{
		FunctionDeclarations: []*genai.FunctionDeclaration{
			{
				Name:        "get_file_content",
				Description: "Retrieve the content of a file from the repository",
				Parameters: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"filepath": {
							Type:        genai.TypeString,
							Description: "Path to the file relative to repository root",
						},
					},
					Required: []string{"filepath"},
				},
			},
		},
	}

	toolConfig.Tools = []*genai.Tool{fileRetrievalTool}

	// Start the chat session for context gathering.
	chat, err := r.client.CreateChat(ctx, r.modelName, toolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create chat session: %w", err)
	}

	// Send the initial prompt with retry logic.
	promptPart := genai.NewPartFromText(contextPrompt)
	var response *genai.GenerateContentResponse
	var analysisText string

	err = r.retryableOperation(ctx, func() error {
		var sendErr error
		response, sendErr = chat.SendMessage(ctx, *promptPart)

		return sendErr
	}, "initial_prompt")
	if err != nil {
		return nil, fmt.Errorf("failed to send message to Gemini: %w", err)
	}

	// Handle function calls.
	for response != nil && len(response.Candidates) > 0 {
		candidate := response.Candidates[0]

		// Check if the model made any function calls.
		var hasToolCalls bool
		for _, part := range candidate.Content.Parts {
			if part.FunctionCall != nil {
				hasToolCalls = true
				r.logger.Debug("Model requested file",
					"function", part.FunctionCall.Name,
					"filepath", part.FunctionCall.Args["filepath"])

				// Send the function response back with retry logic.
				funcResponse := r.handleFileRetrieval(part.FunctionCall, repoPath)

				// Log the function response for debugging.
				r.logger.Debug("Sending function response",
					"function", part.FunctionCall.Name)

				err = r.retryableOperation(ctx, func() error {
					var sendErr error
					response, sendErr = chat.SendMessage(ctx, *funcResponse)

					return sendErr
				}, "function_response")
				if err != nil {
					return nil, fmt.Errorf("failed to send function response: %w", err)
				}

				break
			} else if part.Text != "" {
				// Capture any analysis text from the model.
				analysisText = part.Text
			}
		}

		// If no tool calls, we have the analysis response.
		if !hasToolCalls {
			break
		}
	}

	// Phase 2: Get structured review result without tools.
	reviewPrompt, err := r.promptManager.BuildReviewPrompt(diff, changedFiles, analysisText)
	if err != nil {
		return nil, fmt.Errorf("failed to build review prompt: %w", err)
	}

	// Configure for structured JSON output without tools.
	jsonConfig := &genai.GenerateContentConfig{
		Temperature:      &r.temperature,
		ResponseMIMEType: "application/json",
		ResponseSchema: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"lgtm": {
					Type:        genai.TypeBoolean,
					Description: "Whether the code is approved for production",
				},
				"comments": {
					Type:        genai.TypeString,
					Description: "Review comments or issues found",
				},
			},
			Required: []string{"lgtm", "comments"},
		},
	}

	// Use GenerateContent API directly for structured JSON output.
	// The Chat API doesn't support ResponseMIMEType/ResponseSchema.
	reviewContent := []*genai.Content{
		{
			Parts: []*genai.Part{genai.NewPartFromText(reviewPrompt)},
			Role:  "user",
		},
	}

	var reviewResponse *genai.GenerateContentResponse
	err = r.retryableOperation(ctx, func() error {
		var sendErr error
		reviewResponse, sendErr = r.client.GenerateContent(ctx, r.modelName, reviewContent, jsonConfig)

		return sendErr
	}, "review_prompt")
	if err != nil {
		return nil, fmt.Errorf("failed to get review response: %w", err)
	}

	// Parse the structured response.
	if reviewResponse == nil || len(reviewResponse.Candidates) == 0 {
		return nil, ErrNoResponse
	}

	candidate := reviewResponse.Candidates[0]
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			// Log the raw response for debugging.
			r.logger.Debug("Raw review response from Gemini", "text", part.Text)

			// Parse the JSON response.
			var result Result
			if err := json.Unmarshal([]byte(part.Text), &result); err != nil {
				return nil, fmt.Errorf("failed to parse review response: %w", err)
			}

			return &result, nil
		}
	}

	return nil, ErrEmptyResponse
}

// handleFileRetrieval handles file retrieval tool calls from Gemini.
func (*Reviewer) handleFileRetrieval(funcCall *genai.FunctionCall, repoPath string) *genai.Part {
	// Extract the filepath argument.
	requestedPath, ok := funcCall.Args["filepath"].(string)
	if !ok {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				"error": "filepath parameter must be a string",
			},
		)
	}

	// Validate and resolve the file path.
	if strings.Contains(requestedPath, "..") {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				"error": "invalid filepath: path traversal not allowed",
			},
		)
	}

	// Clean and join the path.
	fullPath := filepath.Join(repoPath, requestedPath)

	// CRITICAL SECURITY CHECK: Resolve to absolute path and verify it's within repo
	// This prevents symlink attacks and other path traversal techniques.
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				"error": fmt.Sprintf("failed to resolve path: %v", err),
			},
		)
	}

	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				"error": fmt.Sprintf("failed to resolve repository path: %v", err),
			},
		)
	}

	// First check if the path (before symlink resolution) is within the repository
	// This handles the case where the file doesn't exist yet.
	if !strings.HasPrefix(absPath, absRepoPath+string(filepath.Separator)) && absPath != absRepoPath {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				"error": "access denied: path is outside repository",
			},
		)
	}

	// SECURITY CHECK: Check if file is gitignored
	isIgnored, err := isFileGitIgnored(requestedPath, repoPath)
	if err != nil {
		// Fail closed on any error for security
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				"error": fmt.Sprintf("access denied: unable to verify gitignore status: %v", err),
			},
		)
	}
	if isIgnored {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				"error": "access denied: file is gitignored",
			},
		)
	}

	// Now check if file exists and resolve symlinks if it does.
	realPath := absPath
	if info, statErr := os.Lstat(absPath); statErr == nil {
		// File/symlink exists.
		if info.Mode()&os.ModeSymlink != 0 {
			// It's a symlink, evaluate it.
			evaluated, evalErr := filepath.EvalSymlinks(absPath)
			if evalErr != nil {
				return genai.NewPartFromFunctionResponse(
					funcCall.Name,
					map[string]any{
						"error": fmt.Sprintf("failed to evaluate symlink: %v", evalErr),
					},
				)
			}

			// Check if the symlink target is within the repository.
			if !strings.HasPrefix(evaluated, absRepoPath+string(filepath.Separator)) && evaluated != absRepoPath {
				return genai.NewPartFromFunctionResponse(
					funcCall.Name,
					map[string]any{
						"error": "access denied: path is outside repository",
					},
				)
			}
			realPath = evaluated
		}
	}

	// Read the file content.
	content, err := os.ReadFile(realPath) //nolint:gosec // Path is validated and sanitized above
	if err != nil {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				"error": fmt.Sprintf("failed to read file: %v", err),
			},
		)
	}

	return genai.NewPartFromFunctionResponse(
		funcCall.Name,
		map[string]any{
			"content": string(content),
		},
	)
}

// isFileGitIgnored checks if a file is ignored by git.
// It uses git check-ignore to properly respect all .gitignore rules including nested ones.
// Returns (isIgnored, error) - if error is not nil, the caller should fail closed for security.
func isFileGitIgnored(relativePath, repoPath string) (bool, error) {
	// Use git check-ignore to check if the file is ignored
	// This respects all .gitignore files in the repository hierarchy
	cmd := exec.Command("git", "check-ignore", relativePath)
	cmd.Dir = repoPath

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Check if it's an exit error
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// git check-ignore returns exit code 1 when file is NOT ignored
			// This is expected behavior, not an error
			if exitErr.ExitCode() == 1 {
				return false, nil
			}
			// Exit code 128 typically means not a git repository or other git errors
			// We should fail closed in this case for security
			if exitErr.ExitCode() == 128 {
				stderrStr := stderr.String()
				return false, fmt.Errorf("git check-ignore failed: %w: %s", err, stderrStr)
			}
		}
		// For any other error (e.g., git not found), fail closed
		return false, fmt.Errorf("failed to execute git check-ignore: %w", err)
	}
	// Exit code 0 means the file is ignored
	return true, nil
}
