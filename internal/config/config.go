package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// ErrNoCredentials indicates that no authentication method is configured.
var ErrNoCredentials = errors.New("no authentication method configured: either google.api_key must be set or google.use_adc must be true")

// GoogleConfig holds Google/GCP configuration.
type GoogleConfig struct {
	// APIKey is the Gemini API key (optional if using Application Default Credentials).
	APIKey string `json:"api_key,omitempty"`
	// UseADC indicates whether to use Application Default Credentials.
	UseADC bool `json:"use_adc,omitempty"`
}

// GitleaksConfig represents Gitleaks configuration.
type GitleaksConfig struct {
	Config string `json:"config,omitempty"`
}

// LoggingConfig represents logging configuration.
type LoggingConfig struct {
	Level string `json:"level"`
}

// RetryConfig represents retry configuration for API calls.
type RetryConfig struct {
	InitialBackoff    string  `json:"initial_backoff"`
	MaxBackoff        string  `json:"max_backoff"`
	MaxRetries        int     `json:"max_retries"`
	BackoffMultiplier float64 `json:"backoff_multiplier"`
}

// GeminiConfig represents Gemini model configuration.
type GeminiConfig struct {
	Retry       *RetryConfig `json:"retry,omitempty"`
	Model       string       `json:"model"`
	Temperature float32      `json:"temperature,omitempty"`
}

// Config represents the application configuration.
//
//nolint:govet // Field alignment optimization not necessary for config struct
type Config struct {
	Gemini   GeminiConfig   `json:"gemini"`
	Google   GoogleConfig   `json:"google"`
	Gitleaks GitleaksConfig `json:"gitleaks,omitempty"`
	Logging  LoggingConfig  `json:"logging"`
}

// Load loads the configuration from the YAML file.
// Load loads the configuration from the YAML file.
func Load() (*Config, error) {
	configPath := GetConfigPath()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file at %s: %w", configPath, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set defaults.
	if cfg.Gemini.Model == "" {
		cfg.Gemini.Model = "gemini-2.5-pro"
	}
	if cfg.Gemini.Temperature == 0 {
		cfg.Gemini.Temperature = 0.2
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}

	// Set retry defaults if not specified.
	if cfg.Gemini.Retry == nil {
		cfg.Gemini.Retry = &RetryConfig{
			MaxRetries:        5,
			InitialBackoff:    "1s",
			MaxBackoff:        "60s",
			BackoffMultiplier: 1.4,
		}
	} else {
		// Set individual retry defaults if not specified.
		if cfg.Gemini.Retry.MaxRetries == 0 {
			cfg.Gemini.Retry.MaxRetries = 5
		}
		if cfg.Gemini.Retry.InitialBackoff == "" {
			cfg.Gemini.Retry.InitialBackoff = "1s"
		}
		if cfg.Gemini.Retry.MaxBackoff == "" {
			cfg.Gemini.Retry.MaxBackoff = "60s"
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

// GetConfigPath returns the path to the configuration file.
func GetConfigPath() string {
	// Check XDG_CONFIG_HOME first.
	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
		return filepath.Join(xdgConfigHome, "lgtmcp", "config.yaml")
	}

	// Fall back to ~/.config.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// If we can't get home dir, use current directory as last resort.
		return "config.yaml"
	}

	return filepath.Join(homeDir, ".config", "lgtmcp", "config.yaml")
}

// NewTestConfig returns a test configuration for use in tests.
func NewTestConfig() *Config {
	return &Config{
		Google: GoogleConfig{
			APIKey: "test-api-key",
		},
		Gemini: GeminiConfig{
			Model:       "gemini-2.5-pro",
			Temperature: 0.2,
			Retry: &RetryConfig{
				MaxRetries:        5,
				InitialBackoff:    "1s",
				MaxBackoff:        "60s",
				BackoffMultiplier: 1.4,
			},
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}
