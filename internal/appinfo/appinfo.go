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

// Package appinfo provides version information for the application.
package appinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	// Version is set via ldflags during build.
	Version = "dev"

	// readBuildInfoFn lets tests stub out debug.ReadBuildInfo.
	readBuildInfoFn = debug.ReadBuildInfo
)

// String returns a formatted version string.
func String() string {
	commit := "unknown"
	modified := false

	// Get commit from build info if available
	if info, ok := readBuildInfoFn(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if setting.Value != "" {
					commit = setting.Value
					if len(commit) > 7 {
						commit = commit[:7]
					}
				}
			case "vcs.modified":
				modified = setting.Value == "true"
			default:
				// Ignore other build settings
			}
		}
	}

	if modified {
		commit += "-dirty"
	}

	return fmt.Sprintf("lgtmcp version %s (%s, %s/%s)",
		Version, commit, runtime.GOOS, runtime.GOARCH)
}
