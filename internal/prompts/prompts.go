// Package prompts handles loading and templating of review prompts.
package prompts

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"
)

// Embedded default prompts.
var (
	//go:embed review.md
	defaultReviewPrompt string

	//go:embed context_gathering.md
	defaultContextGatheringPrompt string
)

// PromptType represents the type of prompt.
type PromptType string

const (
	// ReviewPrompt is the prompt for the review phase.
	ReviewPrompt PromptType = "review"
	// ContextGatheringPrompt is the prompt for the context gathering phase.
	ContextGatheringPrompt PromptType = "context_gathering"
)

// ErrUnknownPromptType is returned when an unknown prompt type is requested.
var ErrUnknownPromptType = errors.New("unknown prompt type")

// Manager handles loading and templating of prompts.
type Manager struct {
	reviewPromptPath           string
	contextGatheringPromptPath string
}

// New creates a new prompt manager.
func New(reviewPromptPath, contextGatheringPromptPath string) *Manager {
	return &Manager{
		reviewPromptPath:           reviewPromptPath,
		contextGatheringPromptPath: contextGatheringPromptPath,
	}
}

// LoadPrompt loads a prompt template either from file or uses the embedded default.
func (m *Manager) LoadPrompt(promptType PromptType) (string, error) {
	var path string
	var defaultPrompt string

	switch promptType {
	case ReviewPrompt:
		path = m.reviewPromptPath
		defaultPrompt = defaultReviewPrompt
	case ContextGatheringPrompt:
		path = m.contextGatheringPromptPath
		defaultPrompt = defaultContextGatheringPrompt
	default:
		return "", fmt.Errorf("%w: %s", ErrUnknownPromptType, promptType)
	}

	// If no custom path specified, use the default.
	if path == "" {
		return defaultPrompt, nil
	}

	// Try to load from the custom path.
	content, err := os.ReadFile(path) //nolint:gosec // Path is user-configured and validated
	if err != nil {
		return "", fmt.Errorf("failed to read prompt file %s: %w", path, err)
	}

	return string(content), nil
}

// ReviewPromptData contains the data for the review prompt template.
type ReviewPromptData struct {
	AnalysisSection string
	FilesList       string
	Diff            string
}

// BuildReviewPrompt builds the review prompt from template with the given data.
func (m *Manager) BuildReviewPrompt(diff string, changedFiles []string, analysisText string) (string, error) {
	promptTemplate, err := m.LoadPrompt(ReviewPrompt)
	if err != nil {
		return "", fmt.Errorf("failed to load review prompt: %w", err)
	}

	filesList := strings.Join(changedFiles, "\n- ")

	// Include the analysis from the first phase if available.
	analysisSection := ""
	if analysisText != "" {
		analysisSection = fmt.Sprintf("Based on your previous analysis:\n%s\n", analysisText)
	}

	data := ReviewPromptData{
		AnalysisSection: analysisSection,
		FilesList:       filesList,
		Diff:            diff,
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

// ContextGatheringPromptData contains the data for the context gathering prompt template.
type ContextGatheringPromptData struct {
	FilesList string
	Diff      string
}

// BuildContextGatheringPrompt builds the context gathering prompt from template with the given data.
func (m *Manager) BuildContextGatheringPrompt(diff string, changedFiles []string) (string, error) {
	promptTemplate, err := m.LoadPrompt(ContextGatheringPrompt)
	if err != nil {
		return "", fmt.Errorf("failed to load context gathering prompt: %w", err)
	}

	filesList := strings.Join(changedFiles, "\n- ")

	data := ContextGatheringPromptData{
		FilesList: filesList,
		Diff:      diff,
	}

	tmpl, err := template.New("context").Parse(promptTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse context gathering prompt template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute context gathering prompt template: %w", err)
	}

	return buf.String(), nil
}
