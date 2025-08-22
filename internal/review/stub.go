package review

import (
	"context"

	"google.golang.org/genai"
)

// StubGeminiClient is a stub implementation of GeminiClient for testing.
type StubGeminiClient struct {
	CreateChatFunc func(ctx context.Context, modelName string,
		config *genai.GenerateContentConfig) (GeminiChat, error)
	GenerateContentFunc func(ctx context.Context, modelName string,
		contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
}

// CreateChat implements GeminiClient.
func (s *StubGeminiClient) CreateChat(
	ctx context.Context, modelName string, config *genai.GenerateContentConfig,
) (GeminiChat, error) {
	if s.CreateChatFunc != nil {
		return s.CreateChatFunc(ctx, modelName, config)
	}

	return &StubGeminiChat{}, nil
}

// GenerateContent implements GeminiClient.
func (s *StubGeminiClient) GenerateContent(
	ctx context.Context, modelName string, contents []*genai.Content, genConfig *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error) {
	if s.GenerateContentFunc != nil {
		return s.GenerateContentFunc(ctx, modelName, contents, genConfig)
	}

	// Default response for testing.
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
}

// StubGeminiChat is a stub implementation of GeminiChat for testing.
type StubGeminiChat struct {
	SendMessageFunc func(ctx context.Context, part genai.Part) (*genai.GenerateContentResponse, error)
}

// SendMessage implements GeminiChat.
func (s *StubGeminiChat) SendMessage(ctx context.Context, part genai.Part) (*genai.GenerateContentResponse, error) {
	if s.SendMessageFunc != nil {
		return s.SendMessageFunc(ctx, part)
	}

	// Default response for testing.
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
}
