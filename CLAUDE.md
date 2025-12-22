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

## Overview

LGTMCP is a Model Context Protocol server that reviews code changes using Google Gemini 3 Pro and either commits them (if approved) or returns review comments.

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
- **Review Engine** (`internal/review/`) - Gemini 3 Pro integration with file retrieval
- **Git Operations** (`internal/git/`) - Diff generation and commit management
- **Security** (`internal/security/`) - Gitleaks v8 for secret detection
- **Prompts** (`internal/prompts/`) - Customizable review prompts with embedded defaults

## Configuration

Config location: `~/.config/lgtmcp/config.yaml` (or `$XDG_CONFIG_HOME/lgtmcp/config.yaml`)

```yaml
google:
  api_key: "your-key" # Or use_adc: true for Application Default Credentials

gemini:
  model: "gemini-3-pro-preview"
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
make coverage # Run tests with coverage (77.4%)
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

Tools (golangci-lint v2.4.0, gofumpt v0.8.0) are managed separately in `tools/` directory:

- `tools/go.mod` - Isolated tool dependencies
- `tools/tools.go` - Tool imports for version pinning
- Makefile uses `go -C tools` for tool installation

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

- **lint.yaml** - Runs golangci-lint on push/PR to main
- **test.yaml** - Runs tests with race detection and coverage on push/PR to main
- **codeql.yaml** - CodeQL security scanning for Go (push/PR to main)

All workflows use pinned action versions with SHA hashes for reproducibility.

## Testing Coverage

- **Overall**: 77.4% (well above 70% threshold)
- **internal/git**: 86.4%
- **internal/security**: 97.4%
- **pkg/mcp**: 73.4%
- **internal/review**: 80.4%
- **internal/config**: 69.7%
- **internal/prompts**: 88.1%
- **internal/logging**: 70.2%

## TODO

- [ ] **File size limits** - Prevent excessive Gemini API token usage
- [x] **Token and cost logging** - Log API usage token counts and estimated cost in USD

## Security Features

- **Gitignore Protection**: The `get_file_contents` tool respects `.gitignore` files and refuses access to any ignored files
  - Prevents accidental exposure of sensitive files like `.env`, API keys, secrets
  - Respects nested `.gitignore` files throughout the repository hierarchy
  - Uses `git check-ignore` for accurate gitignore rule evaluation

## Technical Choices

- **Go**: Single binary, excellent performance, native git ops
- **mark3labs/mcp-go**: Most mature MCP implementation
- **Gitleaks v8**: MIT licensed, embedded library (no subprocess)
- **Gemini 3 Pro Preview**: Advanced reasoning model for code understanding

## Troubleshooting

1. **Config errors**: Check `~/.config/lgtmcp/config.yaml` exists and has valid YAML
2. **Not a git repo**: Ensure `.git` directory exists
3. **Secrets detected**: Remove sensitive information from diff
4. **API errors**: Verify API key and network connectivity

## Notes

- Gemini API limitation: Can't use both function calling and Google Search simultaneously
- Gitleaks v8 has library usage limitations (no direct git repo scanning)
- All quality checks passing: zero lint errors, 100% test pass rate
