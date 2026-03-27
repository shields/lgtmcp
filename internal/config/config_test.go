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

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	t.Run("valid config file", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")

		configContent := `
google:
  api_key: "test-api-key"
gemini:
  model: "gemini-3.1-pro-preview"
logging:
  level: "debug"
`
		require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

		// Set XDG to point to our temp dir minus the lgtmcp subdirectory.
		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		// Create the expected directory structure.
		lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
		require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))
		require.NoError(t, os.Rename(configPath, filepath.Join(lgtmcpDir, "config.yaml")))

		cfg, err := Load()
		require.NoError(t, err)
		assert.NotNil(t, cfg)
		assert.Equal(t, "test-api-key", cfg.Google.APIKey)
		assert.Equal(t, "gemini-3.1-pro-preview", cfg.Gemini.Model)
		assert.Equal(t, "debug", cfg.Logging.Level)
	})

	t.Run("missing config file", func(t *testing.T) {
		tmpDir := t.TempDir()

		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		cfg, err := Load()
		require.Error(t, err)
		assert.Nil(t, cfg)

		var notFound *NotFoundError
		require.ErrorAs(t, err, &notFound)
		assert.Contains(t, notFound.Path, "config.yaml")
	})

	t.Run("invalid yaml", func(t *testing.T) {
		tmpDir := t.TempDir()
		lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
		require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))

		configPath := filepath.Join(lgtmcpDir, "config.yaml")
		invalidContent := `invalid: yaml: content:`
		require.NoError(t, os.WriteFile(configPath, []byte(invalidContent), 0o600))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		cfg, err := Load()
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "cannot parse")
	})

	t.Run("missing api key", func(t *testing.T) {
		tmpDir := t.TempDir()
		lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
		require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))

		configPath := filepath.Join(lgtmcpDir, "config.yaml")
		configContent := `
gemini:
  model: "gemini-3.1-pro-preview"
logging:
  level: "info"
`
		require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		cfg, err := Load()
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Equal(t, ErrNoCredentials, err)
	})

	t.Run("defaults are applied", func(t *testing.T) {
		tmpDir := t.TempDir()
		lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
		require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))

		configPath := filepath.Join(lgtmcpDir, "config.yaml")
		configContent := `
google:
  api_key: "test-api-key"
`
		require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		cfg, err := Load()
		require.NoError(t, err)
		assert.NotNil(t, cfg)
		assert.Equal(t, "gemini-3.1-pro-preview", cfg.Gemini.Model) // Default.
		assert.Equal(t, "info", cfg.Logging.Level)                  // Default.
	})

	t.Run("api key is required", func(t *testing.T) {
		tmpDir := t.TempDir()
		lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
		require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))

		configPath := filepath.Join(lgtmcpDir, "config.yaml")
		configContent := `
google:
  # No API key provided
gemini:
  model: "gemini-3.1-pro-preview"
`
		require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		cfg, err := Load()
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Equal(t, ErrNoCredentials, err)
	})

	t.Run("application default credentials", func(t *testing.T) {
		tmpDir := t.TempDir()
		lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
		require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))

		configPath := filepath.Join(lgtmcpDir, "config.yaml")
		configContent := `
google:
  use_adc: true
gemini:
  model: "gemini-3.1-pro-preview"
logging:
  level: "info"
`
		require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		cfg, err := Load()
		require.NoError(t, err)
		assert.NotNil(t, cfg)
		assert.Empty(t, cfg.Google.APIKey)
		assert.True(t, cfg.Google.UseADC)
		assert.Equal(t, "gemini-3.1-pro-preview", cfg.Gemini.Model)
	})

	t.Run("api key takes precedence over ADC", func(t *testing.T) {
		tmpDir := t.TempDir()
		lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
		require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))

		configPath := filepath.Join(lgtmcpDir, "config.yaml")
		configContent := `
google:
  api_key: "test-api-key"
  use_adc: true
gemini:
  model: "gemini-3.1-pro-preview"
`
		require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		cfg, err := Load()
		require.NoError(t, err)
		assert.NotNil(t, cfg)
		assert.Equal(t, "test-api-key", cfg.Google.APIKey)
		assert.True(t, cfg.Google.UseADC)
		// Both can be set, API key will be used preferentially in the review module.
	})

	t.Run("neither api key nor ADC", func(t *testing.T) {
		tmpDir := t.TempDir()
		lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
		require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))

		configPath := filepath.Join(lgtmcpDir, "config.yaml")
		configContent := `
google:
  use_adc: false
gemini:
  model: "gemini-3.1-pro-preview"
`
		require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		cfg, err := Load()
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Equal(t, ErrNoCredentials, err)
	})
}

func TestNotFoundError_Error(t *testing.T) {
	t.Parallel()
	err := &NotFoundError{Path: "/some/path/config.yaml"}
	assert.Equal(t, "config file not found: /some/path/config.yaml", err.Error())
}

func TestLoad_TrulyInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
	require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))

	configPath := filepath.Join(lgtmcpDir, "config.yaml")
	// Tabs at the start of a line followed by invalid structure cause unmarshal failure.
	require.NoError(t, os.WriteFile(configPath, []byte(":\n\t- :\t:"), 0o600))

	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	cfg, err := Load()
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "cannot parse")
}

func TestLoad_FileReadError(t *testing.T) {
	tmpDir := t.TempDir()
	lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
	require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))

	// Create config.yaml as a directory so os.ReadFile fails with non-ErrNotExist.
	configPath := filepath.Join(lgtmcpDir, "config.yaml")
	require.NoError(t, os.MkdirAll(configPath, 0o750))

	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	cfg, err := Load()
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "cannot read")
}

func TestLoad_PartialRetryConfig(t *testing.T) {
	tmpDir := t.TempDir()
	lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
	require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))

	configContent := `
google:
  api_key: "test-api-key"
gemini:
  retry:
    max_retries: 3
`
	require.NoError(t, os.WriteFile(filepath.Join(lgtmcpDir, "config.yaml"), []byte(configContent), 0o600))

	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.Gemini.Retry.MaxRetries)
	assert.Equal(t, "1s", cfg.Gemini.Retry.InitialBackoff)
	assert.Equal(t, "60s", cfg.Gemini.Retry.MaxBackoff)
	assert.InDelta(t, 1.4, cfg.Gemini.Retry.BackoffMultiplier, 0.01)
}

func TestLoad_TemperatureZero(t *testing.T) {
	tmpDir := t.TempDir()
	lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
	require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))

	configContent := `
google:
  api_key: "test-api-key"
gemini:
  temperature: 0
`
	require.NoError(t, os.WriteFile(filepath.Join(lgtmcpDir, "config.yaml"), []byte(configContent), 0o600))

	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	cfg, err := Load()
	require.NoError(t, err)
	assert.InDelta(t, 0.2, cfg.Gemini.Temperature, 0.01)
}

func TestNewTestConfig(t *testing.T) {
	t.Parallel()
	cfg := NewTestConfig()
	assert.Equal(t, "test-api-key", cfg.Google.APIKey)
	assert.Equal(t, "gemini-3.1-pro-preview", cfg.Gemini.Model)
	assert.InDelta(t, 0.2, cfg.Gemini.Temperature, 0.01)
	assert.NotNil(t, cfg.Gemini.Retry)
	assert.Equal(t, 5, cfg.Gemini.Retry.MaxRetries)
	assert.Equal(t, "info", cfg.Logging.Level)
}

func TestGetConfigPath(t *testing.T) {
	t.Run("uses XDG_CONFIG_HOME when set", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/custom/config")
		path := GetConfigPath()
		assert.Equal(t, "/custom/config/lgtmcp/config.yaml", path)
	})

	t.Run("falls back to ~/.config when XDG not set", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")

		path := GetConfigPath()
		homeDir, err := os.UserHomeDir()
		require.NoError(t, err)
		expected := filepath.Join(homeDir, ".config", "lgtmcp", "config.yaml")
		assert.Equal(t, expected, path)
	})
}
