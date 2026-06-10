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
	"encoding/json"
	"os"

	"google.golang.org/genai"
	"msrl.dev/lgtmcp/internal/logging"
	"msrl.dev/lgtmcp/internal/prompts"
)

// IsTestMode returns true if we're running in test mode (no external API calls).
func IsTestMode() bool {
	return os.Getenv("LGTMCP_TEST_MODE") == "true"
}

// NewForTesting creates a Reviewer with a stub client for testing.
func NewForTesting() *Reviewer {
	logger, err := logging.New(logging.Config{Output: "none"})
	if err != nil {
		// This should never happen with "none" output.
		panic(err)
	}
	return &Reviewer{
		client:        newDefaultStubClient(),
		modelName:     defaultModel,
		temperature:   0.2,
		promptManager: prompts.New("", ""),
		logger:        logger,
	}
}

// newDefaultStubClient creates a stub client with sensible defaults.
func newDefaultStubClient() GeminiClient {
	return newStubClient("Analysis complete. Code looks good.", stubReviewJSON)
}

// newStubClient builds a StubGeminiClient that returns the given phase-1 analysis
// text and phase-2 review JSON.
func newStubClient(analysisText, reviewJSON string) *StubGeminiClient {
	textResp := func(text string) *genai.GenerateContentResponse {
		return &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{{Text: text}},
					},
				},
			},
		}
	}
	return &StubGeminiClient{
		CreateChatFunc: func(_ context.Context, _ string, _ *genai.GenerateContentConfig) (GeminiChat, error) {
			return &StubGeminiChat{
				SendMessageFunc: func(_ context.Context, _ ...genai.Part) (*genai.GenerateContentResponse, error) {
					return textResp(analysisText), nil
				},
			}, nil
		},
		GenerateContentFunc: func(_ context.Context, _ string, _ []*genai.Content,
			_ *genai.GenerateContentConfig,
		) (*genai.GenerateContentResponse, error) {
			return textResp(reviewJSON), nil
		},
	}
}

// WithStubResponse creates a Reviewer that returns a specific response.
func WithStubResponse(lgtm bool, comments string) *Reviewer {
	// Marshal rather than concatenate so comments containing quotes or
	// backslashes still produce valid JSON.
	responseJSON, err := json.Marshal(Result{LGTM: lgtm, Comments: comments})
	if err != nil {
		// Marshaling a plain struct of bool and string cannot fail.
		panic(err)
	}

	logger, err := logging.New(logging.Config{Output: "none"})
	if err != nil {
		// This should never happen with "none" output.
		panic(err)
	}
	return &Reviewer{
		client:        newStubClient("Analysis complete for testing.", string(responseJSON)),
		modelName:     defaultModel,
		temperature:   0.2,
		retryConfig:   nil, // No retry for testing by default.
		promptManager: prompts.New("", ""),
		logger:        logger,
	}
}
