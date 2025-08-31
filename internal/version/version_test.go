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

package version_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/shields/lgtmcp/internal/version"
)

func TestString(t *testing.T) { //nolint:paralleltest // Modifies global Version variable
	// Save original version
	originalVersion := version.Version
	t.Cleanup(func() {
		version.Version = originalVersion
	})

	tests := []struct {
		name             string
		version          string
		expectedContains []string
	}{
		{
			name:    "default dev version",
			version: "dev",
			expectedContains: []string{
				"lgtmcp version dev",
				runtime.GOOS + "/" + runtime.GOARCH,
			},
		},
		{
			name:    "custom version",
			version: "1.2.3",
			expectedContains: []string{
				"lgtmcp version 1.2.3",
				runtime.GOOS + "/" + runtime.GOARCH,
			},
		},
	}

	for _, tt := range tests { //nolint:paralleltest // Cannot parallelize due to global state
		t.Run(tt.name, func(t *testing.T) {
			version.Version = tt.version
			result := version.String()

			for _, expected := range tt.expectedContains {
				if !strings.Contains(result, expected) {
					t.Errorf("String() = %q, want it to contain %q", result, expected)
				}
			}

			// Should have commit info (either from git or "unknown")
			if !strings.Contains(result, "(") || !strings.Contains(result, ")") {
				t.Errorf("String() = %q, want it to have commit info in parentheses", result)
			}
		})
	}
}

func TestDetailedString(t *testing.T) { //nolint:paralleltest // Modifies global Version variable
	// Save original version
	originalVersion := version.Version
	t.Cleanup(func() {
		version.Version = originalVersion
	})

	tests := []struct {
		name             string
		version          string
		expectedContains []string
	}{
		{
			name:    "default dev version",
			version: "dev",
			expectedContains: []string{
				"lgtmcp version dev",
				"Go:",
				"OS/Arch:",
				runtime.Version(),
				runtime.GOOS + "/" + runtime.GOARCH,
			},
		},
		{
			name:    "custom version",
			version: "2.0.0-beta",
			expectedContains: []string{
				"lgtmcp version 2.0.0-beta",
				"Go:",
				"OS/Arch:",
			},
		},
	}

	for _, tt := range tests { //nolint:paralleltest // Cannot parallelize due to global state
		t.Run(tt.name, func(t *testing.T) {
			version.Version = tt.version
			result := version.DetailedString()

			for _, expected := range tt.expectedContains {
				if !strings.Contains(result, expected) {
					t.Errorf("DetailedString() = %q, want it to contain %q", result, expected)
				}
			}

			// Should be multi-line
			lines := strings.Split(result, "\n")
			if len(lines) < 3 {
				t.Errorf("DetailedString() should return multiple lines, got %d lines", len(lines))
			}
		})
	}
}

func TestVersionVariable(t *testing.T) { //nolint:paralleltest // Reads global Version variable
	// Default should be "dev"
	if version.Version != "dev" {
		t.Errorf("Version = %q, want %q", version.Version, "dev")
	}
}
