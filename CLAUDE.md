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

**IMPORTANT**: Update this file after completing any task to document changes.

## Overview

LGTMCP is a Model Context Protocol server that reviews code changes using Google Gemini 2.5 Pro and either commits them (if approved) or returns review comments.

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
- **Review Engine** (`internal/review/`) - Gemini 2.5 Pro integration with file retrieval
- **Git Operations** (`internal/git/`) - Diff generation and commit management
- **Security** (`internal/security/`) - Gitleaks v8 for secret detection
- **Prompts** (`internal/prompts/`) - Customizable review prompts with embedded defaults

## Configuration

Config location: `~/.config/lgtmcp/config.yaml` (or `$XDG_CONFIG_HOME/lgtmcp/config.yaml`)

```yaml
google:
  api_key: "your-key" # Or use_adc: true for Application Default Credentials

gemini:
  model: "gemini-2.5-pro"
  temperature: 0.2

git:
  diff_context_lines: 20

logging:
  level: "info" # debug, info, warn, error

prompts:
  review_prompt_path: "" # Optional custom prompt
```

## Development

```bash
make test    # Run tests (71.8% coverage)
make build   # Build binary
make lint    # Run golangci-lint
make fmt     # Format code
```

### Key Commands

- **Lint check**: `bin/golangci-lint run`
- **Run with lefthook**: `lefthook run pre-commit`

## Testing Coverage

- **Overall**: 71.8% (practical maximum)
- **internal/git**: 87.3%
- **internal/security**: 98.5%
- **pkg/mcp**: 66.7%
- **internal/review**: 56.3% (requires real API credentials)

## TODO

- [ ] **File size limits** - Prevent excessive Gemini API token usage
- [ ] **Cost tracking** - Log API usage costs and token counts

## Security Features

- **Gitignore Protection**: The `get_file_contents` tool respects `.gitignore` files and refuses access to any ignored files
  - Prevents accidental exposure of sensitive files like `.env`, API keys, secrets
  - Respects nested `.gitignore` files throughout the repository hierarchy
  - Uses `git check-ignore` for accurate gitignore rule evaluation

## Technical Choices

- **Go**: Single binary, excellent performance, native git ops
- **mark3labs/mcp-go**: Most mature MCP implementation
- **Gitleaks v8**: MIT licensed, embedded library (no subprocess)
- **Gemini 2.5 Pro**: Latest stable model with excellent code understanding

## Troubleshooting

1. **Config errors**: Check `~/.config/lgtmcp/config.yaml` exists and has valid YAML
2. **Not a git repo**: Ensure `.git` directory exists
3. **Secrets detected**: Remove sensitive information from diff
4. **API errors**: Verify API key and network connectivity

## Notes

- Gemini API limitation: Can't use both function calling and Google Search simultaneously
- Gitleaks v8 has library usage limitations (no direct git repo scanning)
- All quality checks passing: zero lint errors, 100% test pass rate
