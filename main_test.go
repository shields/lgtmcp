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

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setVersionFlag(t *testing.T, value bool) {
	t.Helper()
	old := *versionFlag
	*versionFlag = value
	t.Cleanup(func() { *versionFlag = old })
}

func setToolsFlag(t *testing.T, value string) {
	t.Helper()
	old := *toolsFlag
	*toolsFlag = value
	t.Cleanup(func() { *toolsFlag = old })
}

func TestToolsFlagDefault(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "review_only,review_and_commit", flag.Lookup("tools").DefValue)
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	file, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, file.Close()) })

	old := os.Stderr
	os.Stderr = file
	t.Cleanup(func() { os.Stderr = old })

	fn()

	out, err := os.ReadFile(file.Name())
	require.NoError(t, err)

	return string(out)
}

func TestRun_InvalidToolsFlag(t *testing.T) {
	setVersionFlag(t, false)

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"empty", "", "no tools enabled"},
		{"unknown", "bogus", `unknown tool: "bogus"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setToolsFlag(t, tt.value)

			// Without the early check, run would go on to read the developer's
			// real config.
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())

			var code int
			stderr := captureStderr(t, func() { code = run() })

			assert.Equal(t, 1, code)
			assert.Contains(t, stderr, "invalid -tools")
			assert.Contains(t, stderr, tt.want)
		})
	}
}

func TestRun_UnexpectedArgument(t *testing.T) {
	setVersionFlag(t, false)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Parsing stops at the first non-flag argument, so -tools is never seen.
	require.NoError(t, flag.CommandLine.Parse([]string{"review_only", "-tools", "review_only"}))
	t.Cleanup(func() { require.NoError(t, flag.CommandLine.Parse(nil)) })

	var code int
	stderr := captureStderr(t, func() { code = run() })

	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, `unexpected argument "review_only"`)
}

func listToolsViaRun(t *testing.T) []string {
	t.Helper()

	cfgDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(cfgDir, "lgtmcp"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "lgtmcp", "config.yaml"),
		[]byte("google:\n  api_key: test-key\nlogging:\n  output: none\n"), 0o600))
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	stdinR, stdinW, err := os.Pipe()
	require.NoError(t, err)
	stdoutR, stdoutW, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, stdinR.Close())
		assert.NoError(t, stdoutR.Close())
	})

	// The whole exchange is queued and stdin closed before run starts: the
	// server answers each request, reads EOF, and run returns 0.
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
			`"capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}
	_, err = io.WriteString(stdinW, strings.Join(requests, "\n")+"\n")
	require.NoError(t, err)
	require.NoError(t, stdinW.Close())

	var out bytes.Buffer
	drained := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&out, stdoutR)
		drained <- copyErr
	}()

	oldStdin, oldStdout := os.Stdin, os.Stdout
	t.Cleanup(func() { os.Stdin, os.Stdout = oldStdin, oldStdout })
	os.Stdin, os.Stdout = stdinR, stdoutW
	code := run()
	os.Stdin, os.Stdout = oldStdin, oldStdout

	require.NoError(t, stdoutW.Close())
	require.NoError(t, <-drained)
	require.Equal(t, 0, code)

	// stdout carries the MCP protocol, so every line must be a JSON-RPC message.
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		var msg struct {
			ID     int                 `json:"id"`
			Result mcp.ListToolsResult `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &msg), "stdout line: %q", line)
		if msg.ID != 2 {
			continue
		}
		names := make([]string, 0, len(msg.Result.Tools))
		for _, tool := range msg.Result.Tools {
			names = append(names, tool.Name)
		}
		slices.Sort(names)

		return names
	}
	require.Fail(t, "no tools/list response", "stdout: %q", out.String())

	return nil
}

//nolint:paralleltest // Modifies global flags, os.Stdin, and os.Stdout
func TestRun_ToolsFlagReachesServer(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{"default", flag.Lookup("tools").DefValue, []string{"review_and_commit", "review_only"}},
		{"review only", "review_only", []string{"review_only"}},
		{"review and commit only", "review_and_commit", []string{"review_and_commit"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setVersionFlag(t, false)
			setToolsFlag(t, tt.value)

			assert.Equal(t, tt.want, listToolsViaRun(t))
		})
	}
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
