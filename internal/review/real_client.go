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
