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
