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

// Package version provides version information for the application.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Version is set via ldflags during build.
var Version = "dev"

// String returns a formatted version string.
func String() string {
	commit := "unknown"
	modified := false

	// Get commit from build info if available
	if info, ok := debug.ReadBuildInfo(); ok {
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

// DetailedString returns a detailed version string including build info.
func DetailedString() string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "lgtmcp version %s\n", Version)
	_, _ = fmt.Fprintf(&b, "  Go:       %s\n", runtime.Version())
	_, _ = fmt.Fprintf(&b, "  OS/Arch:  %s/%s\n", runtime.GOOS, runtime.GOARCH)

	// Add module and VCS information if available
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		if buildInfo.Main.Version != "" && buildInfo.Main.Version != "(devel)" {
			_, _ = fmt.Fprintf(&b, "  Module:   %s\n", buildInfo.Main.Version)
		}

		for _, setting := range buildInfo.Settings {
			switch setting.Key {
			case "vcs.revision":
				if setting.Value != "" {
					_, _ = fmt.Fprintf(&b, "  Commit:   %s\n", setting.Value)
				}
			case "vcs.time":
				if setting.Value != "" {
					_, _ = fmt.Fprintf(&b, "  VCS Time: %s\n", setting.Value)
				}
			case "vcs.modified":
				if setting.Value == "true" {
					_, _ = b.WriteString("  Modified: true\n")
				}
			default:
				// Ignore other build settings
			}
		}
	}

	return b.String()
}
