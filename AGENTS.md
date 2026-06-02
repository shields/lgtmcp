<!--
Copyright © 2025 Michael Shields

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

1. Update this file after completing any task to document changes.
2. After completing any task, run `make lint`, `make test`, then use `mcp__lgtmcp__review_only` to review changes.
3. Do not use conventional commit prefixes (feat:, fix:, docs:, etc.) in commit messages.

## File Headers

All new files must include the project copyright and Apache 2.0 license header, matching the style of existing files.

## Commit policy

The sanctioned way to commit changes to this repo is via the `mcp__lgtmcp__review_and_commit` MCP tool, which performs a Gemini-reviewed commit and only writes the commit if the reviewer returns LGTM. Direct `git commit` invocations from the Bash tool are denied via `.claude/settings.json` to prevent unreviewed commits from slipping in.

The Bash deny rule (`Bash(git commit:*)`) is best-effort defense-in-depth, not a hard sandbox. Be aware of these intentional gaps:

- It does not (and cannot) block commits that go through the `mcp__lgtmcp__review_and_commit` tool. That tool runs inside the lgtmcp Go binary, outside the Bash deny scope, and committing is its entire purpose. This is by design.
- The deny rule matches on the leading command name. It does not match indirect invocations such as `sh -c "git commit ..."` or `bash -c "git commit ..."`. This is a known limitation of the Claude Code permission grammar — the rule cannot reliably inspect arguments to a shell wrapper. Do not use shell indirection to bypass the policy; always go through the MCP tool.

If you ever genuinely need a raw `git commit` (e.g., emergency recovery), make that explicit to the user and get approval first rather than working around the deny rule.

## Overview

LGTMCP is a Model Context Protocol server that reviews code changes using Google Gemini 3.1 Pro and either commits them (if approved) or returns review comments.

**Note**: The `mcp__lgtmcp__` tools may run a different version than this repository. Always test with actual code.

## Quick Start

1. **Setup config**:

   ```bash
   mkdir -p ~/.config/lgtmcp
   cp config.example.yaml ~/.config/lgtmcp/config.yaml
   chmod 600 ~/.config/lgtmcp/config.yaml
   ```

2. **Add to Claude Desktop**:

   ```json
   {
     "mcpServers": {
       "lgtmcp": {
         "command": "lgtmcp",
         "args": []
       }
     }
   }
   ```

3. **Use the tools**:
   - `review_only`: Reviews changes without committing
   - `review_and_commit`: Reviews and commits if approved (LGTM=true)

## Architecture

- **MCP Server** (`pkg/mcp/`) - Protocol implementation using mark3labs/mcp-go
- **Review Engine** (`internal/review/`) - Gemini 3.1 Pro integration with file retrieval
- **Git Operations** (`internal/git/`) - Diff generation, commit management, instruction file discovery
- **Security** (`internal/security/`) - Gitleaks v8 for secret detection
- **Prompts** (`internal/prompts/`) - Customizable review prompts with embedded defaults
- **Progress** (`internal/progress/`) - MCP progress notification support

## Configuration

Config location: `~/.config/lgtmcp/config.yaml` (or `$XDG_CONFIG_HOME/lgtmcp/config.yaml`)

```yaml
google:
  api_key: "your-key" # Or use_adc: true for Application Default Credentials

gemini:
  model: "gemini-3.1-pro-preview"
  fallback_model: "gemini-2.5-pro" # Used when primary model quota is exhausted
  temperature: 0.2

git:
  diff_context_lines: 20

logging:
  level: "info" # debug, info, warn, error

prompts:
  review_prompt_path: "" # Optional custom prompt
```

**Model Fallback**: When the primary model's daily quota is exhausted (HTTP 429 with QuotaFailure), the review automatically falls back to `gemini-2.5-pro`. This is distinct from rate limiting, which retries with backoff.

## Development

```bash
make test    # Run tests
make coverage # Run tests with coverage
make build   # Build binary (VERSION=x.y.z for custom version)
make lint    # Run golangci-lint
make fmt     # Format code with gofumpt
make clean   # Remove build artifacts
make deps    # Install tools and dependencies
```

### Version Information

The binary supports a `--version` flag that displays:

- Version (defaults to "dev", set via `VERSION` during build)
- Git commit hash (automatically detected)
- Dirty status (if working tree has uncommitted changes)
- OS/Architecture

Example: `lgtmcp --version` outputs `lgtmcp version 1.0.0 (ef22d19, darwin/arm64)`

### Tool Management

Go tools (golangci-lint, gofumpt) are managed via the `tool` directive in `go.mod` and invoked with `go tool`. Prettier is managed via npm in `tools/package.json`.

### Key Commands

- **Lint check**: `make lint`
- **Format code**: `make fmt`
- **Run with lefthook**: `lefthook run pre-commit`
- **Code review**: After completing any task:
  1. Run `make lint` to check for lint errors
  2. Run `make test` to ensure all tests pass
  3. Use `mcp__lgtmcp__review_only` to review your changes

### GitHub Workflows

CI workflows in `.github/workflows/`:

- **lint.yaml** - Runs golangci-lint, then zizmor (GitHub Actions static analysis, `pedantic` persona) on push/PR to main
- **test.yaml** - Runs tests with race detection and coverage on push/PR to main

All workflows use pinned action versions with SHA hashes for reproducibility.

The zizmor step runs the official `zizmorcore/zizmor-action` as a plain pass/fail gate: `advanced-security: false` (no SARIF upload, so the job stays `contents: read`) and `online-audits: false` (deterministic, offline — matches local `zizmor --pedantic .`). Renovate bumps the pinned action SHA, which also advances the bundled zizmor version.

The project intentionally runs no SAST scan of the Go source: the CodeQL workflow was removed deliberately and is not being replaced. The zizmor step audits only the GitHub Actions workflow files, not Go code, so it is not a SAST substitute. Reintroduce a Go SAST workflow here if that coverage is wanted again.

## TODO

- [ ] **File size limits** - Prevent excessive Gemini API token usage
- [x] **Token and cost logging** - Log API usage token counts and estimated cost in USD
- [x] **Progress notifications** - MCP progress notifications during review operations
- [x] **Usage stats in response** - Duration, token counts, and cost shown in review response footer
- [x] **AGENTS.md discovery** - Automatically discover and inject AGENTS.md instructions into review prompts
- [x] **REVIEW.md discovery** - Automatically discover and inject REVIEW.md instructions into review prompts
- [x] **Context caching measurement** - Log implicit-cache hit rate and dollar savings; correct cached-token cost accounting

## AGENTS.md and REVIEW.md Support

Repositories can include `AGENTS.md` and/or `REVIEW.md` files with project-specific review guidelines. LGTMCP automatically discovers these files by walking from each changed file's directory up to the repo root. The instructions are injected into both the context-gathering and review prompts.

- Files are deduplicated and sorted root-first (shallowest depth first)
- Symlinks pointing outside the repository are skipped
- Discovery errors are non-fatal (logged as warnings)

## Deleted-File Handling

When the diff contains deleted files, lgtmcp lists them in a dedicated "Files deleted by this change" section in both prompts, and the `get_file_content` tool short-circuits requests for those paths with the dedicated `errDeletedFileMsg` instead of returning a generic ENOENT. The diff already carries the full removed content, so the model has everything it needs without a follow-up fetch. The deletion set comes from `security.ChangedFiles.Deleted` (returned by `ExtractChangedFilesDetailed`) and is threaded through `review.WithDeletedFiles`. Staging still receives the full path list so deletions are committed; the broader stage-time TOCTOU window (re-created files, modification swap) is documented at the `StageFiles` callsite in `pkg/mcp/server.go` and tracked separately.

## New-File Diff Synthesis

Untracked files and initial-commit files have no blob to diff against, so `GetDiff` synthesizes a git-style "new file" block via `writeNewFileDiff` in `internal/git/git.go`. The `new file mode` line reflects the file on disk rather than a hardcoded value, matching what real `git diff` emits:

- `gitFileMode` chooses the mode: `120000` for symlinks (checked first, since a symlink's permission bits often include execute bits), `100755` when a regular file's owner-execute bit is set, else `100644`. This mirrors git's `ce_permissions`, which keys off `0o100` alone and ignores group/other execute bits. On Windows, Go never reports execute bits, so regular files resolve to `100644` (consistent with git's default `core.fileMode=false`).
- `newFileForDiff` supplies the content and mode. For a symlink it returns the link target via `os.Readlink` (which reads only the link text and never dereferences the link, so a target outside the repo is never read) and the symlink mode; escaping or dangling symlinks are therefore surfaced to the reviewer as `120000` entries rather than being silently dropped. Regular files delegate to `readRepoFile`, the shared reader that also backs the public `GetFileContent`/`get_file_content` tool — whose follow-the-symlink-and-reject-escapes security contract is unchanged.
- Newly added **empty** files (e.g. `__init__.py`, `.gitkeep`) are surfaced as a header-only block (`writeNewFileDiff` writes the headers and returns), matching git. The `GetDiff` callsites guard only on the read error, not on empty content, so an empty new file is neither dropped from the review nor (for `review_and_commit`, whose staged-file list is derived from the diff) silently omitted from the commit.

## Context Caching

Gemini 2.5/3.x perform **implicit** context caching automatically (no API setup, no storage cost): when a request shares a long prefix with a recent one, the shared tokens are billed at ~10% of the input rate. Within a single review this fires across Phase 1's tool-calling loop, where the chat resends the growing history (including the diff) on each turn. lgtmcp deliberately does **not** create explicit caches (`Caches.Create`): for a one-review-at-a-time workload the hourly storage floor plus per-cache create overhead exceed the read discount from the handful of reuses a single review generates, so explicit caching would lose money. Implicit caching is free and always on, so there is nothing to configure.

The review measures and reports caching effectiveness (`internal/review/review.go`):

- `tokenUsage.cost` bills `PromptTokens - CachedTokens` at the full input rate and `CachedTokens` at 10%. Gemini reports `PromptTokenCount` as the _total effective_ prompt size, which already includes the cached tokens (per the genai `UsageMetadata` docs), so the cached count is subtracted out rather than added on top — fixing an earlier double-count that overstated cost on a cache hit.
- `costWithoutCaching` is the no-cache baseline (every prompt token at the full rate); `savings` is baseline minus actual; `cacheHitRate` is `CachedTokens / PromptTokens`.
- Every review emits the `Token usage` log with `cost_usd_uncached`, `cache_savings_usd`, `cache_hit_rate`, and `cache_engaged`, plus a plain-language `Context caching` line (`engaged=true/false`) so "did it work / are we saving money" is answerable with one grep. The MCP response footer (`pkg/mcp/server.go`) shows `Cached: N (X% hit, saved $Y)`, or `Cached: 0 (no hit)` when nothing was cached.
- Small diffs below the model's implicit-cache minimum (4096 tokens for `gemini-3.1-pro-preview`) never cache; the `engaged=false` log states that explicitly rather than looking broken.

## Security Features

- **Gitignore Protection**: The `get_file_contents` tool respects `.gitignore` files and refuses access to any ignored files
  - Prevents accidental exposure of sensitive files like `.env`, API keys, secrets
  - Respects nested `.gitignore` files throughout the repository hierarchy
  - Uses `git check-ignore` (via the `git.IsIgnored` helper in `internal/git`) for accurate gitignore rule evaluation; the helper strips inherited `GIT_*` variables so a leaked `GIT_DIR`/`GIT_CONFIG_GLOBAL` cannot redirect the check at another repository

## Technical Choices

- **Go**: Single binary, excellent performance, native git ops
- **mark3labs/mcp-go**: Most mature MCP implementation
- **Gitleaks v8**: MIT licensed, embedded library (no subprocess)
- **Gemini 3.1 Pro Preview**: Advanced reasoning model for code understanding

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

## Troubleshooting

1. **Config errors**: Check `~/.config/lgtmcp/config.yaml` exists and has valid YAML
2. **Not a git repo**: Ensure `.git` directory exists
3. **Secrets detected**: Remove sensitive information from diff
4. **API errors**: Verify API key and network connectivity

## Notes

- Gemini API limitation: Can't use both function calling and Google Search simultaneously
- Gitleaks v8 has library usage limitations (no direct git repo scanning)
- All quality checks passing: zero lint errors, 100% test pass rate
