# LGTMCP Logging Improvements

## Executive Summary

This document describes the logging improvements implemented for lgtmcp as a single-user CLI tool, focusing on practical enhancements for debugging and monitoring without over-engineering for large-scale production scenarios.

## Current State Analysis

### Log Sample Analysis

From `/Users/shields/Library/Logs/lgtmcp/lgtmcp.log`:

```
time=2025-08-22T20:34:37.670-07:00 level=INFO msg="Starting lgtmcp server..."
time=2025-08-22T20:34:37.671-07:00 level=INFO msg="Starting LGTMCP server" version=0.0.0-dev
time=2025-08-22T20:55:29.053-07:00 level=INFO msg="handleReviewAndCommit called" tool=review_and_commit arguments="map[...]"
time=2025-08-22T20:55:29.081-07:00 level=INFO msg="Got diff result" diff_length=48189 error=<nil>
time=2025-08-22T20:56:31.785-07:00 level=DEBUG msg="Raw review response from Gemini" text="{...}"
```

### Critical Issues Identified

1. **Process Management Issues**
   - Multiple duplicate startup messages indicate process spawning issues
   - No process ID (PID) tracking
   - No singleton pattern enforcement

2. **Request Correlation Missing**
   - No request IDs to trace operations across log entries
   - Difficult to debug multi-step operations
   - Can't distinguish concurrent requests

3. **Security Concerns**
   - Sensitive data logged in DEBUG level (raw API responses)
   - Full arguments logged including potential secrets
   - No data masking or redaction

4. **Performance Metrics Absent**
   - No timing information for operations
   - Missing latency measurements for API calls
   - No resource usage tracking

## Implemented Improvements ✅

### 1. Request Correlation IDs

```go
type RequestContext struct {
    RequestID   string        // UUID for each request
    SessionID   string        // MCP session identifier
    StartTime   time.Time     // Request start time
    Repository  string        // Git repository path
    Branch      string        // Current git branch
    User        string        // User identifier (if available)
}
```

**Implementation:**

- ✅ Generate 8-character hex ID for each request
- ✅ Thread request_id through all log entries in a single operation
- ✅ Helps trace multi-step operations (prepare → review → commit)

**Example Output:**

```
time=2025-08-23T10:00:00.000Z level=INFO msg="Review request started" request_id=abc-123 session_id=xyz-789 repo=/path/to/repo branch=main
time=2025-08-23T10:00:00.100Z level=INFO msg="Git diff generated" request_id=abc-123 diff_size=48189 duration_ms=100
time=2025-08-23T10:00:02.500Z level=INFO msg="Gemini review complete" request_id=abc-123 duration_ms=2400 approved=true
```

### 2. Timing Information

**Implemented:**

- ✅ Git diff duration tracking
- ✅ Security scan duration
- ✅ Gemini API call timing
- ✅ Stage and commit operation timing
- ✅ Total request duration

**Example Output:**

```
time=2025-08-23T10:00:00.000Z level=INFO msg="Operation completed" request_id=abc-123 operation=gemini_review duration_ms=2400 success=true tokens_used=15234
```

### 3. Repository Context

**Implemented:**

- ✅ Repository name in log entries
- ✅ Changed files count
- ✅ Diff size tracking
- ✅ Consistent structured fields for slog

### 4. Sensitive Data Protection

**Implemented:**

- ✅ Mask API keys and tokens in logs
- ✅ Replace map arguments with `map[...]` to avoid logging values
- ✅ Simple pattern-based masking function

### 5. Improved Error Context

**Implemented:**

- ✅ Error logging with request context
- ✅ Duration tracking on failures
- ✅ Early return detection and logging

## Example Output with Improvements

```
2025-08-23T10:00:00.000Z INFO Review request started request_id=1a2b3c4d tool=review_only
2025-08-23T10:00:00.010Z INFO Processing repository request_id=1a2b3c4d repo=lgtmcp
2025-08-23T10:00:00.100Z INFO Git diff completed repo=lgtmcp diff_size=48189 duration_ms=90
2025-08-23T10:00:00.200Z INFO Security scan completed duration_ms=95 findings=0
2025-08-23T10:00:00.300Z INFO Starting Gemini review repo=lgtmcp changed_files=5 diff_size=48189
2025-08-23T10:00:02.700Z INFO Gemini review completed duration_ms=2400 approved=true
2025-08-23T10:00:02.800Z INFO Review completed request_id=1a2b3c4d approved=true total_duration_ms=2800
```

## Future Improvements (If Needed)

These improvements are intentionally NOT implemented as they would over-complicate a simple single-user CLI tool:

### Log Rotation

- Not needed for single-user CLI with stdio transport
- OS/user can manage log files if needed
- Could use `lumberjack` if logs grow too large

### Metrics Export

- Prometheus/OpenTelemetry would be overkill
- Current timing logs are sufficient for debugging
- Could add simple metrics file if needed

### Advanced Debug Mode

- Current debug level logging is sufficient
- Can increase verbosity with existing log levels
- Separate debug files would complicate single-user setup

### Branch Tracking

- Could add git branch to logs if useful
- Not critical for current use case
- Easy to add if needed: `git symbolic-ref --short HEAD`

## Current Configuration

The existing configuration is sufficient:

```yaml
logging:
  level: "info" # debug, info, warn, error
  output: "directory" # Or stdout/stderr for debugging
  directory: "~/.local/share/lgtmcp/logs" # Optional
```

## Benefits Achieved

1. **Request Traceability**: Can follow a request through all operations
2. **Performance Visibility**: Clear timing for each operation phase
3. **Debugging**: Much easier to identify slow operations or failures
4. **Security**: No sensitive data leaked in logs
5. **Simplicity**: No complex infrastructure needed

## Conclusion

The implemented logging improvements provide practical benefits for a single-user CLI tool without over-engineering. The changes focus on what's actually useful: request tracing, timing information, and sensitive data protection. More complex features are documented as future possibilities but intentionally not implemented to maintain simplicity.
