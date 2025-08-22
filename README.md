# LGTMCP

A Model Context Protocol (MCP) server that provides AI-powered code review using
Google Gemini 2.5 Pro. LGTMCP reviews your code changes and either commits them
automatically (if approved) or provides detailed feedback for improvements.

## Features

- **AI Code Review**: Leverages Google Gemini 2.5 Pro for intelligent code analysis
- **Automatic Commit**: Commits changes when code passes review (optional)
- **Security Scanning**: Built-in secret detection using Gitleaks
- **MCP Integration**: Works seamlessly with Claude Desktop and other MCP clients
- **Review-Only Mode**: Option to get feedback without automatic commits

## Installation

### Build from source

```bash
git clone https://github.com/shields/lgtmcp.git
cd lgtmcp
make build
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
     model: "gemini-2.5-pro"
   logging:
     level: "info"
   ```

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
3. **AI review**: Sends diff to Gemini 2.5 Pro for analysis
4. **Decision**:
   - If approved (LGTM): Returns approval message (`review_only`) or commits changes (`review_and_commit`)
   - If not approved: Returns detailed feedback

## Configuration

All configuration is managed through the YAML configuration file located at:

- `$XDG_CONFIG_HOME/lgtmcp/config.yaml` (if XDG_CONFIG_HOME is set)
- `~/.config/lgtmcp/config.yaml` (default)

See `config.example.yaml` for all available configuration options.

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
