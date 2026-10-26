// Copyright © 2026 Michael Shields
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
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"msrl.dev/lgtmcp/internal/config"
)

type fakeAPIResponse struct {
	body   string
	status int
}

const (
	successBody = `{"candidates":[{"content":{"role":"model","parts":[{"text":` +
		`"{\"lgtm\": true, \"comments\": \"fine\"}"}]}}]}`

	quotaBody = `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota exceeded","details":[` +
		`{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaMetric":"requests"}]},` +
		`{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"30ms"}]}}`

	rateLimitBody = `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"slow down","details":[` +
		`{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"30ms"}]}}`

	unavailableBody = `{"error":{"code":503,"status":"UNAVAILABLE","message":"overloaded"}}`

	badRequestBody = `{"error":{"code":400,"status":"INVALID_ARGUMENT",` +
		`"message":"model reported UNAVAILABLE and INTERNAL and RESOURCE_EXHAUSTED"}}`
)

func sdkReviewer(t *testing.T, responses ...fakeAPIResponse) (*Reviewer, *atomic.Int32) {
	t.Helper()

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(requests.Add(1)) - 1
		resp := responses[min(n, len(responses)-1)]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.status)
		if _, err := w.Write([]byte(resp.body)); err != nil {
			t.Errorf("writing fake API response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	client, err := genai.NewClient(t.Context(), &genai.ClientConfig{
		APIKey:      "test-key",
		Backend:     genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
	})
	require.NoError(t, err)

	r := newTestReviewer(client.Models)
	r.retryConfig = &config.RetryConfig{
		MaxRetries:        new(2),
		InitialBackoff:    "1ms",
		MaxBackoff:        "5ms",
		BackoffMultiplier: 1,
	}

	return r, &requests
}

func sdkError(t *testing.T, resp fakeAPIResponse) error {
	t.Helper()

	r, _ := sdkReviewer(t, resp)
	_, err := r.client.GenerateContent(t.Context(), "test-model", genai.Text("hi"), nil)
	require.Error(t, err)

	return err
}

func TestSDKErrors_Classification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		resp      fakeAPIResponse
		retryable bool
		quota     bool
		delay     time.Duration
	}{
		{
			name:  "quota exhausted",
			resp:  fakeAPIResponse{status: http.StatusTooManyRequests, body: quotaBody},
			quota: true,
			delay: 30 * time.Millisecond,
		},
		{
			name:  "quota exhausted with a body the SDK cannot decode",
			resp:  fakeAPIResponse{status: http.StatusTooManyRequests, body: "[" + quotaBody + "]"},
			quota: true,
		},
		{
			name:      "rate limited",
			resp:      fakeAPIResponse{status: http.StatusTooManyRequests, body: rateLimitBody},
			retryable: true,
			delay:     30 * time.Millisecond,
		},
		{
			name:      "service unavailable",
			resp:      fakeAPIResponse{status: http.StatusServiceUnavailable, body: unavailableBody},
			retryable: true,
		},
		{
			name: "bad request mentioning retryable statuses",
			resp: fakeAPIResponse{status: http.StatusBadRequest, body: badRequestBody},
		},
		{
			name:      "plain text gateway error",
			resp:      fakeAPIResponse{status: http.StatusBadGateway, body: "upstream down"},
			retryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sdkError(t, tt.resp)

			apiErr, isValue := asAPIError(err)
			require.True(t, isValue, "the SDK must return genai.APIError by value, got %T", err)
			assert.Equal(t, tt.resp.status, apiErr.Code)
			assert.Equal(t, tt.quota, isQuotaExhaustedError(err))
			assert.Equal(t, tt.retryable, isRetryableError(err))
			assert.Equal(t, tt.delay, extractRetryDelay(err))
		})
	}
}

func TestSDKErrors_ReviewDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		responses    []fakeAPIResponse
		wantQuota    bool
		wantErr      bool
		wantRequests int32
	}{
		{
			name: "quota exhaustion is not retried",
			responses: []fakeAPIResponse{
				{status: http.StatusTooManyRequests, body: quotaBody},
			},
			wantQuota:    true,
			wantErr:      true,
			wantRequests: 1,
		},
		{
			name: "service unavailable is retried",
			responses: []fakeAPIResponse{
				{status: http.StatusServiceUnavailable, body: unavailableBody},
				{status: http.StatusOK, body: successBody},
			},
			wantRequests: 2,
		},
		{
			name: "rate limit with retry delay is retried",
			responses: []fakeAPIResponse{
				{status: http.StatusTooManyRequests, body: rateLimitBody},
				{status: http.StatusOK, body: successBody},
			},
			wantRequests: 2,
		},
		{
			name: "bad request mentioning retryable statuses is not retried",
			responses: []fakeAPIResponse{
				{status: http.StatusBadRequest, body: badRequestBody},
				{status: http.StatusOK, body: successBody},
			},
			wantErr:      true,
			wantRequests: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, requests := sdkReviewer(t, tt.responses...)

			result, err := r.ReviewDiff(t.Context(), "diff content", []string{"a.go"}, t.TempDir())

			assert.Equal(t, tt.wantRequests, requests.Load())
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, result)
				assert.Equal(t, tt.wantQuota, errors.Is(err, ErrQuotaExhausted))

				return
			}
			require.NoError(t, err)
			assert.True(t, result.LGTM)
		})
	}
}
