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

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setVersionFlag(t *testing.T, value bool) {
	t.Helper()
	old := *versionFlag
	*versionFlag = value
	t.Cleanup(func() { *versionFlag = old })
}

//nolint:paralleltest // Modifies global versionFlag
func TestRun_VersionFlag(t *testing.T) {
	setVersionFlag(t, true)

	code := run()
	assert.Equal(t, 0, code)
}

func TestRun_ConfigNotFound(t *testing.T) {
	setVersionFlag(t, false)

	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	code := run()
	assert.Equal(t, 1, code)
}

func TestRun_ConfigParseError(t *testing.T) {
	setVersionFlag(t, false)

	tmpDir := t.TempDir()
	lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
	require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(lgtmcpDir, "config.yaml"), []byte(":\n\t-:\t:"), 0o600))

	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	code := run()
	assert.Equal(t, 1, code)
}

func TestRun_ConfigNoCredentials(t *testing.T) {
	setVersionFlag(t, false)

	tmpDir := t.TempDir()
	lgtmcpDir := filepath.Join(tmpDir, "lgtmcp")
	require.NoError(t, os.MkdirAll(lgtmcpDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(lgtmcpDir, "config.yaml"),
		[]byte("gemini:\n  model: test\n"), 0o600))

	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	code := run()
	assert.Equal(t, 1, code)
}
