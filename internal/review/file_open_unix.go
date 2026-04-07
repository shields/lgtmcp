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

//go:build unix

package review

import "syscall"

// openNonblockFlag is OR'd into the open flags used by handleFileRetrieval so
// that opening a FIFO or other slow device returns immediately instead of
// hanging the review process. After the open the caller verifies via
// f.Stat() that the descriptor is a regular file and rejects anything else.
const openNonblockFlag = syscall.O_NONBLOCK
