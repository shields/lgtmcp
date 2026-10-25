// Copyright © 2025-2026 Michael Shields
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
	"encoding/json/v2"

	"google.golang.org/genai"
	"msrl.dev/lgtmcp/internal/config"
	"msrl.dev/lgtmcp/internal/logging"
	"msrl.dev/lgtmcp/internal/prompts"
)

// NewForTesting creates a Reviewer with a stub client for testing.
func NewForTesting() *Reviewer {
	logger, err := logging.New(logging.Config{Output: "none"})
	if err != nil {
		// This should never happen with "none" output.
		panic(err)
	}
	return &Reviewer{
		client:        &StubGeminiClient{},
		modelName:     config.DefaultModel,
		thinkingLevel: thinkingLevelFromConfig(config.DefaultThinkingLevel),
		promptManager: prompts.New(""),
		logger:        logger,
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

	return NewForTestingWithClient(&StubGeminiClient{
		GenerateContentFunc: func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig,
		) (*genai.GenerateContentResponse, error) {
			return textResponse(string(responseJSON)), nil
		},
	})
}

// NewForTestingWithClient creates a Reviewer like NewForTesting that sends its
// requests to client.
func NewForTestingWithClient(client GeminiClient) *Reviewer {
	r := NewForTesting()
	r.client = client

	return r
}
