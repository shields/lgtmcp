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
	stdpath "path"
	"strconv"
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
		// Skip go.sum and go.work.sum files as they may contain
		// checksums/hashes that trigger false positives for API key
		// detection. Use path.Base (not filepath.Base) since git diffs
		// always use forward slashes.
		if base := stdpath.Base(file); base == "go.sum" || base == "go.work.sum" {
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
// It handles paths containing spaces, renames and copies (whose authoritative
// destination comes from "rename to"/"copy to" lines), CRLF line endings, the
// diff.noprefix git config, and C-style quoted paths used by git for non-ASCII
// or otherwise special characters.
func ExtractChangedFiles(diff string) []string {
	var files []string
	seen := make(map[string]bool)
	addFile := func(file string) {
		if file != "" && !seen[file] {
			files = append(files, file)
			seen[file] = true
		}
	}

	// pending holds the best-effort path from the most recent "diff --git"
	// header. It is committed on the next header or at end of stream, and
	// may be overridden by an authoritative "rename to"/"copy to" line.
	var pending string
	for rawLine := range strings.SplitSeq(diff, "\n") {
		line := strings.TrimSuffix(rawLine, "\r")
		if strings.HasPrefix(line, "diff --git ") {
			addFile(pending)
			pending = parseGitDiffHeader(line)

			continue
		}
		if path, ok := strings.CutPrefix(line, "rename to "); ok {
			pending = unquoteIfQuoted(path)
		} else if path, ok := strings.CutPrefix(line, "copy to "); ok {
			pending = unquoteIfQuoted(path)
		}
	}
	addFile(pending)

	return files
}

// parseGitDiffHeader extracts the destination file path from a "diff --git"
// header. It handles three forms - C-quoted, standard prefixed
// ("a/<p> b/<p>"), and noprefix ("<p> <p>", used when diff.noprefix is set) -
// preferring whichever interpretation produces unambiguously equal halves.
// For mismatched halves (e.g., "git diff file1 file2") it falls back to the
// destination path after the first separator. Renames and copies should be
// reconciled by the caller using subsequent "rename to"/"copy to" lines,
// which override the value here.
func parseGitDiffHeader(line string) string {
	rest, ok := strings.CutPrefix(line, "diff --git ")
	if !ok {
		return ""
	}
	if strings.HasPrefix(rest, `"`) {
		return parseQuotedDiffHeader(rest)
	}
	// Prefer unambiguous interpretations: a noprefix form whose halves are
	// literally equal cannot also be a valid prefixed form (the latter has
	// "a/" on the left and "b/" on the right, so the halves differ).
	if path, ok := splitDiffPathsExact(rest, " "); ok {
		return path
	}
	if afterA, ok := strings.CutPrefix(rest, "a/"); ok {
		if path, ok := splitDiffPathsExact(afterA, " b/"); ok {
			return path
		}
	}
	// Mismatched halves: try the prefixed fallback first since unquoted
	// "diff --git" headers normally use the "a/"/"b/" prefixes.
	if afterA, ok := strings.CutPrefix(rest, "a/"); ok {
		if path := splitDiffPathsFallback(afterA, " b/"); path != "" {
			return path
		}
	}

	return splitDiffPathsFallback(rest, " ")
}

// parseQuotedDiffHeader handles a "diff --git" header where both paths are
// C-style quoted (used by git when paths contain control characters, double
// quotes, backslashes, or - when core.quotePath is true - high-bit bytes).
// It extracts and unquotes both halves and returns the destination, only
// stripping "a/"/"b/" prefixes when both halves have them (which distinguishes
// the standard prefixed form from a noprefix form whose paths happen to begin
// with "b/"). It returns an empty string on any parse failure so the malformed
// entry is skipped rather than masquerading as a valid filename.
func parseQuotedDiffHeader(rest string) string {
	aEnd := findQuotedEnd(rest, 0)
	if aEnd == -1 {
		return ""
	}
	bStart := aEnd + 1
	for bStart < len(rest) && rest[bStart] == ' ' {
		bStart++
	}
	if bStart >= len(rest) || rest[bStart] != '"' {
		return ""
	}
	bEnd := findQuotedEnd(rest, bStart)
	if bEnd == -1 {
		return ""
	}
	aPath, errA := strconv.Unquote(rest[:aEnd+1])
	bPath, errB := strconv.Unquote(rest[bStart : bEnd+1])
	if errA != nil || errB != nil {
		return ""
	}
	// Strip "a/"/"b/" prefixes only when both halves carry them; otherwise
	// the header is in the noprefix form and the b-side is already the
	// destination path verbatim.
	if bTrimmed, bOK := strings.CutPrefix(bPath, "b/"); bOK && strings.HasPrefix(aPath, "a/") {
		return bTrimmed
	}

	return bPath
}

// findQuotedEnd returns the index of the closing quote of a C-style quoted
// string that starts at index start, or -1 if the string is malformed.
func findQuotedEnd(s string, start int) int {
	if start >= len(s) || s[start] != '"' {
		return -1
	}
	for i := start + 1; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			i++

			continue
		}
		if c == '"' {
			return i
		}
	}

	return -1
}

// unquoteIfQuoted returns s unchanged unless it is a C-style quoted string,
// in which case it returns the unquoted form. On unquote failure it returns
// the empty string so a malformed value cannot become a misleading filename.
func unquoteIfQuoted(s string) string {
	if !strings.HasPrefix(s, `"`) {
		return s
	}
	out, err := strconv.Unquote(s)
	if err != nil {
		return ""
	}

	return out
}

// splitDiffPathsExact searches for a position where rest splits into two
// identical halves around sep, scanning right-to-left so paths containing the
// separator can still be matched. It returns the destination path and true on
// success. This handles the common case of "<p> <p>" or "<p> b/<p>" headers
// where the source and destination paths are equal.
func splitDiffPathsExact(rest, sep string) (string, bool) {
	for i := strings.LastIndex(rest, sep); i != -1; i = strings.LastIndex(rest[:i], sep) {
		if rest[:i] == rest[i+len(sep):] {
			return rest[i+len(sep):], true
		}
	}

	return "", false
}

// splitDiffPathsFallback returns the substring of rest after the first
// occurrence of sep, or "" if sep is absent. This is used as a heuristic
// fallback for headers whose source and destination differ (e.g., the
// uncommon "git diff file1 file2"); splitting at the first separator
// preserves any embedded separator that belongs to the destination path.
func splitDiffPathsFallback(rest, sep string) string {
	_, after, ok := strings.Cut(rest, sep)
	if !ok {
		return ""
	}

	return after
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
	_, _ = fmt.Fprintf(&sb, "🚨 Found %d potential secret(s):\n\n", len(findings))

	for i, finding := range findings {
		_, _ = fmt.Fprintf(&sb, "%d. %s\n", i+1, finding.Description)
		_, _ = fmt.Fprintf(&sb, "   File: %s\n", finding.File)
		if finding.StartLine > 0 {
			_, _ = fmt.Fprintf(&sb, "   Line: %d\n", finding.StartLine)
		}
		_, _ = fmt.Fprintf(&sb, "   Rule: %s\n", finding.RuleID)
		if finding.Secret != "" {
			// Redact most of the secret for safety.
			redacted := redactSecret(finding.Secret)
			_, _ = fmt.Fprintf(&sb, "   Secret: %s\n", redacted)
		}
		if finding.Commit != "" {
			_, _ = fmt.Fprintf(&sb, "   Commit: %s\n", finding.Commit)
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
