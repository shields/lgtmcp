package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// GitCommandTimeout is the maximum duration for a git command to complete.
	gitCommandTimeout = 30 * time.Second
)

var (
	ErrNotGitRepo      = errors.New("directory is not a git repository")
	ErrNoChanges       = errors.New("no changes to commit")
	ErrCommandFailed   = errors.New("git command failed")
	ErrInvalidPath     = errors.New("invalid path")
	ErrEmptyCommitMsg  = errors.New("commit message cannot be empty")
	ErrFileNotFound    = errors.New("file not found")
	ErrCommandTimeout  = errors.New("git command timed out")
	ErrPathOutsideRepo = errors.New("path is outside repository")
	ErrNotRegularFile  = errors.New("not a regular file")
)

type Git struct {
	repoPath string
}

func New(repoPath string) (*Git, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	if !isGitRepo(absPath) {
		return nil, ErrNotGitRepo
	}

	return &Git{
		repoPath: absPath,
	}, nil
}

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
					_, _ = diffOutput.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", file, file))
					_, _ = diffOutput.WriteString("new file mode 100644\n")
					_, _ = diffOutput.WriteString("--- /dev/null\n")
					_, _ = diffOutput.WriteString(fmt.Sprintf("+++ b/%s\n", file))
					lines := strings.Split(content, "\n")
					for _, line := range lines {
						_, _ = diffOutput.WriteString(fmt.Sprintf("+%s\n", line))
					}
				}
			}
		}
		diff = diffOutput.String()
	} else {
		// Normal case: diff between HEAD and working directory (including untracked files).
		// This shows all changes regardless of staging status.
		diff, err = g.runGitCommand(ctx, "diff", "HEAD", "--", ".")
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
			files := strings.Split(strings.TrimSpace(untrackedFiles), "\n")
			for _, file := range files {
				if file != "" {
					content, err := g.GetFileContent(ctx, file)
					if err == nil && content != "" {
						_, _ = untrackedDiff.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", file, file))
						_, _ = untrackedDiff.WriteString("new file mode 100644\n")
						_, _ = untrackedDiff.WriteString("--- /dev/null\n")
						_, _ = untrackedDiff.WriteString(fmt.Sprintf("+++ b/%s\n", file))
						lines := strings.Split(content, "\n")
						for _, line := range lines {
							_, _ = untrackedDiff.WriteString(fmt.Sprintf("+%s\n", line))
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

func (g *Git) StageAll(ctx context.Context) error {
	_, err := g.runGitCommand(ctx, "add", "-A")
	if err != nil {
		return fmt.Errorf("failed to stage all changes: %w", err)
	}

	return nil
}

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
	info, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrFileNotFound, relativePath)
		}

		return "", fmt.Errorf("failed to stat file: %w", err)
	}

	// If it's a symlink, verify it points within the repo.
	if info.Mode()&os.ModeSymlink != 0 {
		linkTarget, linkErr := os.Readlink(fullPath)
		if linkErr != nil {
			return "", fmt.Errorf("failed to read symlink: %w", linkErr)
		}

		// Resolve the symlink target.
		if !filepath.IsAbs(linkTarget) {
			linkTarget = filepath.Join(filepath.Dir(fullPath), linkTarget)
		}

		absTarget, absErr := filepath.Abs(linkTarget)
		if absErr != nil {
			return "", fmt.Errorf("failed to resolve symlink target: %w", absErr)
		}

		if !strings.HasPrefix(absTarget, g.repoPath) {
			return "", fmt.Errorf("%w: symlink points outside repo", ErrPathOutsideRepo)
		}
	} else if !info.Mode().IsRegular() {
		// If it's not a symlink, it must be a regular file.
		return "", fmt.Errorf("%w: %s", ErrNotRegularFile, relativePath)
	}

	// Read the file content.
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	return string(content), nil
}

func (g *Git) GetRepoPath() string {
	return g.repoPath
}

func (g *Git) runGitCommand(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.repoPath

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

func isGitRepo(path string) bool {
	gitDir := filepath.Join(path, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return false
	}

	return info.IsDir()
}

func CheckGitRepo(path string) bool {
	return isGitRepo(path)
}
