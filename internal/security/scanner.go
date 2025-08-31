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

package security

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/report"
)

// ErrCustomConfigNotSupported indicates that custom gitleaks config is not supported.
var ErrCustomConfigNotSupported = errors.New("custom config not supported in this version")

// detectorMutex protects the creation of new detectors to avoid race conditions
// in the gitleaks library which uses a global viper instance.
var detectorMutex sync.Mutex

// Scanner provides secret detection capabilities using gitleaks.
type Scanner struct {
	detector *detect.Detector
}

// New creates a new Scanner with optional custom configuration.
func New(configPath string) (*Scanner, error) {
	if configPath != "" {
		// For custom config, we need to load it ourselves.
		// The v8 API doesn't have a LoadConfig function exposed.
		// So we'll use the default and note this limitation.
		return nil, ErrCustomConfigNotSupported
	}

	// Synchronize detector creation to avoid race conditions in gitleaks.
	// The gitleaks library uses a global viper instance which causes races
	// when multiple detectors are created concurrently.
	detectorMutex.Lock()
	detector, err := detect.NewDetectorDefaultConfig()
	detectorMutex.Unlock()

	if err != nil {
		return nil, fmt.Errorf("failed to create detector with default config: %w", err)
	}

	detector.FollowSymlinks = false // Never follow symlinks for security.

	return &Scanner{
		detector: detector,
	}, nil
}

// ScanDiff scans a git diff for secrets by extracting changed files.
// Note: This method extracts file paths from the diff and scans the actual files
// rather than scanning the diff directly, as gitleaks v8 doesn't reliably
// detect secrets in diff format when used as a library.
func (s *Scanner) ScanDiff(
	_ context.Context,
	diff string,
	getFileContent func(path string) (string, error),
) ([]report.Finding, error) {
	if diff == "" {
		return nil, nil
	}

	// Parse the diff to extract changed files.
	changedFiles := ExtractChangedFiles(diff)

	var allFindings []report.Finding
	for _, file := range changedFiles {
		// Skip go.sum as it contains checksums/hashes that sometimes
		// trigger false positives for API key detection.
		if file == "go.sum" {
			continue
		}

		content, err := getFileContent(file)
		if err != nil {
			// Skip files that can't be read (deleted files, etc.).
			continue
		}

		findings := s.detector.DetectString(content)
		for i := range findings {
			findings[i].File = file
		}
		allFindings = append(allFindings, findings...)
	}

	return allFindings, nil
}

// ExtractChangedFiles parses a git diff and returns the list of changed files.
func ExtractChangedFiles(diff string) []string {
	var files []string
	seen := make(map[string]bool)

	lines := strings.Split(diff, "\n")
	for _, line := range lines {
		// Look for diff headers like "diff --git a/file.txt b/file.txt".
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				// Extract the b/ file path (the new version).
				file := strings.TrimPrefix(parts[3], "b/")
				if !seen[file] {
					files = append(files, file)
					seen[file] = true
				}
			}
		}
	}

	return files
}

// ScanContent scans arbitrary content for secrets.
func (s *Scanner) ScanContent(_ context.Context, content, filename string) ([]report.Finding, error) {
	if content == "" {
		return nil, nil
	}

	// Use DetectString to scan the content.
	findings := s.detector.DetectString(content)

	// Filter findings to only include those relevant to the given filename if provided.
	if filename != "" {
		var filtered []report.Finding
		for _, finding := range findings {
			// Update the file path in the finding to match the provided filename.
			finding.File = filename
			filtered = append(filtered, finding)
		}

		return filtered, nil
	}

	return findings, nil
}

// FormatFindings formats findings into a human-readable string.
func FormatFindings(findings []report.Finding) string {
	if len(findings) == 0 {
		return ""
	}

	var sb strings.Builder
	_, _ = sb.WriteString(fmt.Sprintf("🚨 Found %d potential secret(s):\n\n", len(findings)))

	for i, finding := range findings {
		_, _ = sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, finding.Description))
		_, _ = sb.WriteString(fmt.Sprintf("   File: %s\n", finding.File))
		if finding.StartLine > 0 {
			_, _ = sb.WriteString(fmt.Sprintf("   Line: %d\n", finding.StartLine))
		}
		_, _ = sb.WriteString(fmt.Sprintf("   Rule: %s\n", finding.RuleID))
		if finding.Secret != "" {
			// Redact most of the secret for safety.
			redacted := redactSecret(finding.Secret)
			_, _ = sb.WriteString(fmt.Sprintf("   Secret: %s\n", redacted))
		}
		if finding.Commit != "" {
			_, _ = sb.WriteString(fmt.Sprintf("   Commit: %s\n", finding.Commit))
		}
		_, _ = sb.WriteString("\n")
	}

	return sb.String()
}

// redactSecret redacts most of a secret, showing only first and last few characters.
func redactSecret(secret string) string {
	if len(secret) <= 8 {
		return "***"
	}

	// Show first 3 and last 3 characters.
	return fmt.Sprintf("%s...%s", secret[:3], secret[len(secret)-3:])
}

// HasFindings returns true if there are any findings.
func HasFindings(findings []report.Finding) bool {
	return len(findings) > 0
}
