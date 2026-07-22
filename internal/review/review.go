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

// Package review provides code review functionality using Google Gemini.
package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/genai"
	"msrl.dev/lgtmcp/internal/config"
	"msrl.dev/lgtmcp/internal/git"
	"msrl.dev/lgtmcp/internal/logging"
	"msrl.dev/lgtmcp/internal/prompts"
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
	// ErrQuotaExhausted indicates the Gemini API quota has been exceeded.
	ErrQuotaExhausted = errors.New("gemini API quota exhausted")
)

// quotaFailureType is the gRPC error detail type for quota exhaustion.
const quotaFailureType = "type.googleapis.com/google.rpc.QuotaFailure"

// maxRetrievedFileSize bounds how much of any single file handleFileRetrieval
// will return to the model. It is well above any plausible source file but
// low enough that even an attacker-supplied multi-gigabyte path cannot OOM
// the review process.
const maxRetrievedFileSize int64 = 16 * 1024 * 1024

// isQuotaExhaustedError checks if the error is a quota exhaustion (not rate limit).
// Quota failures have QuotaFailure in the error details, indicating a daily/monthly
// limit has been reached. This is distinct from rate limiting, which is temporary.
func isQuotaExhaustedError(err error) bool {
	if err == nil {
		return false
	}

	// Check APIError.Details for QuotaFailure type.
	var apiErr *genai.APIError
	if errors.As(err, &apiErr) && apiErr.Details != nil {
		for _, detail := range apiErr.Details {
			if typeVal, ok := detail["@type"].(string); ok {
				if typeVal == quotaFailureType {
					return true
				}
			}
		}
	}

	// Fallback: check error string for the QuotaFailure type.
	// This handles cases where the error is wrapped or stringified.
	return strings.Contains(err.Error(), quotaFailureType)
}

// TokenUsage contains token usage statistics from a review.
type TokenUsage struct {
	PromptTokens     int32 `json:"prompt_tokens"`
	CandidatesTokens int32 `json:"candidates_tokens"`
	TotalTokens      int32 `json:"total_tokens"`
	CachedTokens     int32 `json:"cached_tokens,omitempty"`
	ThoughtsTokens   int32 `json:"thoughts_tokens,omitempty"`
	ToolUseTokens    int32 `json:"tool_use_tokens,omitempty"`
}

// Result represents the result of a code review.
type Result struct {
	Comments        string      `json:"comments"`
	LGTM            bool        `json:"lgtm"`
	TokenUsage      *TokenUsage `json:"token_usage,omitempty"`
	DurationMS      int64       `json:"duration_ms,omitempty"`
	CostUSD         float64     `json:"cost_usd,omitempty"`
	CacheSavingsUSD float64     `json:"cache_savings_usd,omitempty"`
	Model           string      `json:"model,omitempty"`
}

// FileFetchCallback is called when a file is fetched during review.
type FileFetchCallback func(path string)

// GeminiClient abstracts the Gemini API operations for testing.
type GeminiClient interface {
	CreateChat(ctx context.Context, modelName string, genConfig *genai.GenerateContentConfig) (GeminiChat, error)
	GenerateContent(ctx context.Context, modelName string, contents []*genai.Content,
		genConfig *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
}

// GeminiChat abstracts the chat session operations. SendMessage is variadic,
// matching genai.Chat.SendMessage, so that a single turn can carry one
// function response part per function call the model made in parallel.
type GeminiChat interface {
	SendMessage(ctx context.Context, parts ...genai.Part) (*genai.GenerateContentResponse, error)
}

// Options contains optional parameters for a review.
type Options struct {
	FileFetchCallback FileFetchCallback
	Instructions      string
	// DeletedFiles is the subset of changed paths that the diff marks as deletions.
	DeletedFiles []string
}

// Option is a functional option for ReviewDiff.
type Option func(*Options)

// WithFileFetchCallback sets a callback to be invoked when files are fetched.
func WithFileFetchCallback(callback FileFetchCallback) Option {
	return func(opts *Options) {
		opts.FileFetchCallback = callback
	}
}

// WithInstructions sets project-specific instructions from AGENTS.md and REVIEW.md files.
func WithInstructions(instructions string) Option {
	return func(opts *Options) {
		opts.Instructions = instructions
	}
}

// WithDeletedFiles records which changed paths are deletions so the file
// retrieval tool can respond with a clear deleted-file message.
func WithDeletedFiles(deleted []string) Option {
	return func(opts *Options) {
		opts.DeletedFiles = deleted
	}
}

// Reviewer handles code review using Gemini.
type Reviewer struct {
	client        GeminiClient
	retryConfig   *config.RetryConfig
	modelName     string
	fallbackModel string
	temperature   float32
	promptManager *prompts.Manager
	logger        logging.Logger
}

const (
	defaultModel      = "gemini-3.6-flash"
	errorKey          = "error"
	errDeletedFileMsg = "file was deleted or renamed away in this change; the diff records the removal, " +
		"and a renamed file's content lives at its new path"

	// maxToolTurns bounds the Phase 1 tool-calling loop. Each turn can fetch
	// several files (parallel function calls), so this is generous for a code
	// review while still stopping a runaway model from burning tokens forever.
	maxToolTurns = 32
)

// modelPricing contains per-million-token pricing for supported models.
type modelPricing struct {
	InputPrice  float64 // USD per 1M input tokens
	OutputPrice float64 // USD per 1M output tokens
}

// pricingByModel maps model names to their pricing (≤200K context).
// Pricing from https://ai.google.dev/gemini-api/docs/pricing (no API available).
var pricingByModel = map[string]modelPricing{
	"gemini-3.6-flash":       {InputPrice: 1.50, OutputPrice: 7.50},
	"gemini-3.1-pro-preview": {InputPrice: 2.00, OutputPrice: 12.00},
	"gemini-2.5-pro":         {InputPrice: 1.25, OutputPrice: 10.00},
	"gemini-2.5-pro-preview": {InputPrice: 1.25, OutputPrice: 10.00},
	"gemini-2.5-flash":       {InputPrice: 0.30, OutputPrice: 2.50},
	"gemini-2.5-flash-lite":  {InputPrice: 0.10, OutputPrice: 0.40},
}

// tokenUsage tracks cumulative token counts across API calls.
type tokenUsage struct {
	PromptTokens     int32
	CandidatesTokens int32
	CachedTokens     int32
	ThoughtsTokens   int32
	ToolUseTokens    int32
}

// addFromResponse accumulates token counts from a Gemini response.
func (t *tokenUsage) addFromResponse(resp *genai.GenerateContentResponse) {
	if resp == nil || resp.UsageMetadata == nil {
		return
	}
	t.PromptTokens += resp.UsageMetadata.PromptTokenCount
	t.CandidatesTokens += resp.UsageMetadata.CandidatesTokenCount
	t.CachedTokens += resp.UsageMetadata.CachedContentTokenCount
	t.ThoughtsTokens += resp.UsageMetadata.ThoughtsTokenCount
	t.ToolUseTokens += resp.UsageMetadata.ToolUsePromptTokenCount
}

// total returns the total number of tokens used. It mirrors the genai
// TotalTokenCount definition: prompt + candidates + tool-use prompt + thoughts.
func (t *tokenUsage) total() int32 {
	return t.PromptTokens + t.CandidatesTokens + t.ToolUseTokens + t.ThoughtsTokens
}

// cost returns the estimated cost in USD for the given model.
// Returns -1 if the model is not in the pricing table.
// Note: Uses base pricing tier (≤200K context). Large contexts may cost more.
func (t *tokenUsage) cost(modelName string) float64 {
	pricing, ok := pricingByModel[modelName]
	if !ok {
		return -1 // Unknown model
	}
	// PromptTokens is the total effective prompt size and already includes
	// CachedTokens (per the genai UsageMetadata docs), so only the non-cached
	// remainder bills at the full input rate; cached tokens bill at 10%.
	// Clamp; the API should never report cached > prompt, but guard anyway.
	// Tool-use prompt tokens are reported separately from PromptTokens and
	// are also input.
	fullRateTokens := max(t.PromptTokens-t.CachedTokens, 0) + t.ToolUseTokens
	inputCost := float64(fullRateTokens)/1_000_000*pricing.InputPrice +
		float64(t.CachedTokens)/1_000_000*pricing.InputPrice*0.1
	// Output cost: includes thoughts tokens (reasoning models bill these as output).
	outputTokens := t.CandidatesTokens + t.ThoughtsTokens
	outputCost := float64(outputTokens) / 1_000_000 * pricing.OutputPrice
	return inputCost + outputCost
}

// costWithoutCaching is the baseline cost had no tokens been served from cache:
// every prompt token billed at the full input rate. It is the reference point
// for savings(). Returns -1 for unknown models, matching cost().
func (t *tokenUsage) costWithoutCaching(modelName string) float64 {
	pricing, ok := pricingByModel[modelName]
	if !ok {
		return -1
	}
	inputCost := float64(t.PromptTokens+t.ToolUseTokens) / 1_000_000 * pricing.InputPrice
	outputTokens := t.CandidatesTokens + t.ThoughtsTokens
	return inputCost + float64(outputTokens)/1_000_000*pricing.OutputPrice
}

// savings returns the USD saved by (implicit) context caching on this review:
// the baseline cost minus the actual discounted cost. Returns 0 for unknown
// models or when nothing was cached.
func (t *tokenUsage) savings(modelName string) float64 {
	base := t.costWithoutCaching(modelName)
	if base < 0 {
		return 0
	}
	if s := base - t.cost(modelName); s > 0 {
		return s
	}
	return 0
}

// cacheHitRate returns CachedTokens/PromptTokens in [0,1] — the fraction of the
// prompt served from cache (PromptTokens already includes cached tokens).
// Guards against divide-by-zero.
func (t *tokenUsage) cacheHitRate() float64 {
	if t.PromptTokens <= 0 {
		return 0
	}
	return float64(t.CachedTokens) / float64(t.PromptTokens)
}

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

	// Temperature is a pointer so an explicit 0 is honored; fall back to the
	// default only when it is genuinely unset (e.g. a hand-built config).
	temperature := float32(0.2)
	if cfg.Gemini.Temperature != nil {
		temperature = *cfg.Gemini.Temperature
	}

	return &Reviewer{
		client:        &RealGeminiClient{client: client},
		modelName:     cfg.Gemini.Model,
		fallbackModel: cfg.Gemini.FallbackModel,
		temperature:   temperature,
		retryConfig:   cfg.Gemini.Retry,
		promptManager: prompts.New(
			cfg.Prompts.ReviewPromptPath,
			cfg.Prompts.ContextGatheringPromptPath,
		),
		logger: logger,
	}, nil
}

// isRetryableError checks if the error is retryable (rate limit or server errors).
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Quota exhaustion is not retryable - should fallback to a different model instead.
	if isQuotaExhaustedError(err) {
		return false
	}

	// Check if it's a genai.APIError.
	var apiErr *genai.APIError
	if errors.As(err, &apiErr) {
		// Check HTTP status code for retryable errors.
		switch apiErr.Code {
		case http.StatusRequestTimeout, // 408
			http.StatusTooManyRequests,     // 429
			http.StatusInternalServerError, // 500
			http.StatusBadGateway,          // 502
			http.StatusServiceUnavailable,  // 503
			http.StatusGatewayTimeout:      // 504
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

// maxBackoffDuration returns the configured maximum backoff, falling back to
// 60s when unset or unparseable.
func maxBackoffDuration(retryConfig *config.RetryConfig) time.Duration {
	maxBackoff, err := time.ParseDuration(retryConfig.MaxBackoff)
	if err != nil || maxBackoff == 0 {
		return 60 * time.Second
	}

	return maxBackoff
}

// calculateBackoff calculates the exponential backoff with jitter.
func calculateBackoff(attempt int, retryConfig *config.RetryConfig) time.Duration {
	// Parse initial and max backoff.
	initialBackoff, err := time.ParseDuration(retryConfig.InitialBackoff)
	if err != nil || initialBackoff == 0 {
		initialBackoff = time.Second
	}

	maxBackoff := maxBackoffDuration(retryConfig)

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
	maxRetries := 0
	if r.retryConfig != nil && r.retryConfig.MaxRetries != nil {
		maxRetries = *r.retryConfig.MaxRetries
	}

	if r.retryConfig == nil || maxRetries <= 0 {
		// No retry configured, just run the operation once.
		err := operation()
		if err != nil && isQuotaExhaustedError(err) {
			return errors.Join(ErrQuotaExhausted, err)
		}
		return err
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
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
			// Wrap quota errors with sentinel for caller detection.
			if isQuotaExhaustedError(err) {
				return errors.Join(ErrQuotaExhausted, err)
			}
			return err // Non-retryable error.
		}

		// Don't retry if we've exhausted attempts.
		if attempt >= maxRetries {
			break
		}

		// Calculate backoff duration.
		var backoff time.Duration

		// First, check if the API provided a retry delay. Cap it at MaxBackoff
		// so a hostile or buggy server cannot pin us in a multi-minute (or
		// multi-hour) sleep with an oversized retryDelay.
		if apiDelay := extractRetryDelay(err); apiDelay > 0 {
			backoff = min(apiDelay, maxBackoffDuration(r.retryConfig))
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
			"max_attempts", maxRetries+1)
	}

	return fmt.Errorf("operation %s failed after %d attempts: %w", operationName, maxRetries+1, lastErr)
}

// modelSpend records the token usage attributed to a single model attempt.
// ReviewDiff accumulates one per model it tries so cost can be computed with
// each model's own pricing.
type modelSpend struct {
	model string
	usage tokenUsage
}

// applyAggregateSpend folds the spend from every attempted model onto result.
// Token counts are summed for the at-a-glance totals; cost and savings are
// computed per model, because a quota fallback runs a second model that may
// price differently from the primary. With a single attempt (the common case)
// this reproduces that model's own figures.
func applyAggregateSpend(result *Result, spends []modelSpend) {
	var combined tokenUsage
	var cost, savings float64
	var costKnown bool
	for _, s := range spends {
		combined.PromptTokens += s.usage.PromptTokens
		combined.CandidatesTokens += s.usage.CandidatesTokens
		combined.CachedTokens += s.usage.CachedTokens
		combined.ThoughtsTokens += s.usage.ThoughtsTokens
		combined.ToolUseTokens += s.usage.ToolUseTokens
		if c := s.usage.cost(s.model); c >= 0 {
			cost += c
			savings += s.usage.savings(s.model)
			costKnown = true
		}
	}

	result.TokenUsage = &TokenUsage{
		PromptTokens:     combined.PromptTokens,
		CandidatesTokens: combined.CandidatesTokens,
		TotalTokens:      combined.total(),
		CachedTokens:     combined.CachedTokens,
		ThoughtsTokens:   combined.ThoughtsTokens,
		ToolUseTokens:    combined.ToolUseTokens,
	}
	if costKnown {
		result.CostUSD = cost
		result.CacheSavingsUSD = savings
	} else {
		result.CostUSD = 0
		result.CacheSavingsUSD = 0
	}
}

// ReviewDiff performs a code review on the provided diff.
// If the primary model's quota is exhausted, it falls back to the fallback model.
func (r *Reviewer) ReviewDiff(
	ctx context.Context, diff string, changedFiles []string, repoPath string, opts ...Option,
) (*Result, error) {
	startTime := time.Now()

	options := &Options{}
	for _, opt := range opts {
		opt(options)
	}

	// Collect the token spend of every model attempted (primary plus any
	// fallback) so the reported cost and totals account for the full run, not
	// just the model that finally answered.
	var spends []modelSpend
	record := func(model string, usage tokenUsage) {
		spends = append(spends, modelSpend{model: model, usage: usage})
	}

	result, err := r.reviewDiffWithModel(ctx, diff, changedFiles, repoPath, r.modelName, options, record)

	// On quota exhaustion, try fallback model once. An empty fallback model
	// (possible on a hand-constructed Reviewer; config.Load defaults it)
	// means no fallback rather than a request with an empty model name.
	if errors.Is(err, ErrQuotaExhausted) && r.fallbackModel != "" &&
		r.fallbackModel != config.FallbackModelNone && r.fallbackModel != r.modelName {
		r.logger.Warn("Primary model quota exhausted, falling back",
			"primary_model", r.modelName,
			"fallback_model", r.fallbackModel)
		result, err = r.reviewDiffWithModel(ctx, diff, changedFiles, repoPath, r.fallbackModel, options, record)
	}

	// Fold the spend from every attempt onto the result and update duration to
	// reflect total wall-clock time including any fallback attempt.
	if result != nil {
		result.DurationMS = time.Since(startTime).Milliseconds()
		applyAggregateSpend(result, spends)
	}

	return result, err
}

// reviewDiffWithModel performs a code review using the specified model.
//
//nolint:maintidx // Complex multi-phase review process; refactoring would hurt readability.
func (r *Reviewer) reviewDiffWithModel(
	ctx context.Context, diff string, changedFiles []string, repoPath string, modelName string,
	opts *Options, recordSpend func(model string, usage tokenUsage),
) (*Result, error) {
	startTime := time.Now()
	// Validate inputs.
	if diff == "" {
		return nil, ErrEmptyDiff
	}

	// Track token usage across all API calls.
	usage := &tokenUsage{}

	// Log token usage when function exits (success or failure).
	defer func() {
		// Report this model's spend to the caller so ReviewDiff can aggregate
		// across the primary attempt and any fallback. This runs on every exit
		// path, so a model that consumed tokens before erroring (e.g. quota
		// exhausted mid-review) still has its spend counted.
		if recordSpend != nil {
			recordSpend(modelName, *usage)
		}
		if usage.total() > 0 {
			logArgs := []any{
				"prompt_tokens", usage.PromptTokens,
				"candidates_tokens", usage.CandidatesTokens,
				"total_tokens", usage.total(),
			}
			if usage.CachedTokens > 0 {
				logArgs = append(logArgs, "cached_tokens", usage.CachedTokens)
			}
			if usage.ThoughtsTokens > 0 {
				logArgs = append(logArgs, "thoughts_tokens", usage.ThoughtsTokens)
			}
			if usage.ToolUseTokens > 0 {
				logArgs = append(logArgs, "tool_use_tokens", usage.ToolUseTokens)
			}
			if cost := usage.cost(modelName); cost >= 0 {
				logArgs = append(logArgs, "cost_usd", cost,
					"cost_usd_uncached", usage.costWithoutCaching(modelName),
					"cache_savings_usd", usage.savings(modelName),
					"cache_hit_rate", usage.cacheHitRate(),
					"cache_engaged", usage.CachedTokens > 0)
			}
			r.logger.Info("Token usage", logArgs...)

			// Plain-language caching verdict so "did it work / are we saving
			// money" is answerable with a single grep (msg="Context caching").
			if usage.CachedTokens == 0 {
				r.logger.Info("Context caching",
					"engaged", false,
					"reason", "no cached tokens (prompt below model minimum, prefix changed, or no implicit cache)",
					"prompt_tokens", usage.PromptTokens,
					"model", modelName)
			} else {
				r.logger.Info("Context caching",
					"engaged", true,
					"cached_tokens", usage.CachedTokens,
					"prompt_tokens", usage.PromptTokens,
					"hit_rate", usage.cacheHitRate(),
					"saved_usd", usage.savings(modelName),
					"model", modelName)
			}
		}
	}()

	deletedSet := make(map[string]bool, len(opts.DeletedFiles))
	for _, p := range opts.DeletedFiles {
		deletedSet[filepath.Clean(p)] = true
	}

	// Phase 1: Let Gemini analyze the code with tool support for file retrieval.
	contextPrompt, err := r.promptManager.BuildContextGatheringPrompt(
		diff, changedFiles, opts.DeletedFiles, opts.Instructions,
	)
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
	chat, err := r.client.CreateChat(ctx, modelName, toolConfig)
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
	usage.addFromResponse(response)

	// Handle function calls. The loop is bounded: nothing upstream applies a
	// deadline, so without a cap a model that keeps requesting files would
	// fetch (and bill) forever. On hitting the cap we proceed to the
	// structured review phase with the context gathered so far.
	for turn := 0; response != nil && len(response.Candidates) > 0; turn++ {
		if turn >= maxToolTurns {
			r.logger.Warn("Tool-calling turn limit reached; proceeding to review",
				"max_turns", maxToolTurns)
			break
		}

		candidate := response.Candidates[0]

		// A candidate can arrive with no Content (e.g. blocked by safety
		// filters or stopped on MAX_TOKENS), in which case there is nothing
		// to act on; fall through to the structured review phase.
		if candidate.Content == nil {
			break
		}

		// The model may make several function calls in one turn (parallel
		// function calling). The API requires exactly one response part per
		// call, so collect a response for each before replying.
		var funcResponses []genai.Part
		for _, part := range candidate.Content.Parts {
			switch {
			case part.FunctionCall != nil:
				requestedFile, ok := part.FunctionCall.Args["filepath"].(string)
				r.logger.Debug("Model requested file",
					"function", part.FunctionCall.Name,
					"filepath", requestedFile)

				// Invoke file fetch callback if provided.
				if opts.FileFetchCallback != nil && ok && requestedFile != "" {
					opts.FileFetchCallback(requestedFile)
				}

				funcResponses = append(funcResponses,
					*r.handleFileRetrieval(ctx, part.FunctionCall, repoPath, deletedSet))
			case part.Text != "" && !part.Thought:
				// Capture any analysis text from the model. Thought-summary
				// parts also carry text but are reasoning, not analysis, so
				// they fall through to the default case.
				analysisText = part.Text
			default:
				// Other part kinds (inline data, thought summaries) need no action.
			}
		}

		// If no tool calls, we have the analysis response.
		if len(funcResponses) == 0 {
			break
		}

		// Send the function responses back with retry logic.
		r.logger.Debug("Sending function responses", "count", len(funcResponses))

		err = r.retryableOperation(ctx, func() error {
			var sendErr error
			response, sendErr = chat.SendMessage(ctx, funcResponses...)

			return sendErr
		}, "function_response")
		if err != nil {
			return nil, fmt.Errorf("failed to send function response: %w", err)
		}
		usage.addFromResponse(response)
	}

	// Phase 2: Get structured review result without tools.
	reviewPrompt, err := r.promptManager.BuildReviewPrompt(
		diff, changedFiles, opts.DeletedFiles, analysisText, opts.Instructions,
	)
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
		reviewResponse, sendErr = r.client.GenerateContent(ctx, modelName, reviewContent, jsonConfig)

		return sendErr
	}, "review_prompt")
	if err != nil {
		return nil, fmt.Errorf("failed to get review response: %w", err)
	}
	usage.addFromResponse(reviewResponse)

	// Parse the structured response.
	if reviewResponse == nil || len(reviewResponse.Candidates) == 0 {
		return nil, ErrNoResponse
	}

	candidate := reviewResponse.Candidates[0]
	if candidate.Content == nil {
		return nil, ErrEmptyResponse
	}
	for _, part := range candidate.Content.Parts {
		// Skip thought-summary parts: they carry text but are reasoning, not
		// the structured JSON verdict, and would fail to parse below.
		if part.Text != "" && !part.Thought {
			// Log the raw response for debugging.
			r.logger.Debug("Raw review response from Gemini", "text", part.Text)

			// Parse the JSON response.
			var result Result
			if err := json.Unmarshal([]byte(part.Text), &result); err != nil {
				return nil, fmt.Errorf("failed to parse review response: %w", err)
			}

			// Add usage statistics to result.
			result.DurationMS = time.Since(startTime).Milliseconds()
			result.Model = modelName
			result.TokenUsage = &TokenUsage{
				PromptTokens:     usage.PromptTokens,
				CandidatesTokens: usage.CandidatesTokens,
				TotalTokens:      usage.total(),
				CachedTokens:     usage.CachedTokens,
				ThoughtsTokens:   usage.ThoughtsTokens,
				ToolUseTokens:    usage.ToolUseTokens,
			}
			if cost := usage.cost(modelName); cost >= 0 {
				result.CostUSD = cost
				result.CacheSavingsUSD = usage.savings(modelName)
			}

			return &result, nil
		}
	}

	return nil, ErrEmptyResponse
}

// handleFileRetrieval handles file retrieval tool calls from Gemini. The
// deleted set contains paths the caller has identified as deletions in the
// diff under review; requests for those paths return a clear deleted-file
// response instead of attempting to open the (now-missing) file.
func (*Reviewer) handleFileRetrieval(
	ctx context.Context, funcCall *genai.FunctionCall, repoPath string, deleted map[string]bool,
) *genai.Part {
	// Extract the filepath argument.
	requestedPath, ok := funcCall.Args["filepath"].(string)
	if !ok {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				errorKey: "filepath parameter must be a string",
			},
		)
	}

	// Short-circuit deletions so the model gets a deletion-specific message
	// instead of a generic ENOENT it might think is transient.
	if deleted[filepath.Clean(requestedPath)] {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{errorKey: errDeletedFileMsg},
		)
	}

	// Validate and resolve the file path.
	if strings.Contains(requestedPath, "..") {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				errorKey: "invalid filepath: path traversal not allowed",
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
				errorKey: fmt.Sprintf("failed to resolve path: %v", err),
			},
		)
	}

	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				errorKey: fmt.Sprintf("failed to resolve repository path: %v", err),
			},
		)
	}

	// First check if the path (before symlink resolution) is within the repository
	// This handles the case where the file doesn't exist yet.
	if !strings.HasPrefix(absPath, absRepoPath+string(filepath.Separator)) && absPath != absRepoPath {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				errorKey: "access denied: path is outside repository",
			},
		)
	}

	// SECURITY CHECK: Check if file is gitignored
	isIgnored, err := git.IsIgnored(ctx, repoPath, requestedPath)
	if err != nil {
		// Fail closed on any error for security
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				errorKey: fmt.Sprintf("access denied: unable to verify gitignore status: %v", err),
			},
		)
	}
	if isIgnored {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				errorKey: "access denied: file is gitignored",
			},
		)
	}

	// git check-ignore matches the requested name only, while the rooted open
	// below follows in-repo symlinks. Without this re-check, a symlink such as
	// "config-link -> .env" (link not ignored, target ignored) would launder
	// gitignored content past the check above. Resolve the path and, when it
	// differs from the requested one, run check-ignore on the target too.
	// Resolution errors (e.g. nonexistent file, dangling symlink) fall through
	// to the open, which fails with the clearer ENOENT-style message. Like the
	// check above this is best-effort against races; os.Root still guarantees
	// the escape property at open time.
	if resolved, evalErr := filepath.EvalSymlinks(absPath); evalErr == nil {
		// Resolve the repo root too: on systems where the repo path itself
		// contains symlinked components (e.g. /tmp on macOS), Rel against the
		// unresolved root would misreport every file as outside the repo.
		resolvedRepo, repoErr := filepath.EvalSymlinks(absRepoPath)
		if repoErr != nil {
			return genai.NewPartFromFunctionResponse(
				funcCall.Name,
				map[string]any{
					errorKey: fmt.Sprintf("failed to resolve repository path: %v", repoErr),
				},
			)
		}
		relResolved, relErr := filepath.Rel(resolvedRepo, resolved)
		if relErr != nil || relResolved == ".." ||
			strings.HasPrefix(relResolved, ".."+string(filepath.Separator)) {
			return genai.NewPartFromFunctionResponse(
				funcCall.Name,
				map[string]any{
					errorKey: "access denied: path is outside repository",
				},
			)
		}
		if relResolved != filepath.Clean(requestedPath) {
			targetIgnored, targetErr := git.IsIgnored(ctx, repoPath, relResolved)
			if targetErr != nil {
				// Fail closed on any error for security.
				return genai.NewPartFromFunctionResponse(
					funcCall.Name,
					map[string]any{
						errorKey: fmt.Sprintf(
							"access denied: unable to verify gitignore status: %v", targetErr,
						),
					},
				)
			}
			if targetIgnored {
				return genai.NewPartFromFunctionResponse(
					funcCall.Name,
					map[string]any{
						errorKey: "access denied: file is gitignored",
					},
				)
			}
		}
	}

	// TOCTOU defense: route the open through os.Root, which uses openat
	// (Linux) or equivalent on other platforms to validate every path
	// component against the repository directory atomically. Root rejects
	// any path whose resolution escapes the root, including via parent
	// symlinks swapped under us, closing the entire class of races where
	// EvalSymlinks-and-then-open could be tricked. We still pass O_NONBLOCK
	// so that a planted FIFO or slow device cannot hang the review, and
	// verify after open that f.Stat() reports a regular file (Root permits
	// /proc files, FIFOs and device nodes that happen to live inside the
	// repo, which we never want to read).
	root, err := os.OpenRoot(repoPath)
	if err != nil {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				errorKey: fmt.Sprintf("failed to open repository: %v", err),
			},
		)
	}
	defer root.Close() //nolint:errcheck // read-only handle, close error is inconsequential

	relPath := filepath.Clean(requestedPath)
	relPath = strings.TrimPrefix(relPath, string(filepath.Separator))
	f, err := root.OpenFile(relPath, os.O_RDONLY|openNonblockFlag, 0)
	if err != nil {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				errorKey: fmt.Sprintf("failed to read file: %v", err),
			},
		)
	}
	defer f.Close() //nolint:errcheck // read-only file, close error is inconsequential

	openedInfo, err := f.Stat()
	if err != nil {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				errorKey: fmt.Sprintf("failed to stat opened file: %v", err),
			},
		)
	}
	if !openedInfo.Mode().IsRegular() {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				errorKey: "access denied: not a regular file",
			},
		)
	}

	// Bound the read so an attacker (or a runaway request from the model)
	// cannot OOM the review process by asking for a multi-gigabyte file.
	// We read maxRetrievedFileSize+1 so we can distinguish "exactly fits"
	// from "overflows" after ReadAll returns.
	limited := io.LimitReader(f, maxRetrievedFileSize+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				errorKey: fmt.Sprintf("failed to read file: %v", err),
			},
		)
	}
	if int64(len(content)) > maxRetrievedFileSize {
		return genai.NewPartFromFunctionResponse(
			funcCall.Name,
			map[string]any{
				errorKey: fmt.Sprintf("file too large: exceeds %d bytes", maxRetrievedFileSize),
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
