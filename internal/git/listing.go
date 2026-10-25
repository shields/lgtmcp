// Copyright © 2026 Michael Shields
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

package git

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"unicode"
)

// maxListingEntriesPerDir and maxListingEntries bound the directory listing
// sent with every review request, so a directory of generated files or a
// change spanning many directories cannot crowd out the diff.
const (
	maxListingEntriesPerDir = 100
	maxListingEntries       = 1000
)

// DirectoryListing is the immediate contents of one directory.
type DirectoryListing struct {
	// Dir is the repository-relative directory, "" for the root.
	Dir string
	// Entries are repository-relative paths in lexical order; subdirectories
	// carry a trailing "/".
	Entries []string
	// Omitted counts entries left out by the listing bounds.
	Omitted int
}

// ListChangedDirectories lists every directory that contains a changed file:
// its tracked files and changed files, and the subdirectories that hold either.
// Paths in deletedFiles are left out, as are names containing control
// characters or Unicode line separators and directories with nothing left to
// list. Listings are sorted by directory and capped at maxListingEntriesPerDir
// entries each and maxListingEntries in total.
//
// Untracked files appear in the diff as new files, so changedFiles already
// holds every untracked path worth listing and the index supplies the tracked
// ones. The whole index is read in one call because git compares every entry
// with every pathspec, so a pathspec per directory costs the index size times
// the number of directories.
func (g *Git) ListChangedDirectories(
	ctx context.Context, changedFiles, deletedFiles []string,
) ([]DirectoryListing, error) {
	dirSet := make(map[string]bool)
	for _, f := range changedFiles {
		dirSet[parentDir(f)] = true
	}
	if len(dirSet) == 0 {
		return nil, nil
	}

	indexed, err := g.runGitCommand(ctx, "ls-files", "-z", "--cached")
	if err != nil {
		return nil, fmt.Errorf("failed to list changed directories: %w", err)
	}

	deleted := make(map[string]bool, len(deletedFiles))
	for _, f := range deletedFiles {
		deleted[f] = true
	}

	entries := make(map[string][]string, len(dirSet))
	add := func(dir, entry string) {
		// A newline or other line break would let a file name start a new line
		// of the prompt.
		if !strings.ContainsFunc(entry, breaksLine) {
			entries[dir] = append(entries[dir], entry)
		}
	}
	// A path lists itself in its directory and lists each ancestor directory in
	// that directory's parent. Deleted paths do neither, so a directory that
	// has lost every file is absent from its parent as well.
	walked := make(map[string]bool)
	visit := func(p string) {
		if deleted[p] {
			return
		}
		dir := parentDir(p)
		if dirSet[dir] {
			add(dir, p)
		}
		for child := dir; child != "" && !walked[child]; child = parentDir(child) {
			walked[child] = true
			if parent := parentDir(child); dirSet[parent] {
				add(parent, child+"/")
			}
		}
	}
	for p := range strings.SplitSeq(indexed, "\x00") {
		if p != "" {
			visit(p)
		}
	}
	for _, p := range changedFiles {
		visit(p)
	}

	dirs := slices.Sorted(maps.Keys(dirSet))
	listings := make([]DirectoryListing, 0, len(dirs))
	remaining := maxListingEntries
	for _, d := range dirs {
		if len(entries[d]) == 0 {
			continue
		}
		slices.Sort(entries[d])
		all := slices.Compact(entries[d])
		shown := all[:min(len(all), maxListingEntriesPerDir, remaining)]
		remaining -= len(shown)
		listings = append(listings, DirectoryListing{Dir: d, Entries: shown, Omitted: len(all) - len(shown)})
	}

	return listings, nil
}

// FormatDirectoryListings renders listings for the review prompt, one block
// per directory. Directories the listing bounds left without a single entry
// collapse into one closing count, so the output stays bounded however many
// directories the change touches.
func FormatDirectoryListings(listings []DirectoryListing) string {
	var b strings.Builder
	unlisted := 0
	for _, l := range listings {
		if len(l.Entries) == 0 {
			unlisted++

			continue
		}
		if b.Len() > 0 {
			_ = b.WriteByte('\n')
		}
		label := l.Dir + "/"
		if l.Dir == "" {
			label = "(repository root)"
		}
		_, _ = fmt.Fprintf(&b, "%s\n", label)
		for _, e := range l.Entries {
			_, _ = fmt.Fprintf(&b, "- %s\n", e)
		}
		if l.Omitted > 0 {
			_, _ = fmt.Fprintf(&b, "- (%d more not listed)\n", l.Omitted)
		}
	}
	if unlisted > 0 {
		if b.Len() > 0 {
			_ = b.WriteByte('\n')
		}
		_, _ = fmt.Fprintf(&b, "(%d more directories not listed)\n", unlisted)
	}

	return b.String()
}

func breaksLine(r rune) bool {
	// U+2028 and U+2029 end a line in many renderers without being control
	// characters.
	return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
}

// parentDir returns the repository-relative directory containing p, "" for a
// path at the root.
func parentDir(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}

	return ""
}
