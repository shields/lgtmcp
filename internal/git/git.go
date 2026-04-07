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
	"strings"
	"time"

	"msrl.dev/lgtmcp/internal/config"
)

const (
	// GitCommandTimeout is the maximum duration for a git command to complete.
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
	// Check if this is an initial commit (no HEAD exists).
	_, err := g.runGitCommand(ctx, "rev-parse", "HEAD")
	isInitialCommit := err != nil

	var diff string

	if isInitialCommit {
		// For initial commit, get all files and show them as new.
		files, filesErr := g.runGitCommand(ctx, "ls-files", "--others", "--exclude-standard")
		if filesErr != nil {
			return "", fmt.Errorf("failed to get files for initial commit: %w", filesErr)
		}

		// Also check for any files that might be staged.
		stagedFiles, stageErr := g.runGitCommand(ctx, "diff", "--cached", "--name-only")
		if stageErr != nil {
			// Ignore error, just use untracked files.
			stagedFiles = ""
		}
		if stagedFiles != "" {
			if files != "" {
				files = files + "\n" + stagedFiles
			} else {
				files = stagedFiles
			}
		}

		if files == "" {
			return "", ErrNoChanges
		}

		var diffOutput bytes.Buffer
		fileList := strings.Split(strings.TrimSpace(files), "\n")
		uniqueFiles := make(map[string]bool)
		for _, file := range fileList {
			if file != "" && !uniqueFiles[file] {
				uniqueFiles[file] = true
				content, contentErr := g.GetFileContent(ctx, file)
				if contentErr == nil && content != "" {
					_, _ = fmt.Fprintf(&diffOutput, "diff --git a/%s b/%s\n", file, file)
					_, _ = diffOutput.WriteString("new file mode 100644\n")
					_, _ = diffOutput.WriteString("--- /dev/null\n")
					_, _ = fmt.Fprintf(&diffOutput, "+++ b/%s\n", file)
					for line := range strings.SplitSeq(content, "\n") {
						_, _ = fmt.Fprintf(&diffOutput, "+%s\n", line)
					}
				}
			}
		}
		diff = diffOutput.String()
	} else {
		// Normal case: diff between HEAD and working directory (including untracked files).
		// This shows all changes regardless of staging status.
		// Use the configured context lines (default 20).
		contextFlag := fmt.Sprintf("--unified=%d", g.diffContextLines)
		diff, err = g.runGitCommand(ctx, "diff", contextFlag, "HEAD", "--", ".")
		if err != nil {
			return "", fmt.Errorf("failed to get diff against HEAD: %w", err)
		}

		// Also include untracked files.
		untrackedFiles, err := g.runGitCommand(ctx, "ls-files", "--others", "--exclude-standard")
		if err != nil {
			return "", fmt.Errorf("failed to get untracked files: %w", err)
		}

		if untrackedFiles != "" {
			var untrackedDiff bytes.Buffer
			for file := range strings.SplitSeq(strings.TrimSpace(untrackedFiles), "\n") {
				if file != "" {
					content, err := g.GetFileContent(ctx, file)
					if err == nil && content != "" {
						_, _ = fmt.Fprintf(&untrackedDiff, "diff --git a/%s b/%s\n", file, file)
						_, _ = untrackedDiff.WriteString("new file mode 100644\n")
						_, _ = untrackedDiff.WriteString("--- /dev/null\n")
						_, _ = fmt.Fprintf(&untrackedDiff, "+++ b/%s\n", file)
						for line := range strings.SplitSeq(content, "\n") {
							_, _ = fmt.Fprintf(&untrackedDiff, "+%s\n", line)
						}
					}
				}
			}
			if untrackedDiff.Len() > 0 {
				if diff != "" {
					diff += "\n"
				}
				diff += untrackedDiff.String()
			}
		}
	}

	if diff == "" {
		return "", ErrNoChanges
	}

	return diff, nil
}

// StageAll stages all changes in the repository.
func (g *Git) StageAll(ctx context.Context) error {
	_, err := g.runGitCommand(ctx, "add", "-A")
	if err != nil {
		return fmt.Errorf("failed to stage all changes: %w", err)
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
	// Reject absolute paths.
	if filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("%w: absolute paths not allowed", ErrInvalidPath)
	}

	// Clean the path and ensure it's relative.
	cleanPath := filepath.Clean(relativePath)

	// Construct full path.
	fullPath := filepath.Join(g.repoPath, cleanPath)

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

	// Check if file exists (use Lstat to not follow symlinks).
	if _, lstatErr := os.Lstat(fullPath); lstatErr != nil {
		if os.IsNotExist(lstatErr) {
			return "", fmt.Errorf("%w: %s", ErrFileNotFound, relativePath)
		}

		return "", fmt.Errorf("failed to stat file: %w", lstatErr)
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
		return "", fmt.Errorf("failed to resolve repo path: %w", err)
	}

	resolved, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	if !strings.HasPrefix(resolved, canonicalRepo+string(filepath.Separator)) && resolved != canonicalRepo {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideRepo, relativePath)
	}

	// After resolution, verify it's a regular file (not a directory, device, etc.).
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("failed to stat resolved file: %w", err)
	}
	if !resolvedInfo.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s", ErrNotRegularFile, relativePath)
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	return string(content), nil
}

// GetRepoPath returns the absolute path to the repository.
func (g *Git) GetRepoPath() string {
	return g.repoPath
}

func (g *Git) runGitCommand(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // args are constructed internally, not from user input
	cmd.Dir = g.repoPath
	// Strip GIT_* env vars so the command operates on g.repoPath rather than
	// being redirected by an inherited GIT_DIR/GIT_INDEX_FILE/GIT_AUTHOR_*
	// (which happens when lgtmcp is exercised from inside a git pre-commit hook).
	cmd.Env = scrubGitEnv(os.Environ())

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("%w: %s", ErrCommandTimeout, strings.Join(args, " "))
		}
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}

		return "", fmt.Errorf("%w: %s", ErrCommandFailed, errMsg)
	}

	return stdout.String(), nil
}

// scrubGitEnv returns env with all GIT_* variables removed.
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
