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

// Package prompts handles loading and templating of review prompts.
package prompts

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"

	"msrl.dev/lgtmcp/internal/config"
)

// Embedded default prompt.
//
//go:embed review.md
var defaultReviewPrompt string

// PromptType represents the type of prompt.
type PromptType string

const (
	// ReviewPrompt is the prompt that drives the review conversation.
	ReviewPrompt PromptType = "review"
)

// ErrUnknownPromptType is returned when an unknown prompt type is requested.
var ErrUnknownPromptType = errors.New("unknown prompt type")

// Manager handles loading and templating of prompts.
type Manager struct {
	reviewPromptPath string
	// configDir is the lgtmcp config directory used to validate custom
	// prompt paths. When empty, [config.Dir] is used at validation
	// time. Tests may override it via [Manager.SetConfigDir].
	configDir string
}

// New creates a new prompt manager.
func New(reviewPromptPath string) *Manager {
	return &Manager{
		reviewPromptPath: reviewPromptPath,
	}
}

// SetConfigDir overrides the lgtmcp config directory used to validate custom
// prompt paths. Intended for tests; production code should rely on the
// default ([config.Dir]).
func (m *Manager) SetConfigDir(dir string) {
	m.configDir = dir
}

// LoadPrompt loads a prompt template either from file or uses the embedded default.
func (m *Manager) LoadPrompt(promptType PromptType) (string, error) {
	var path string
	var defaultPrompt string

	switch promptType {
	case ReviewPrompt:
		path = m.reviewPromptPath
		defaultPrompt = defaultReviewPrompt
	default:
		return "", fmt.Errorf("%w: %s", ErrUnknownPromptType, promptType)
	}

	// If no custom path specified, use the default. The embedded defaults
	// carry an Apache license header as a leading HTML comment for source-file
	// compliance; strip it here so the boilerplate never reaches the model and
	// wastes context tokens. Custom prompt files are returned verbatim — a
	// leading comment there may be intentional.
	if path == "" {
		return stripLeadingComment(defaultPrompt), nil
	}

	// Reject traversal and absolute paths outside the lgtmcp config directory
	// so that a compromised config cannot exfiltrate arbitrary files via the
	// review prompt.
	configDir := m.configDir
	if configDir == "" {
		dir, err := config.Dir()
		if err != nil {
			return "", fmt.Errorf("cannot determine config directory: %w", err)
		}
		configDir = dir
	}
	safePath, err := config.ValidatePathIn(path, configDir)
	if err != nil {
		return "", fmt.Errorf("invalid prompt path: %w", err)
	}

	content, err := os.ReadFile(safePath) //nolint:gosec // Path validated by config.ValidatePathIn
	if err != nil {
		return "", fmt.Errorf("failed to read prompt file %s: %w", path, err)
	}

	return string(content), nil
}

// ReviewPromptData contains the data for the review prompt template.
//
// It deliberately has no AnalysisSection field. A template that references
// one was written for a review in which a separate context-gathering prompt
// fetched files, so it lacks any instruction to call get_file_content; it must
// fail when executed rather than run as a diff-only review that can approve
// unseen code.
type ReviewPromptData struct {
	InstructionsSection string
	// FilesList holds every changed path (including deletions); retained for
	// custom templates that reference {{.FilesList}}.
	FilesList         string
	ExistingFilesList string
	DeletedFilesList  string
	DirectoryListing  string
	Diff              string
	CurrentDate       string
}

// BuildReviewPrompt builds the review prompt from template with the given data.
// deletedFiles must be a subset of changedFiles; paths in it are listed as
// deletions and excluded from the existing-files section.
//
//nolint:lll // Long function signature
func (m *Manager) BuildReviewPrompt(diff string, changedFiles, deletedFiles []string, instructions, directoryListing string) (string, error) {
	promptTemplate, err := m.LoadPrompt(ReviewPrompt)
	if err != nil {
		return "", fmt.Errorf("failed to load review prompt: %w", err)
	}

	existing, deleted := splitFiles(changedFiles, deletedFiles)

	data := ReviewPromptData{
		InstructionsSection: instructions,
		FilesList:           joinNames(changedFiles),
		ExistingFilesList:   joinNames(existing),
		DeletedFilesList:    joinNames(deleted),
		DirectoryListing:    directoryListing,
		Diff:                diff,
		CurrentDate:         time.Now().Format("January 2, 2006"),
	}

	tmpl, err := template.New("review").Parse(promptTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse review prompt template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute review prompt template: %w", err)
	}

	return buf.String(), nil
}

// joinNames quotes any name containing a character that does not print (a
// newline, say) so that it cannot start a new line of the prompt.
func joinNames(names []string) string {
	items := make([]string, len(names))
	for i, name := range names {
		items[i] = name
		if strings.IndexFunc(name, func(r rune) bool { return !strconv.IsPrint(r) }) >= 0 {
			items[i] = strconv.Quote(name)
		}
	}

	return strings.Join(items, "\n- ")
}

// stripLeadingComment removes a single leading HTML comment block (e.g. the
// embedded prompt's Apache license header) plus the blank lines that follow
// it, so the boilerplate does not consume model context. Only a comment at the
// very start (after optional leading whitespace) is removed; comments elsewhere
// and an unterminated comment leave the template untouched.
func stripLeadingComment(s string) string {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if !strings.HasPrefix(trimmed, "<!--") {
		return s
	}

	_, after, found := strings.Cut(trimmed, "-->")
	if !found {
		return s
	}

	return strings.TrimLeft(after, " \t\r\n")
}

// splitFiles partitions changedFiles into the paths that still exist (i.e.,
// are not in deletedFiles) and the paths that were deleted, preserving the
// order from changedFiles in both outputs. The deletedFiles set is matched by
// equality.
//
//nolint:nonamedreturns // Both returns are []string; names disambiguate them for revive's confusing-results rule.
func splitFiles(changedFiles, deletedFiles []string) (existing, deleted []string) {
	if len(deletedFiles) == 0 {
		return changedFiles, nil
	}
	deletedSet := make(map[string]bool, len(deletedFiles))
	for _, f := range deletedFiles {
		deletedSet[f] = true
	}
	for _, f := range changedFiles {
		if deletedSet[f] {
			deleted = append(deleted, f)
		} else {
			existing = append(existing, f)
		}
	}
	return existing, deleted
}
