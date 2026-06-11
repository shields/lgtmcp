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

// Package config provides configuration management for LGTMCP.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"sigs.k8s.io/yaml"
)

// ErrNoCredentials indicates that no authentication method is configured.
var ErrNoCredentials = errors.New(
	"google.api_key or google.use_adc must be set",
)

// ErrPathTraversal indicates a config-supplied path contains "..".
var ErrPathTraversal = errors.New("path contains parent-directory segments")

// ErrPathOutsideBase indicates a config-supplied absolute path is not under
// the allowed base directory.
var ErrPathOutsideBase = errors.New("absolute path is outside the allowed directory")

const defaultMaxBackoff = "60s"

// NotFoundError indicates the config file was not found.
type NotFoundError struct {
	Path string
}

func (e *NotFoundError) Error() string {
	return "config file not found: " + e.Path
}

// GoogleConfig holds Google/GCP configuration.
type GoogleConfig struct {
	// APIKey is the Gemini API key (optional if using Application Default Credentials).
	APIKey string `json:"api_key,omitempty"`
	// UseADC indicates whether to use Application Default Credentials.
	UseADC bool `json:"use_adc,omitempty"`
}

// GitConfig represents Git configuration.
type GitConfig struct {
	// DiffContextLines is the number of context lines to include in git diff output.
	// Use pointer to distinguish between unset (nil = default 20) and explicitly set to 0.
	DiffContextLines *int `json:"diff_context_lines,omitempty"`
}

// GitleaksConfig represents Gitleaks configuration.
type GitleaksConfig struct {
	Config string `json:"config,omitempty"`
}

// LoggingConfig represents logging configuration.
type LoggingConfig struct {
	// Level is the minimum log level (debug, info, warn, error).
	Level string `json:"level"`

	// Output specifies where logs should be written:
	// - "none": Disable logging
	// - "stderr": Write to standard error
	// - "directory": Write to files in specified directory (default if empty)
	// - "mcp": Send logs to MCP client (not yet wired up in the server
	//   binary; selecting it currently fails at startup).
	// "stdout" is rejected at startup: stdout carries the MCP stdio
	// protocol, so logging there would corrupt the transport.
	Output string `json:"output,omitempty"`

	// Directory is the directory for log files (when Output is "directory").
	// If not specified, uses the standard platform location:
	// - macOS: ~/Library/Logs/lgtmcp/
	// - Linux: ~/.local/share/lgtmcp/logs/
	// - Windows: %LOCALAPPDATA%\lgtmcp\logs\.
	Directory string `json:"directory,omitempty"`
}

// PromptsConfig holds prompt file configuration.
type PromptsConfig struct {
	ReviewPromptPath           string `json:"review_prompt_path,omitempty"`
	ContextGatheringPromptPath string `json:"context_gathering_prompt_path,omitempty"`
}

// RetryConfig represents retry configuration for API calls.
type RetryConfig struct {
	InitialBackoff string `json:"initial_backoff"`
	MaxBackoff     string `json:"max_backoff"`
	// MaxRetries is the number of retries after the initial attempt. Use a
	// pointer to distinguish unset (nil = default 5) from an explicit 0,
	// which disables retries.
	MaxRetries        *int    `json:"max_retries,omitempty"`
	BackoffMultiplier float64 `json:"backoff_multiplier"`
}

// FallbackModelNone disables quota fallback when set as FallbackModel.
const FallbackModelNone = "none"

// GeminiConfig represents Gemini model configuration.
type GeminiConfig struct {
	Retry         *RetryConfig `json:"retry,omitempty"`
	Model         string       `json:"model"`
	FallbackModel string       `json:"fallback_model,omitempty"`
	// Temperature is the sampling temperature. Use a pointer to distinguish
	// unset (nil = default 0.2) from an explicit 0, which requests fully
	// deterministic output.
	Temperature *float32 `json:"temperature,omitempty"`
}

// Config represents the application configuration.
type Config struct {
	Gemini   GeminiConfig   `json:"gemini"`
	Google   GoogleConfig   `json:"google"`
	Git      GitConfig      `json:"git,omitzero"`
	Gitleaks GitleaksConfig `json:"gitleaks,omitzero"`
	Logging  LoggingConfig  `json:"logging"`
	Prompts  PromptsConfig  `json:"prompts,omitzero"`
}

// Load loads the configuration from the YAML file.
func Load() (*Config, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath) //nolint:gosec // Path comes from GetConfigPath which is safe
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &NotFoundError{Path: configPath}
		}
		return nil, fmt.Errorf("cannot read %s: %w", configPath, err)
	}

	// The logger is built from this config, so it does not exist yet; send the
	// advisory permission warning straight to stderr (stdout carries the MCP
	// protocol and must not be written to).
	if warning := configPermissionWarning(configPath); warning != "" {
		_, _ = fmt.Fprintln(os.Stderr, "warning: "+warning)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", configPath, err)
	}

	// Set defaults.
	if cfg.Gemini.Model == "" {
		cfg.Gemini.Model = "gemini-3.1-pro-preview"
	}
	if cfg.Gemini.FallbackModel == "" {
		cfg.Gemini.FallbackModel = "gemini-2.5-pro"
	}
	if cfg.Gemini.Temperature == nil {
		cfg.Gemini.Temperature = new(float32(0.2))
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}

	// Set retry defaults if not specified.
	if cfg.Gemini.Retry == nil {
		cfg.Gemini.Retry = &RetryConfig{
			MaxRetries:        new(5),
			InitialBackoff:    "1s",
			MaxBackoff:        defaultMaxBackoff,
			BackoffMultiplier: 1.4,
		}
	} else {
		// Set individual retry defaults if not specified. MaxRetries is a
		// pointer so an explicit 0 (disable retries) is preserved.
		if cfg.Gemini.Retry.MaxRetries == nil {
			cfg.Gemini.Retry.MaxRetries = new(5)
		}
		if cfg.Gemini.Retry.InitialBackoff == "" {
			cfg.Gemini.Retry.InitialBackoff = "1s"
		}
		if cfg.Gemini.Retry.MaxBackoff == "" {
			cfg.Gemini.Retry.MaxBackoff = defaultMaxBackoff
		}
		if cfg.Gemini.Retry.BackoffMultiplier == 0 {
			cfg.Gemini.Retry.BackoffMultiplier = 1.4
		}
	}

	// Validate credentials: either API key or ADC must be configured.
	if cfg.Google.APIKey == "" && !cfg.Google.UseADC {
		return nil, ErrNoCredentials
	}

	// If both are set, API key takes precedence (logged during client creation).
	return &cfg, nil
}

// configPermissionWarning returns a non-empty advisory message when the config
// file at path is readable or writable by group or others. The file may hold a
// Gemini API key, so permissions broader than 0600 risk leaking the credential.
// It returns "" on Windows (whose file modes carry no Unix permission bits) and
// when the file cannot be stat'd. This is advisory only; Load never fails on it.
func configPermissionWarning(path string) string {
	if runtime.GOOS == "windows" {
		return ""
	}

	info, err := os.Stat(path)
	if err != nil {
		return ""
	}

	perm := info.Mode().Perm()
	if perm&0o077 == 0 {
		return ""
	}

	return fmt.Sprintf(
		"config file %s has permissions %#o and may contain a Gemini API key; "+
			"restrict access with: chmod 600 %s",
		path, perm, path,
	)
}

// GetConfigPath returns the path to the configuration file. It fails rather
// than guessing when the home directory cannot be determined: silently falling
// back to a working-directory-relative "config.yaml" would also relocate the
// [Dir] base used to validate config-supplied paths, weakening that boundary.
func GetConfigPath() (string, error) {
	// Check XDG_CONFIG_HOME first. The XDG Base Directory spec requires these
	// variables to hold absolute paths and says relative values must be
	// ignored (os.UserConfigDir errors on them); honoring one would resolve
	// the config — and the [Dir] base used to validate config-supplied paths —
	// against whatever the process working directory happens to be.
	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdgConfigHome) {
		return filepath.Join(xdgConfigHome, "lgtmcp", "config.yaml"), nil
	}

	// Fall back to ~/.config.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine config path: %w", err)
	}

	return filepath.Join(homeDir, ".config", "lgtmcp", "config.yaml"), nil
}

// Dir returns the directory that holds the lgtmcp configuration file. It is
// the parent directory of [GetConfigPath] and propagates its error.
func Dir() (string, error) {
	path, err := GetConfigPath()
	if err != nil {
		return "", err
	}

	return filepath.Dir(path), nil
}

// ValidatePath validates a filesystem path supplied through the YAML
// configuration file against the lgtmcp config directory ([Dir]). See
// [ValidatePathIn] for the rules enforced.
func ValidatePath(path string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}

	return ValidatePathIn(path, dir)
}

// ValidatePathIn validates a filesystem path supplied through the YAML
// configuration file and resolves it against baseDir. It exists to limit the
// blast radius of a compromised or malicious config:
//
//   - Relative paths are joined onto baseDir, never the current working
//     directory, so an attacker cannot read e.g. ".env" from a project root.
//   - Non-absolute parts must satisfy [filepath.IsLocal], which rules out
//     ".." traversal as well as Windows drive-relative and root-relative
//     escapes such as `C:..\foo` or `\Windows`.
//   - The fully resolved path must remain inside baseDir.
//
// An empty input path is returned as-is so callers can keep distinguishing
// "unset" from "invalid". On success the cleaned absolute path is returned.
func ValidatePathIn(path, baseDir string) (string, error) {
	if path == "" {
		return "", nil
	}

	cleaned := filepath.Clean(path)
	base := filepath.Clean(baseDir)

	if !filepath.IsAbs(cleaned) {
		if !filepath.IsLocal(cleaned) {
			return "", fmt.Errorf("%w: %q", ErrPathTraversal, path)
		}
		cleaned = filepath.Join(base, cleaned)
	}

	rel, err := filepath.Rel(base, cleaned)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: %q (base %q)", ErrPathOutsideBase, path, base)
	}

	return cleaned, nil
}

// NewTestConfig returns a test configuration for use in tests.
func NewTestConfig() *Config {
	return &Config{
		Google: GoogleConfig{
			APIKey: "test-api-key",
		},
		Gemini: GeminiConfig{
			Model:       "gemini-3.1-pro-preview",
			Temperature: new(float32(0.2)),
			Retry: &RetryConfig{
				MaxRetries:        new(5),
				InitialBackoff:    "1s",
				MaxBackoff:        defaultMaxBackoff,
				BackoffMultiplier: 1.4,
			},
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}
