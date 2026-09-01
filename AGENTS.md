<!--
Copyright © 2025-2026 Michael Shields

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# LGTMCP - AI Code Review via MCP

**IMPORTANT**:

1. After completing any task, run `make lint`, `make test`, then use `mcp__lgtmcp__review_only` to review changes.
2. Do not use conventional commit prefixes (feat:, fix:, docs:, etc.) in commit messages.
3. Update this file only when a change alters durable guidance — commands, policies, architecture, or gotchas recorded nowhere else. Implementation detail belongs in code doc comments; this file is not a changelog.

## File Headers

All new files must include the project copyright and Apache 2.0 license header, matching the style of existing files.

## Commit policy

The sanctioned way to commit changes to this repo is via the `mcp__lgtmcp__review_and_commit` MCP tool, which performs a Gemini-reviewed commit and only writes the commit if the reviewer returns LGTM. Direct `git commit` invocations from the Bash tool are denied via `.claude/settings.json` to prevent unreviewed commits from slipping in.

The Bash deny rule (`Bash(git commit:*)`) is best-effort defense-in-depth, not a hard sandbox. Be aware of these intentional gaps:

- It does not (and cannot) block commits that go through the `mcp__lgtmcp__review_and_commit` tool. That tool runs inside the lgtmcp Go binary, outside the Bash deny scope, and committing is its entire purpose. This is by design.
- The deny rule matches on the leading command name. It does not match indirect invocations such as `sh -c "git commit ..."` or `bash -c "git commit ..."`. This is a known limitation of the Claude Code permission grammar — the rule cannot reliably inspect arguments to a shell wrapper. Do not use shell indirection to bypass the policy; always go through the MCP tool.

If you ever genuinely need a raw `git commit` (e.g., emergency recovery), make that explicit to the user and get approval first rather than working around the deny rule.

## Overview

LGTMCP is a Model Context Protocol server that reviews code changes using Google Gemini 3.7 Flash and either commits them (if approved) or returns review comments. Setup, configuration, usage, and troubleshooting are covered in `README.md`; every configuration option is documented in `config.example.yaml`.

**Note**: The `mcp__lgtmcp__` tools may run a different version than this repository. Always test with actual code.

## Architecture

- **MCP Server** (`pkg/mcp/`) - Protocol implementation using mark3labs/mcp-go
- **Review Engine** (`internal/review/`) - Gemini 3.7 Flash integration with file retrieval
- **Git Operations** (`internal/git/`) - Diff generation, commit management, instruction file discovery
- **Security** (`internal/security/`) - Gitleaks v8 for secret detection
- **Prompts** (`internal/prompts/`) - Customizable review prompts with embedded defaults
- **Progress** (`internal/progress/`) - MCP progress notification support

## Development

```bash
make test    # Run tests
make coverage # Run tests with coverage
make build   # Build binary (VERSION=x.y.z for custom version)
make lint    # Run golangci-lint
make fmt     # Format code with gofumpt and prettier
make clean   # Remove build artifacts
make deps    # Install tools and dependencies
```

### Formatting gotcha

`CLAUDE.md` is a symlink to `AGENTS.md`, and both prettier callsites (the Makefile `fmt` target and `.lefthook.yml`'s `format-prettier` hook) exclude it with a `:!CLAUDE.md` pathspec; the comments at those callsites explain the basics. Two details recorded only here: a `.prettierignore` entry cannot fix this, because prettier's symlink hard-error fires before ignore rules are consulted (verified with the file both in and out of the ignore list); and letting prettier expand its own globs instead of taking a git-derived file list was rejected because its traversal, while skipping symlinks silently, would sweep in untracked local state such as `.claude/` and `.serena/`. A future tracked symlink needs the same pathspec exclusion.

### CI

`lint.yaml` (golangci-lint, then zizmor on the workflow files) and `test.yaml` (race + coverage) run on every push and pull request; actions are SHA-pinned, and the zizmor step's configuration rationale is commented inline in `lint.yaml`. Recorded only here:

- The project intentionally runs no SAST scan of the Go source — the CodeQL workflow was deliberately removed and is not being replaced. zizmor audits only the workflow YAML, so it is not a substitute; reintroduce a Go SAST workflow if that coverage is wanted again.
- Renovate's SHA bumps of `zizmor-action` are what advance the bundled zizmor version; zizmor is not pinned separately.

## TODO

- [ ] **File size limits** - Prevent excessive Gemini API token usage

## Design notes

Implementation details live in doc comments on the functions named below; these notes record only decisions and constraints written nowhere else.

- **MCP error semantics** (`pkg/mcp/server.go`): following MCP guidance that tool-execution failures belong inside the result object, only malformed requests (non-object arguments, a non-string `directory` or `commit_message`) return protocol-level Go errors; every failure while the tool runs is an in-band `IsError` result the model can read and react to. A detected secret and "no changes to review" are non-error results — findings, not failures. The per-case classification is commented in `server.go` and asserted by `assertInBandToolError` in the tests.
- **New-file diff synthesis** (`writeNewFileDiff`/`gitFileMode`/`newFileForDiff` in `internal/git/git.go`): synthesized blocks match real `git diff` byte-for-byte; see the doc comments. One cross-file guarantee: an empty new file's header-only block (no hunk) still registers in `security.ExtractChangedFilesDetailed`, which keys off the `diff --git` header, so `review_and_commit`'s diff-derived staging list commits empty new files instead of dropping them.
- **Context caching** (`internal/review/review.go`): only Gemini's implicit caching is used; explicit caches (`Caches.Create`) were deliberately rejected because, at one review at a time, the hourly storage floor plus per-cache create overhead exceed the read discount — explicit caching would lose money. Implicit caching engages across Phase 1's tool-calling loop (the growing history, including the diff, is resent each turn) and never fires on prompts below the model's minimum (4096 tokens for `gemini-3.7-flash`) — expected, not a bug.
- **Response footer** (`formatUsageFooter` in `pkg/mcp/server.go`): `formatCount` hand-rolls thousands separators rather than using `golang.org/x/text/message`, deliberately keeping `x/text` an indirect-only dependency; the footer is always ASCII English, so locale-aware formatting isn't needed.
- **Gemini API constraint**: function calling and Google Search grounding cannot be combined in one request. The review's `get_file_content` tool already relies on function calling, so a search-grounded review would require a redesign.
- **No sampling parameters**: `temperature`, `top_p`, and `top_k` are deliberately not sent (a `temperature` option was removed in 2026). Gemini 3 models do not support them — Google's Gemini 3.7 Flash migration checklist says to strip them — so `thinking_level` is the review's only generation knob.

## Technical Choices

- **Go**: Single binary, excellent performance, native git ops
- **mark3labs/mcp-go**: Most mature MCP implementation
- **Gitleaks v8**: MIT licensed, embedded library (no subprocess)
- **Gemini 3.7 Flash**: Fast, capable model for code understanding

## Why the git CLI (not go-git)

lgtmcp deliberately shells out to the `git` binary — centralized in
`internal/git/git.go`, including the `git.IsIgnored` gitignore check — rather than
a pure-Go library such as go-git. The deciding factors:

- **The diff is the product.** The unified diff sent to Gemini must match what
  `git diff` emits byte-for-byte; go-git's diff engine differs (hunk boundaries,
  `\ No newline at end of file`, prefixes, rename/binary handling), and the
  new-file synthesis in `writeNewFileDiff`/`gitFileMode` is tuned to real git.
- **Commit behavior is a feature.** `review_and_commit` must honor the user's git
  config — GPG/SSH signing, hooks, `includeIf`, commit templates. go-git runs no
  hooks and has only partial signing.
- **`check-ignore` is a fail-closed security boundary.** It depends on git's full
  ignore semantics (nested `.gitignore`, negations, `core.excludesFile`,
  `.git/info/exclude`); go-git's matcher is its least-complete component, and an
  under-match would expose a file the repo intends to hide.
- **No dependency is saved.** lgtmcp only runs inside git repositories, so the
  `git` binary is always present and is a required runtime dependency.

All git subprocesses route through one hardened helper in `internal/git`
(`runGit`), which strips every `GIT_*` variable — so an inherited
`GIT_DIR`/`GIT_CONFIG_GLOBAL` from a surrounding git process such as a pre-commit
hook cannot redirect a command at the wrong repository — and applies a uniform
timeout.
