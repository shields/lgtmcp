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

//go:build e2e || integration

package test

import "msrl.dev/lgtmcp/internal/security"

// fakeSecrets generates synthetic, non-functional secret values for tests.
//
// It lives in this untagged helper so both the e2e and integration suites can
// share a single declaration; declaring it in each tagged file collides when
// the package is built with both tags at once.
var fakeSecrets = security.FakeSecrets{}
