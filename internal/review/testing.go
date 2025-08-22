package review

import (
	"context"
	"os"

	"github.com/shields/lgtmcp/internal/prompts"
	"google.golang.org/genai"
)

// IsTestMode returns true if we're running in test mode (no external API calls).
func IsTestMode() bool {
	return os.Getenv("LGTMCP_TEST_MODE") == "true"
}

// NewForTesting creates a Reviewer with a stub client for testing.
func NewForTesting() *Reviewer {
	return &Reviewer{
		client:        newDefaultStubClient(),
		modelName:     "gemini-2.5-pro",
		temperature:   0.2,
		promptManager: prompts.New("", ""),
	}
}

// newDefaultStubClient creates a stub client with sensible defaults.
func newDefaultStubClient() GeminiClient {
	return &StubGeminiClient{
		CreateChatFunc: func(_ context.Context, _ string, _ *genai.GenerateContentConfig) (GeminiChat, error) {
			return &StubGeminiChat{
				SendMessageFunc: func(_ context.Context, _ genai.Part) (*genai.GenerateContentResponse, error) {
					// Return analysis text for phase 1.
					return &genai.GenerateContentResponse{
						Candidates: []*genai.Candidate{
							{
								Content: &genai.Content{
									Parts: []*genai.Part{
										{
											Text: "Analysis complete. Code looks good.",
										},
									},
								},
							},
						},
					}, nil
				},
			}, nil
		},
		GenerateContentFunc: func(_ context.Context, _ string, _ []*genai.Content,
			_ *genai.GenerateContentConfig,
		) (*genai.GenerateContentResponse, error) {
			// Default: approve with generic message for phase 2.
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: &genai.Content{
							Parts: []*genai.Part{
								{
									Text: `{"lgtm": true, "comments": "Test review - changes look good"}`,
								},
							},
						},
					},
				},
			}, nil
		},
	}
}

// WithStubResponse creates a Reviewer that returns a specific response.
//
//nolint:revive // lgtm is not a control flag, it's part of the response data
func WithStubResponse(lgtm bool, comments string) *Reviewer {
	lgtmStr := "false"
	if lgtm {
		lgtmStr = "true"
	}
	responseJSON := `{"lgtm": ` + lgtmStr + `, "comments": "` + comments + `"}`

	return &Reviewer{
		client: &StubGeminiClient{
			CreateChatFunc: func(_ context.Context, _ string, _ *genai.GenerateContentConfig) (GeminiChat, error) {
				return &StubGeminiChat{
					SendMessageFunc: func(_ context.Context, _ genai.Part) (*genai.GenerateContentResponse, error) {
						// Return analysis text for phase 1.
						return &genai.GenerateContentResponse{
							Candidates: []*genai.Candidate{
								{
									Content: &genai.Content{
										Parts: []*genai.Part{
											{
												Text: "Analysis complete for testing.",
											},
										},
									},
								},
							},
						}, nil
					},
				}, nil
			},
			GenerateContentFunc: func(_ context.Context, _ string, _ []*genai.Content,
				_ *genai.GenerateContentConfig,
			) (*genai.GenerateContentResponse, error) {
				// Return the specified response for phase 2.
				return &genai.GenerateContentResponse{
					Candidates: []*genai.Candidate{
						{
							Content: &genai.Content{
								Parts: []*genai.Part{
									{
										Text: responseJSON,
									},
								},
							},
						},
					},
				}, nil
			},
		},
		modelName:     "gemini-2.5-pro",
		temperature:   0.2,
		retryConfig:   nil, // No retry for testing by default.
		promptManager: prompts.New("", ""),
	}
}
