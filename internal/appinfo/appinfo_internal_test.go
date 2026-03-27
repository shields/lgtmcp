// Copyright © 2026 Michael Shields
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

package appinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func setupBuildInfo(t *testing.T, fn func() (*debug.BuildInfo, bool), version string) {
	t.Helper()
	origFn := readBuildInfoFn
	origVersion := Version
	readBuildInfoFn = fn
	Version = version
	t.Cleanup(func() {
		readBuildInfoFn = origFn
		Version = origVersion
	})
}

//nolint:paralleltest // Modifies global variables
func TestString_WithVCSInfo(t *testing.T) {
	setupBuildInfo(t, func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abc123def4567890"},
				{Key: "vcs.modified", Value: "true"},
			},
		}, true
	}, "1.0.0")

	result := String()
	assert.Contains(t, result, "abc123d-dirty")
	assert.Contains(t, result, "lgtmcp version 1.0.0")
	assert.Contains(t, result, runtime.GOOS+"/"+runtime.GOARCH)
}

//nolint:paralleltest // Modifies global variables
func TestString_ShortCommit(t *testing.T) {
	setupBuildInfo(t, func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abc12"},
				{Key: "vcs.modified", Value: "false"},
			},
		}, true
	}, "dev")

	result := String()
	assert.Contains(t, result, "(abc12,")
	assert.NotContains(t, result, "-dirty")
}

//nolint:paralleltest // Modifies global variables
func TestString_NoBuildInfo(t *testing.T) {
	setupBuildInfo(t, func() (*debug.BuildInfo, bool) {
		return nil, false
	}, "dev")

	result := String()
	assert.Contains(t, result, "unknown")
	assert.NotContains(t, result, "-dirty")
}

//nolint:paralleltest // Modifies global variables
func TestDetailedString_WithVCSInfo(t *testing.T) {
	setupBuildInfo(t, func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "v1.2.3"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abc123def4567890"},
				{Key: "vcs.time", Value: "2025-06-01T12:00:00Z"},
				{Key: "vcs.modified", Value: "true"},
			},
		}, true
	}, "1.2.3")

	result := DetailedString()
	assert.Contains(t, result, "lgtmcp version 1.2.3")
	assert.Contains(t, result, "Module:   v1.2.3")
	assert.Contains(t, result, "Commit:   abc123def4567890")
	assert.Contains(t, result, "VCS Time: 2025-06-01T12:00:00Z")
	assert.Contains(t, result, "Modified: true")
	assert.Contains(t, result, "Go:")
	assert.Contains(t, result, "OS/Arch:")
}

//nolint:paralleltest // Modifies global variables
func TestDetailedString_NoBuildInfo(t *testing.T) {
	setupBuildInfo(t, func() (*debug.BuildInfo, bool) {
		return nil, false
	}, "dev")

	result := DetailedString()
	assert.Contains(t, result, "lgtmcp version dev")
	assert.NotContains(t, result, "Module:")
	assert.NotContains(t, result, "Commit:")
}

//nolint:paralleltest // Modifies global variables
func TestDetailedString_DevelVersion(t *testing.T) {
	setupBuildInfo(t, func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "(devel)"},
		}, true
	}, "dev")

	result := DetailedString()
	assert.NotContains(t, result, "Module:")
}

//nolint:paralleltest // Modifies global variables
func TestDetailedString_EmptyModuleVersion(t *testing.T) {
	setupBuildInfo(t, func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: ""},
		}, true
	}, "dev")

	result := DetailedString()
	assert.NotContains(t, result, "Module:")
}

//nolint:paralleltest // Modifies global variables
func TestString_EmptyRevision(t *testing.T) {
	setupBuildInfo(t, func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: ""},
			},
		}, true
	}, "dev")

	result := String()
	assert.Contains(t, result, "unknown")
}

//nolint:paralleltest // Modifies global variables
func TestDetailedString_EmptyVCSFields(t *testing.T) {
	setupBuildInfo(t, func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: ""},
				{Key: "vcs.time", Value: ""},
				{Key: "vcs.modified", Value: "false"},
			},
		}, true
	}, "dev")

	result := DetailedString()
	assert.NotContains(t, result, "Commit:")
	assert.NotContains(t, result, "VCS Time:")
	assert.NotContains(t, result, "Modified:")
}

//nolint:paralleltest // Modifies global variables
func TestDetailedString_UnknownSettings(t *testing.T) {
	setupBuildInfo(t, func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: "GOARCH", Value: "amd64"},
				{Key: "GOOS", Value: "linux"},
			},
		}, true
	}, "dev")

	result := DetailedString()
	for line := range strings.SplitSeq(result, "\n") {
		assert.NotContains(t, line, "GOARCH")
	}
}
