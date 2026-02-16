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

# LGTMCP

A Model Context Protocol (MCP) server that provides AI-powered code review using
Google Gemini 3 Pro. LGTMCP reviews your code changes and either commits them
automatically (if approved) or provides detailed feedback for improvements.

## Features

- **AI Code Review**: Leverages Google Gemini 3 Pro for intelligent code analysis
- **Automatic Commit**: Commits changes when code passes review (optional)
- **Security Scanning**: Built-in secret detection using Gitleaks
- **Gitignore Protection**: Prevents access to gitignored files during review
- **MCP Integration**: Works seamlessly with Claude Desktop and other MCP clients
- **Review-Only Mode**: Option to get feedback without automatic commits

## Installation

### Build from source

```bash
git clone https://msrl.dev/lgtmcp.git
cd lgtmcp
make build
```

### Install to ~/bin

```bash
make install
```

This installs the binary to `~/bin` by default. You can customize the installation directory:

```bash
make install INSTALL_PATH=/usr/local/bin
```

**Note**: Ensure `~/bin` is in your shell's `PATH`. Add this to your shell configuration file if needed:

```bash
# For bash/zsh
export PATH="$HOME/bin:$PATH"
```

### Configuration

1. Get a Google API key from [Google AI Studio](https://aistudio.google.com/apikey).

2. Create configuration directory:

   ```bash
   mkdir -p ~/.config/lgtmcp
   ```

3. Create configuration file from example:

   ```bash
   cp config.example.yaml ~/.config/lgtmcp/config.yaml
   ```

4. Edit the configuration file with your settings:

   ```yaml
   google:
     api_key: "your-gemini-api-key-here"
   gemini:
     model: "gemini-3-pro-preview"
     fallback_model: "gemini-2.5-pro" # Default; set to "none" to disable
   logging:
     level: "info"
   ```

The `fallback_model` is use when we run into quota exhaustion on the primary
model. While Gemini 3 Pro is in preview, it has very low daily rate limits.

### Claude Code configuration

1. Set up configuration file as described above

2. Configure LGTMCP with Claude Code:

```bash
claude mcp add lgtmcp -- lgtmcp
```

## Usage

### Basic Usage

The MCP server exposes two tools:

#### `review_only`

Reviews code changes and returns feedback without committing.

**Parameters:**

- `directory`: Path to the git repository

#### `review_and_commit`

Reviews code changes and commits if approved. This is a separate tool so that
you can set tool permissions on it differently from `review`.

**Parameters:**

- `directory`: Path to the git repository
- `commit_message`: Message for the commit if approved

### Example Workflows

**Review only (no commit):**

```
review_only("/path/to/repo")
```

**Review and commit if approved:**

```
review_and_commit("/path/to/repo", "Add new feature")
```

### What Happens

1. **Security check**: Scans files for secrets using Gitleaks
2. **Diff generation**: Creates diff of all staged and unstaged changes
3. **AI review**: Sends diff to Gemini 3 Pro for analysis
   - Gemini can request file contents for context
   - Gitignored files are automatically blocked from access
4. **Decision**:
   - If approved (LGTM): Returns approval message (`review_only`) or commits changes (`review_and_commit`)
   - If not approved: Returns detailed feedback

## Configuration

All configuration is managed through the YAML configuration file located at:

- `$XDG_CONFIG_HOME/lgtmcp/config.yaml` (if XDG_CONFIG_HOME is set)
- `~/.config/lgtmcp/config.yaml` (default)

See `config.example.yaml` for all available configuration options.

## Logging

LGTMCP logs are written to platform-specific default locations:

- **macOS**: `~/Library/Logs/lgtmcp/lgtmcp.log`
- **Linux**: `~/.local/share/lgtmcp/logs/lgtmcp.log` (or `$XDG_DATA_HOME/lgtmcp/logs/lgtmcp.log`)
- **Windows**: `%LOCALAPPDATA%\lgtmcp\logs\lgtmcp.log`

You can configure logging in your `config.yaml`:

```yaml
logging:
  output: "directory" # Options: none, stdout, stderr, directory, mcp
  level: "info" # Options: debug, info, warn, error
  # directory: "/custom/log/path"  # Optional custom directory
```

To view logs on macOS:

```bash
# View the log file
tail -f ~/Library/Logs/lgtmcp/lgtmcp.log

# Or open in Console.app
open ~/Library/Logs/lgtmcp/lgtmcp.log
```

## Development

### Building

```bash
make build
```

### Testing

```bash
make test
```

### Linting

```bash
make lint
```

### Coverage

```bash
make coverage
```

## Troubleshooting

**"Not a git repository" error**

- Ensure you're in a git repository with a `.git` directory

**"Secrets detected" error**

- Review and remove any exposed secrets from your changes

**"Gemini API error"**

- Verify your API key is valid and has quota remaining
- Check network connectivity

**"No changes to review"**

- Make sure you have staged or unstaged changes in your repository
