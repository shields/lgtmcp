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

// Package git provides Git repository operations.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"msrl.dev/lgtmcp/internal/config"
)

const (
	// gitCommandTimeout is the maximum duration for a git command to complete.
	gitCommandTimeout = 30 * time.Second
)

var (
	// ErrNotGitRepo indicates the directory is not a git repository.
	ErrNotGitRepo = errors.New("directory is not a git repository")
	// ErrNoChanges indicates there are no changes to commit.
	ErrNoChanges = errors.New("no changes to commit")
	// ErrCommandFailed indicates a git command failed to execute.
	ErrCommandFailed = errors.New("git command failed")
	// ErrInvalidPath indicates an invalid file path was provided.
	ErrInvalidPath = errors.New("invalid path")
	// ErrEmptyCommitMsg indicates the commit message is empty.
	ErrEmptyCommitMsg = errors.New("commit message cannot be empty")
	// ErrFileNotFound indicates the requested file was not found.
	ErrFileNotFound = errors.New("file not found")
	// ErrCommandTimeout indicates a git command timed out.
	ErrCommandTimeout = errors.New("git command timed out")
	// ErrPathOutsideRepo indicates the path is outside the repository.
	ErrPathOutsideRepo = errors.New("path is outside repository")
	// ErrNotRegularFile indicates the path is not a regular file.
	ErrNotRegularFile = errors.New("not a regular file")
)

// Git provides git repository operations.
type Git struct {
	repoPath         string
	diffContextLines int
}

// New creates a new Git instance for the given repository path.
func New(repoPath string, cfg *config.GitConfig) (*Git, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	if !isGitRepo(absPath) {
		return nil, ErrNotGitRepo
	}

	// Default to 20 lines of context if not specified
	contextLines := 20
	if cfg != nil && cfg.DiffContextLines != nil {
		contextLines = *cfg.DiffContextLines
	}

	return &Git{
		repoPath:         absPath,
		diffContextLines: contextLines,
	}, nil
}

// GetDiff returns the diff of all changes in the repository.
func (g *Git) GetDiff(ctx context.Context) (string, error) {
	// Check if this is an initial commit (no HEAD exists). --verify --quiet
	// makes the check precise: exit 0 means HEAD resolves, exit 1 means it
	// does not (unborn branch). Anything else — a real git failure, not an
	// initial commit — must surface as an error rather than mislabel the
	// whole repository as new files.
	res, err := runGit(ctx, g.repoPath, nil, nil, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to check for HEAD: %w", err)
	}
	if res.exitCode != 0 && res.exitCode != 1 {
		msg := strings.TrimSpace(res.stderr)
		if msg == "" {
			msg = fmt.Sprintf("exit status %d", res.exitCode)
		}

		return "", fmt.Errorf("failed to check for HEAD: %w: %s", ErrCommandFailed, msg)
	}
	isInitialCommit := res.exitCode == 1

	var diff string

	if isInitialCommit {
		// For initial commit, get all files and show them as new. The -z flag
		// yields raw NUL-terminated paths; without it git C-quotes names with
		// special or non-ASCII bytes (per core.quotePath), and the quoted form
		// would fail the stat in newFileForDiff and silently drop the file.
		files, filesErr := g.runGitCommand(ctx, "ls-files", "-z", "--others", "--exclude-standard")
		if filesErr != nil {
			return "", fmt.Errorf("failed to get files for initial commit: %w", filesErr)
		}

		// Also check for any files that might be staged.
		stagedFiles, stageErr := g.runGitCommand(ctx, "diff", "--cached", "--name-only", "-z")
		if stageErr != nil {
			// Ignore error, just use untracked files.
			stagedFiles = ""
		}
		// Every entry is NUL-terminated, so plain concatenation is safe.
		files += stagedFiles

		if files == "" {
			return "", ErrNoChanges
		}

		var diffOutput bytes.Buffer
		uniqueFiles := make(map[string]bool)
		for file := range strings.SplitSeq(files, "\x00") {
			if file != "" && !uniqueFiles[file] {
				uniqueFiles[file] = true
				content, mode, contentErr := g.newFileForDiff(file)
				if contentErr == nil {
					writeNewFileDiff(&diffOutput, file, content, mode)
				}
			}
		}
		diff = diffOutput.String()
	} else {
		// Normal case: diff between HEAD and working directory (including untracked files).
		// This shows all changes regardless of staging status.
		// Use the configured context lines (default 20).
		// Pin output to a parseable unified diff regardless of user git config:
		// force canonical a/ and b/ prefixes (diff.mnemonicPrefix would emit
		// c/ and w/), disable external diff drivers (diff.external replaces
		// the unified format with arbitrary tool output), and disable color
		// (color.diff=always would inject ANSI escapes).
		contextFlag := fmt.Sprintf("--unified=%d", g.diffContextLines)
		diff, err = g.runGitCommand(ctx, "diff", contextFlag, "--no-color", "--no-ext-diff",
			"--src-prefix=a/", "--dst-prefix=b/", "HEAD", "--", ".")
		if err != nil {
			return "", fmt.Errorf("failed to get diff against HEAD: %w", err)
		}

		// Also include untracked files. The -z flag yields raw NUL-terminated
		// paths; the default C-quoting of special or non-ASCII names (per
		// core.quotePath) would fail the stat in newFileForDiff and silently
		// drop the file from the diff.
		untrackedFiles, err := g.runGitCommand(ctx, "ls-files", "-z", "--others", "--exclude-standard")
		if err != nil {
			return "", fmt.Errorf("failed to get untracked files: %w", err)
		}

		if untrackedFiles != "" {
			var untrackedDiff bytes.Buffer
			for file := range strings.SplitSeq(untrackedFiles, "\x00") {
				if file != "" {
					content, mode, err := g.newFileForDiff(file)
					if err == nil {
						writeNewFileDiff(&untrackedDiff, file, content, mode)
					}
				}
			}
			// Append the synthesized blocks directly: git emits file blocks
			// back to back, and a separating blank line would be a stray
			// non-diff line in the output.
			diff += untrackedDiff.String()
		}
	}

	if diff == "" {
		return "", ErrNoChanges
	}

	return diff, nil
}

// binaryDetectionLimit is how many leading bytes are searched for a NUL to
// classify content as binary, matching git's FIRST_FEW_BYTES heuristic.
const binaryDetectionLimit = 8000

// writeNewFileDiff renders content as a synthetic "new file" diff block (used
// for untracked files and initial commits, which have no blob to diff against),
// mirroring git's own rendering: a "new file mode" line whose value reflects the
// file on disk (see gitFileMode), a "@@ -0,0 +1,N @@" hunk header (",N" omitted
// for a single line, as git does), one "+" line per added line, and a
// "\ No newline at end of file" marker when the content does not end in a
// newline. Paths are C-quoted via gitQuotePath when they contain special bytes,
// and the "+++" line gets git's trailing tab when the rendered path contains a
// space, so patch parsers can find where the filename ends.
// A single trailing newline is treated as the terminator of the last line, not
// as content, so a file ending in "\n" does not gain a phantom empty added line
// (content ending in "\n\n" keeps a genuine blank final line). An empty file
// yields only the header lines and no hunk, also matching git, so a newly added
// empty file is still surfaced to the reviewer rather than dropped. Binary
// content (a NUL among the leading bytes, git's heuristic) yields a "Binary
// files ... differ" line instead of hunks of raw bytes; secret scanning is
// unaffected, since it reads file contents from disk rather than from the diff.
func writeNewFileDiff(buf *bytes.Buffer, file, content string, mode os.FileMode) {
	_, _ = fmt.Fprintf(buf, "diff --git %s %s\n", gitQuotePath("a/", file), gitQuotePath("b/", file))
	_, _ = fmt.Fprintf(buf, "new file mode %s\n", gitFileMode(mode))
	if content == "" {
		return
	}
	if strings.Contains(content[:min(len(content), binaryDetectionLimit)], "\x00") {
		_, _ = fmt.Fprintf(buf, "Binary files /dev/null and %s differ\n", gitQuotePath("b/", file))

		return
	}
	_, _ = buf.WriteString("--- /dev/null\n")
	// git appends a tab to a "---"/"+++" line whose rendered path contains a
	// literal space — even inside a C-quoted form — so that patch parsers can
	// find where the filename ends. "--- /dev/null" never contains one.
	bPath := gitQuotePath("b/", file)
	if strings.Contains(bPath, " ") {
		bPath += "\t"
	}
	_, _ = fmt.Fprintf(buf, "+++ %s\n", bPath)

	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if len(lines) == 1 {
		_, _ = buf.WriteString("@@ -0,0 +1 @@\n")
	} else {
		_, _ = fmt.Fprintf(buf, "@@ -0,0 +1,%d @@\n", len(lines))
	}
	for _, line := range lines {
		_, _ = fmt.Fprintf(buf, "+%s\n", line)
	}
	if !strings.HasSuffix(content, "\n") {
		_, _ = buf.WriteString("\\ No newline at end of file\n")
	}
}

// gitQuotePath renders a prefixed path ("a/<file>" or "b/<file>") for a
// synthesized diff header the way git's quote_c_style does with the default
// core.quotePath=true: if the path contains a double quote, backslash, control
// character, or non-ASCII byte, the whole prefixed path is wrapped in double
// quotes with those bytes escaped (named C escapes where git has them, octal
// otherwise). Quoting keeps hostile filenames — e.g. one containing a newline —
// from corrupting or spoofing header lines, and the diff parser in
// internal/security already unquotes this form from real git output.
func gitQuotePath(prefix, path string) string {
	if !gitPathNeedsQuoting(path) {
		return prefix + path
	}

	var b strings.Builder
	_ = b.WriteByte('"')
	_, _ = b.WriteString(prefix)
	for i := range len(path) {
		c := path[i]
		switch esc, ok := gitPathEscapes[c]; {
		case ok:
			_, _ = b.WriteString(esc)
		case c < 0x20 || c >= 0x7f:
			_, _ = fmt.Fprintf(&b, `\%03o`, c)
		default:
			_ = b.WriteByte(c)
		}
	}
	_ = b.WriteByte('"')

	return b.String()
}

// gitPathEscapes holds the named C escapes from git's quote.c table; bytes
// that need quoting but have no named escape are rendered as octal instead.
var gitPathEscapes = map[byte]string{
	'\a': `\a`, '\b': `\b`, '\f': `\f`, '\n': `\n`, '\r': `\r`,
	'\t': `\t`, '\v': `\v`, '"': `\"`, '\\': `\\`,
}

// gitPathNeedsQuoting reports whether path contains a byte that git's default
// quoting (core.quotePath=true) escapes: control characters, DEL, double
// quote, backslash, or any non-ASCII byte.
func gitPathNeedsQuoting(path string) bool {
	for i := range len(path) {
		if c := path[i]; c < 0x20 || c >= 0x7f || c == '"' || c == '\\' {
			return true
		}
	}

	return false
}

// gitFileMode renders the git index mode for a synthesized new-file block.
// Symlinks are 120000 (checked first, since a symlink's permission bits often
// include execute bits). A regular file is 100755 when its owner-execute bit is
// set and 100644 otherwise, mirroring git's ce_permissions, which keys off 0o100
// alone and ignores group/other execute bits. On Windows, Go never reports
// execute bits, so regular files resolve to 100644 — matching git's default
// core.fileMode=false there.
func gitFileMode(mode os.FileMode) string {
	switch {
	case mode&os.ModeSymlink != 0:
		return "120000"
	case mode&0o100 != 0:
		return "100755"
	default:
		return "100644"
	}
}

// StageAll stages all changes in the repository.
func (g *Git) StageAll(ctx context.Context) error {
	_, err := g.runGitCommand(ctx, "add", "-A")
	if err != nil {
		return fmt.Errorf("failed to stage all changes: %w", err)
	}

	return nil
}

// StageFiles stages only the specified files (additions, modifications, and
// deletions). Limiting staging to a known list avoids picking up files that
// appeared in the working directory after the security scan but before commit.
//
// Paths that exist in neither the working tree nor the index but are present
// in HEAD are skipped: such a path is an already-staged deletion (e.g. the
// source of a fully staged "git mv") with nothing left to stage, and
// "git add" would otherwise fail fatally on a pathspec that matches nothing.
// A path unknown to the working tree, the index, and HEAD is an error.
func (g *Git) StageFiles(ctx context.Context, files []string) error {
	if len(files) == 0 {
		return nil
	}

	if slices.Contains(files, "") {
		return fmt.Errorf("%w: empty path", ErrInvalidPath)
	}

	files, err := g.stageablePaths(ctx, files)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}

	// Pass paths via stdin (NUL-separated) so we never hit ARG_MAX, and set
	// GIT_LITERAL_PATHSPECS=1 so wildcard-named files like "*" are treated as
	// literal paths instead of globs that could match unscanned content.
	var stdin bytes.Buffer
	for _, f := range files {
		_, _ = stdin.WriteString(f)
		_ = stdin.WriteByte(0)
	}

	if _, err := g.runGitCommandStdin(
		ctx, &stdin, []string{"GIT_LITERAL_PATHSPECS=1"},
		"add", "-A", "--pathspec-from-file=-", "--pathspec-file-nul",
	); err != nil {
		return fmt.Errorf("failed to stage files: %w", err)
	}

	return nil
}

// Commit creates a commit with the given message.
func (g *Git) Commit(ctx context.Context, message string) (string, error) {
	if message == "" {
		return "", ErrEmptyCommitMsg
	}

	// Check if there are changes to commit.
	status, err := g.runGitCommand(ctx, "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("failed to get status: %w", err)
	}

	if status == "" {
		return "", ErrNoChanges
	}

	// Commit with the provided message.
	if _, commitErr := g.runGitCommand(ctx, "commit", "-m", message); commitErr != nil {
		return "", fmt.Errorf("failed to commit: %w", commitErr)
	}

	// Get the commit hash.
	hash, err := g.runGitCommand(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to get commit hash: %w", err)
	}

	return strings.TrimSpace(hash), nil
}

// GetFileContent returns the content of a file at the given relative path.
func (g *Git) GetFileContent(_ context.Context, relativePath string) (string, error) {
	content, _, err := g.readRepoFile(relativePath)

	return content, err
}

// GetRepoPath returns the absolute path to the repository.
func (g *Git) GetRepoPath() string {
	return g.repoPath
}

// repoPathFor joins a repo-relative path onto the repository root and verifies
// it stays within the repo lexically — before any symlink resolution. It rejects
// absolute paths and paths that escape the repo (e.g. via ".."). The returned
// path is not guaranteed to exist; callers stat it as needed.
func (g *Git) repoPathFor(relativePath string) (string, error) {
	// Reject absolute paths.
	if filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("%w: absolute paths not allowed", ErrInvalidPath)
	}

	// Clean the path and construct the full path under the repo root.
	fullPath := filepath.Join(g.repoPath, filepath.Clean(relativePath))

	// Security check: ensure the path is within the repo.
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	// Ensure the path is within the repo by checking it starts with repo path + separator.
	// This prevents accessing sibling directories with the same prefix.
	if !strings.HasPrefix(absPath, g.repoPath+string(filepath.Separator)) && absPath != g.repoPath {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideRepo, relativePath)
	}

	return fullPath, nil
}

// stageablePaths returns the subset of files with something left to stage:
// paths present in the working tree (additions, modifications) or in the
// index (deletions not yet staged). A path present only in HEAD is an
// already-staged deletion (e.g. the source of a fully staged "git mv") with
// nothing left to stage, and is dropped. A path in none of the three is
// unknown to git — a parsing bug or a file that vanished mid-review — and is
// an error, keeping staging fail-closed. The index and HEAD listings are
// fetched lazily, only once some path is missing from the working tree.
func (g *Git) stageablePaths(ctx context.Context, files []string) ([]string, error) {
	var keep []string
	var indexed, inHead map[string]bool
	for _, f := range files {
		fullPath, err := g.repoPathFor(f)
		if err != nil {
			return nil, err
		}
		if _, lstatErr := os.Lstat(fullPath); lstatErr == nil {
			keep = append(keep, f)

			continue
		} else if !os.IsNotExist(lstatErr) {
			return nil, fmt.Errorf("failed to stat %s: %w", f, lstatErr)
		}
		if indexed == nil {
			if indexed, err = g.listedPaths(ctx, "ls-files", "--cached", "-z"); err != nil {
				return nil, fmt.Errorf("failed to list index: %w", err)
			}
		}
		if indexed[f] {
			keep = append(keep, f)

			continue
		}
		if inHead == nil {
			if inHead, err = g.listedPaths(ctx, "ls-tree", "-r", "-z", "--name-only", "HEAD"); err != nil {
				return nil, fmt.Errorf("failed to list HEAD: %w", err)
			}
		}
		if !inHead[f] {
			return nil, fmt.Errorf("%w: %s: not in working tree, index, or HEAD", ErrFileNotFound, f)
		}
	}

	return keep, nil
}

// listedPaths runs a git command that emits NUL-separated paths and returns
// them as a set.
func (g *Git) listedPaths(ctx context.Context, args ...string) (map[string]bool, error) {
	out, err := g.runGitCommand(ctx, args...)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool)
	for p := range strings.SplitSeq(out, "\x00") {
		if p != "" {
			set[p] = true
		}
	}

	return set, nil
}

// readRepoFile reads a repo-relative file and returns its content together with
// the mode of the resolved file. Symlinks are followed and the full chain is
// resolved so a link cannot escape the repository; the final target must be a
// regular file. This is the security-sensitive reader backing the MCP
// get_file_content tool, which must never expose files outside the repo.
func (g *Git) readRepoFile(relativePath string) (string, os.FileMode, error) {
	fullPath, err := g.repoPathFor(relativePath)
	if err != nil {
		return "", 0, err
	}

	// Check if file exists (use Lstat to not follow symlinks).
	if _, lstatErr := os.Lstat(fullPath); lstatErr != nil {
		if os.IsNotExist(lstatErr) {
			return "", 0, fmt.Errorf("%w: %s", ErrFileNotFound, relativePath)
		}

		return "", 0, fmt.Errorf("failed to stat file: %w", lstatErr)
	}

	// Resolve the full symlink chain to catch multi-hop symlinks and
	// directory-level symlinks that might escape the repo. Compare against
	// the canonicalized repo path so system-level symlinks (e.g. macOS
	// /var -> /private/var) don't cause false negatives. This mirrors the
	// pattern in instructions.go and intentionally uses HasPrefix with an
	// explicit separator: a git repository cannot be the filesystem root
	// (New() requires a .git entry under repoPath), so the "//" edge case
	// does not arise in practice.
	canonicalRepo, err := filepath.EvalSymlinks(g.repoPath)
	if err != nil {
		return "", 0, fmt.Errorf("failed to resolve repo path: %w", err)
	}

	resolved, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return "", 0, fmt.Errorf("failed to resolve path: %w", err)
	}

	if !strings.HasPrefix(resolved, canonicalRepo+string(filepath.Separator)) && resolved != canonicalRepo {
		return "", 0, fmt.Errorf("%w: %s", ErrPathOutsideRepo, relativePath)
	}

	// After resolution, verify it's a regular file (not a directory, device, etc.).
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		return "", 0, fmt.Errorf("failed to stat resolved file: %w", err)
	}
	if !resolvedInfo.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%w: %s", ErrNotRegularFile, relativePath)
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", 0, fmt.Errorf("failed to read file: %w", err)
	}

	return string(content), resolvedInfo.Mode(), nil
}

// newFileForDiff returns the content and mode used to synthesize a "new file"
// diff block for a repo-relative path. A symlink yields its target text — via
// os.Readlink, which does not dereference the link, so the contents of a target
// outside the repo are never read — and the symlink mode, matching how git
// records symlinks and ensuring escaping or dangling symlinks are surfaced to
// the reviewer rather than silently dropped. Regular files delegate to
// readRepoFile, returning their content and a 100644/100755 mode.
func (g *Git) newFileForDiff(relativePath string) (string, os.FileMode, error) {
	fullPath, err := g.repoPathFor(relativePath)
	if err != nil {
		return "", 0, err
	}

	info, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, fmt.Errorf("%w: %s", ErrFileNotFound, relativePath)
		}

		return "", 0, fmt.Errorf("failed to stat file: %w", err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, linkErr := os.Readlink(fullPath)
		if linkErr != nil {
			return "", 0, fmt.Errorf("failed to read symlink: %w", linkErr)
		}

		return target, info.Mode(), nil
	}

	return g.readRepoFile(relativePath)
}

func (g *Git) runGitCommand(ctx context.Context, args ...string) (string, error) {
	return g.runGitCommandStdin(ctx, nil, nil, args...)
}

func (g *Git) runGitCommandStdin(
	ctx context.Context, stdin io.Reader, extraEnv []string, args ...string,
) (string, error) {
	res, err := runGit(ctx, g.repoPath, stdin, extraEnv, args...)
	if err != nil {
		if errors.Is(err, ErrCommandTimeout) {
			return "", err
		}
		errMsg := res.stderr
		if errMsg == "" {
			errMsg = err.Error()
		}

		return "", fmt.Errorf("%w: %s", ErrCommandFailed, errMsg)
	}
	if res.exitCode != 0 {
		errMsg := res.stderr
		if errMsg == "" {
			errMsg = fmt.Sprintf("exit status %d", res.exitCode)
		}

		return "", fmt.Errorf("%w: %s", ErrCommandFailed, errMsg)
	}

	return res.stdout, nil
}

// IsIgnored reports whether relativePath is ignored by git in the repository at
// repoPath. It shells out to `git check-ignore`, which honors the full ignore
// ruleset — nested .gitignore files, negations, core.excludesFile, and
// .git/info/exclude — that an in-process matcher would not faithfully reproduce.
// The result gates whether file contents are exposed to the model, so any error
// is returned to the caller to fail closed.
//
// The "--" separator stops git from treating a path that begins with "-" as an
// option. Routing through runGit strips inherited GIT_* variables (e.g. GIT_DIR
// or GIT_CONFIG_GLOBAL) that would otherwise redirect the check at a different
// repository or ignore file when lgtmcp runs inside another git process such as a
// pre-commit hook.
func IsIgnored(ctx context.Context, repoPath, relativePath string) (bool, error) {
	res, err := runGit(ctx, repoPath, nil, nil, "check-ignore", "--", relativePath)
	if err != nil {
		return false, fmt.Errorf("failed to execute git check-ignore: %w", err)
	}

	switch res.exitCode {
	case 0: // The path is ignored.
		return true, nil
	case 1: // The path is not ignored.
		return false, nil
	default: // 128 (e.g. not a git repository) or anything else: fail closed.
		msg := strings.TrimSpace(res.stderr)
		if msg == "" {
			msg = fmt.Sprintf("exit status %d", res.exitCode)
		}

		return false, fmt.Errorf("%w: git check-ignore: %s", ErrCommandFailed, msg)
	}
}

// gitResult holds the outcome of a completed git invocation.
type gitResult struct {
	stdout   string
	stderr   string
	exitCode int
}

// runGit runs git in repoPath with a sanitized environment and the standard
// timeout, returning the command's stdout, stderr, and process exit code. All
// GIT_* variables are stripped so the command operates on repoPath rather than
// being redirected by an inherited GIT_DIR/GIT_INDEX_FILE/GIT_AUTHOR_* (which
// happens when lgtmcp is exercised from inside a git pre-commit hook). A
// non-zero exit is reported via the result's exitCode with a nil error; err is
// non-nil only when the command did not run to completion — ErrCommandTimeout
// on deadline, the underlying exec error when git cannot be started, or the
// signal error when git is killed instead of exiting (exitCode -1).
func runGit(
	ctx context.Context, repoPath string, stdin io.Reader, extraEnv []string, args ...string,
) (gitResult, error) {
	ctx, cancel := context.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // args are constructed internally, not from user input
	cmd.Dir = repoPath
	cmd.Env = scrubGitEnv(os.Environ())
	if len(extraEnv) > 0 {
		cmd.Env = append(cmd.Env, extraEnv...)
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	res := gitResult{stdout: outBuf.String(), stderr: errBuf.String()}
	if runErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return res, fmt.Errorf("%w: %s", ErrCommandTimeout, strings.Join(args, " "))
		}

		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			res.exitCode = exitErr.ExitCode()
			// A negative exit code means git did not exit but was killed by
			// a signal (e.g. SIGSEGV). Report that as a run failure so the
			// signal name reaches the caller instead of an opaque
			// "exit status -1".
			if res.exitCode < 0 {
				return res, runErr
			}

			return res, nil
		}

		res.exitCode = -1

		return res, runErr
	}

	return res, nil
}

// scrubGitEnv returns env with all GIT_* variables removed. This applies to every
// git command, commit included, and is deliberate: lgtmcp often runs inside
// another git process (e.g. a pre-commit hook), where variables such as GIT_DIR,
// GIT_INDEX_FILE, GIT_AUTHOR_NAME/EMAIL, and GIT_CONFIG_GLOBAL describe the outer
// operation and would otherwise misdirect lgtmcp's command at the wrong
// repository, author, or ignore file. Commit identity and signing are still
// honored — git reads user.name/user.email, commit.gpgsign, and user.signingkey
// from the repository config and the user's ~/.gitconfig, which HOME (left intact)
// still locates. Narrowing this to only the location variables would re-expose the
// author and ignore-file leaks it exists to prevent.
func scrubGitEnv(env []string) []string {
	out := env[:0:0]
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_") {
			continue
		}
		out = append(out, e)
	}
	return out
}

func isGitRepo(path string) bool {
	gitPath := filepath.Join(path, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return true
	}
	if !info.Mode().IsRegular() {
		return false
	}
	// Worktrees use a .git file starting with "gitdir: "
	return hasGitdirPrefix(gitPath)
}

func hasGitdirPrefix(path string) bool {
	f, err := os.Open(path) //nolint:gosec // path is constructed internally, not from user input
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck // read-only file, close error is inconsequential
	const prefix = "gitdir: "
	buf := make([]byte, len(prefix))
	_, err = io.ReadFull(f, buf)
	return err == nil && string(buf) == prefix
}

// CheckGitRepo verifies that the given path is a valid git repository.
func CheckGitRepo(path string) bool {
	return isGitRepo(path)
}
