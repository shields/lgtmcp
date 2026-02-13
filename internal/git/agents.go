package git

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AgentFile represents an AGENTS.md file found in the repository.
type AgentFile struct {
	Path    string // Relative path from repo root (e.g., "AGENTS.md" or "src/AGENTS.md")
	Content string
}

// maxAgentFileSize is the maximum size of an AGENTS.md file (50KB).
const maxAgentFileSize = 50 * 1024

// FindAgentFiles discovers AGENTS.md files relevant to the changed files.
// For each changed file, it walks from the file's directory up to the repo root,
// collecting unique AGENTS.md files. Results are sorted root-first (fewest path
// separators), then alphabetically. Files larger than 50KB are skipped.
func (g *Git) FindAgentFiles(changedFiles []string) ([]AgentFile, error) {
	if len(changedFiles) == 0 {
		return nil, nil
	}

	// Resolve repo path once to handle system-level symlinks
	// (e.g., macOS /var -> /private/var).
	canonicalRepo, err := filepath.EvalSymlinks(g.repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve repo path: %w", err)
	}

	// Collect unique directories to check.
	dirs := make(map[string]bool)
	for _, f := range changedFiles {
		dir := filepath.Dir(filepath.Clean(f))
		for {
			dirs[dir] = true
			if dir == "." {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break // Safety: stop if Dir no longer changes (e.g. absolute path root)
			}
			dir = parent
		}
	}

	// Check each directory for AGENTS.md and collect unique files by path.
	found := make(map[string]string) // path -> content
	for dir := range dirs {
		relPath := filepath.Join(dir, "AGENTS.md")
		fullPath := filepath.Join(g.repoPath, relPath)

		info, err := os.Lstat(fullPath)
		if err != nil {
			continue // File doesn't exist
		}

		// Resolve the full path to catch both file-level and
		// directory-level symlinks that might escape the repo.
		resolved, evalErr := filepath.EvalSymlinks(fullPath)
		if evalErr != nil {
			continue
		}
		if !strings.HasPrefix(resolved, canonicalRepo+string(filepath.Separator)) && resolved != canonicalRepo {
			continue
		}

		// After resolution, verify it's a regular file (not a directory, etc.).
		resolvedInfo, statErr := os.Stat(resolved)
		if statErr != nil || !resolvedInfo.Mode().IsRegular() {
			continue
		}

		// Skip files that are too large (symlink info.Size is unreliable).
		if resolvedInfo.Size() > maxAgentFileSize {
			continue
		}

		// Lstat reported a symlink but the file itself isn't—still need
		// to skip non-regular entries detected by the initial Lstat when
		// EvalSymlinks didn't change anything (e.g., directories named AGENTS.md).
		_ = info // info was used for Lstat; resolved check above handles all cases.

		if _, exists := found[relPath]; exists {
			continue
		}

		content, err := os.ReadFile(resolved)
		if err != nil {
			continue // Skip unreadable files rather than failing entirely
		}

		found[relPath] = string(content)
	}

	if len(found) == 0 {
		return nil, nil
	}

	// Sort by depth (fewest separators first), then alphabetically.
	paths := make([]string, 0, len(found))
	for p := range found {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		di := strings.Count(paths[i], string(filepath.Separator))
		dj := strings.Count(paths[j], string(filepath.Separator))
		if di != dj {
			return di < dj
		}
		return paths[i] < paths[j]
	})

	result := make([]AgentFile, len(paths))
	for i, p := range paths {
		result[i] = AgentFile{Path: p, Content: found[p]}
	}
	return result, nil
}

// FormatAgentInstructions formats discovered AGENTS.md files into a prompt section.
// Returns an empty string if no files are provided.
func FormatAgentInstructions(files []AgentFile) string {
	if len(files) == 0 {
		return ""
	}

	var sb strings.Builder
	_, _ = sb.WriteString("## Repository Agent Instructions\n\n")
	_, _ = sb.WriteString("The following AGENTS.md files were found in the repository ")
	_, _ = sb.WriteString("and contain project-specific review guidelines:\n\n")

	for _, f := range files {
		_, _ = fmt.Fprintf(&sb, "### %s\n\n%s\n\n", f.Path, strings.TrimSpace(f.Content))
	}

	return sb.String()
}
