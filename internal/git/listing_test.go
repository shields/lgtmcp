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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"msrl.dev/lgtmcp/internal/testutil"
)

func TestListChangedDirectories(t *testing.T) {
	t.Parallel()

	t.Run("lists files and subdirectories of changed directories only", func(t *testing.T) {
		t.Parallel()
		repo := testutil.CreateTempGitRepo(t)
		testutil.CreateFile(t, repo, "main.go", "package main")
		testutil.CreateFile(t, repo, "go.mod", "module x")
		testutil.CreateFile(t, repo, "cmd/tool/main.go", "package main")
		testutil.CreateFile(t, repo, "internal/a/a.go", "package a")
		testutil.CreateFile(t, repo, "internal/a/b.go", "package a")
		testutil.CreateFile(t, repo, "internal/a/sub/c.go", "package sub")
		testutil.CreateFile(t, repo, "internal/other/o.go", "package other")
		testutil.RunGitCmd(t, repo, "add", ".")
		testutil.RunGitCmd(t, repo, "commit", "-m", "initial")

		g, err := New(repo, nil)
		require.NoError(t, err)

		listings, err := g.ListChangedDirectories(t.Context(), []string{"main.go", "internal/a/a.go"}, nil)
		require.NoError(t, err)
		assert.Equal(t, []DirectoryListing{
			{Dir: "", Entries: []string{"cmd/", "go.mod", "internal/", "main.go"}},
			{Dir: "internal/a", Entries: []string{"internal/a/a.go", "internal/a/b.go", "internal/a/sub/"}},
		}, listings)
	})

	t.Run("includes untracked files and excludes ignored and deleted ones", func(t *testing.T) {
		t.Parallel()
		repo := testutil.CreateTempGitRepo(t)
		testutil.CreateFile(t, repo, ".gitignore", "secret.env\nbuild/\n")
		testutil.CreateFile(t, repo, "pkg/kept.go", "package pkg")
		testutil.CreateFile(t, repo, "pkg/gone.go", "package pkg")
		testutil.RunGitCmd(t, repo, "add", ".")
		testutil.RunGitCmd(t, repo, "commit", "-m", "initial")
		testutil.CreateFile(t, repo, "pkg/new.go", "package pkg")
		testutil.CreateFile(t, repo, "pkg/secret.env", "TOKEN=x")
		testutil.CreateFile(t, repo, "pkg/build/out.bin", "binary")
		testutil.RunGitCmd(t, repo, "rm", "-q", "--cached", "pkg/gone.go")

		g, err := New(repo, nil)
		require.NoError(t, err)

		listings, err := g.ListChangedDirectories(t.Context(),
			[]string{"pkg/new.go", "pkg/gone.go"}, []string{"pkg/gone.go"})
		require.NoError(t, err)
		require.Len(t, listings, 1)
		assert.Equal(t, []string{"pkg/kept.go", "pkg/new.go"}, listings[0].Entries)
	})

	t.Run("directory with nothing left is omitted", func(t *testing.T) {
		t.Parallel()
		repo := testutil.CreateTempGitRepo(t)
		testutil.CreateFile(t, repo, "keep.go", "package main")
		testutil.CreateFile(t, repo, "old/only.go", "package old")
		testutil.RunGitCmd(t, repo, "add", ".")
		testutil.RunGitCmd(t, repo, "commit", "-m", "initial")
		testutil.RunGitCmd(t, repo, "rm", "-q", "old/only.go")

		g, err := New(repo, nil)
		require.NoError(t, err)

		listings, err := g.ListChangedDirectories(t.Context(), []string{"old/only.go"}, []string{"old/only.go"})
		require.NoError(t, err)
		assert.Empty(t, listings)
	})

	t.Run("directory names with glob characters", func(t *testing.T) {
		t.Parallel()
		repo := testutil.CreateTempGitRepo(t)
		testutil.CreateFile(t, repo, "a[b]/x.go", "package a")
		testutil.CreateFile(t, repo, "a[b]/z.go", "package a")
		testutil.CreateFile(t, repo, "ab/y.go", "package ab")
		testutil.RunGitCmd(t, repo, "add", ".")
		testutil.RunGitCmd(t, repo, "commit", "-m", "initial")

		g, err := New(repo, nil)
		require.NoError(t, err)

		listings, err := g.ListChangedDirectories(t.Context(), []string{"a[b]/x.go"}, nil)
		require.NoError(t, err)
		require.Len(t, listings, 1)
		assert.Equal(t, []string{"a[b]/x.go", "a[b]/z.go"}, listings[0].Entries)
	})

	t.Run("names with line breaks are omitted", func(t *testing.T) {
		t.Parallel()
		repo := testutil.CreateTempGitRepo(t)
		testutil.CreateFile(t, repo, "pkg/ok.go", "package pkg")
		testutil.CreateFile(t, repo, "pkg/bad\nname.go", "package pkg")
		testutil.CreateFile(t, repo, "pkg/line\u2028separator.go", "package pkg")
		testutil.CreateFile(t, repo, "pkg/paragraph\u2029separator.go", "package pkg")
		testutil.CreateFile(t, repo, "pkg/sub\ndir/x.go", "package sub")
		testutil.CreateFile(t, repo, "pkg/sub/y.go", "package sub")
		testutil.RunGitCmd(t, repo, "add", ".")
		testutil.RunGitCmd(t, repo, "commit", "-q", "-m", "initial")

		g, err := New(repo, nil)
		require.NoError(t, err)

		listings, err := g.ListChangedDirectories(t.Context(), []string{"pkg/ok.go"}, nil)
		require.NoError(t, err)
		assert.Equal(t, []DirectoryListing{
			{Dir: "pkg", Entries: []string{"pkg/ok.go", "pkg/sub/"}},
		}, listings)
	})

	t.Run("subdirectory above a vacated directory is still listed", func(t *testing.T) {
		t.Parallel()
		repo := testutil.CreateTempGitRepo(t)
		testutil.CreateFile(t, repo, "main.go", "package main")
		testutil.CreateFile(t, repo, "internal/doc.go", "package internal")
		testutil.CreateFile(t, repo, "internal/foo/foo.go", "package foo")
		testutil.CreateFile(t, repo, "internal/foo/bar/b.go", "package bar")
		testutil.RunGitCmd(t, repo, "add", ".")
		testutil.RunGitCmd(t, repo, "commit", "-q", "-m", "initial")
		testutil.RunGitCmd(t, repo, "rm", "-q", "internal/foo/bar/b.go")

		g, err := New(repo, nil)
		require.NoError(t, err)

		listings, err := g.ListChangedDirectories(t.Context(),
			[]string{"main.go", "internal/doc.go", "internal/foo/bar/b.go"}, []string{"internal/foo/bar/b.go"})
		require.NoError(t, err)
		assert.Equal(t, []DirectoryListing{
			{Dir: "", Entries: []string{"internal/", "main.go"}},
			{Dir: "internal", Entries: []string{"internal/doc.go", "internal/foo/"}},
		}, listings)
	})

	t.Run("vacated subdirectory is not listed in its parent", func(t *testing.T) {
		t.Parallel()
		repo := testutil.CreateTempGitRepo(t)
		testutil.CreateFile(t, repo, "pkg/changed.go", "package pkg")
		testutil.CreateFile(t, repo, "pkg/old/gone.go", "package old")
		testutil.RunGitCmd(t, repo, "add", ".")
		testutil.RunGitCmd(t, repo, "commit", "-q", "-m", "initial")
		testutil.RunGitCmd(t, repo, "rm", "-q", "pkg/old/gone.go")

		g, err := New(repo, nil)
		require.NoError(t, err)

		listings, err := g.ListChangedDirectories(t.Context(),
			[]string{"pkg/changed.go", "pkg/old/gone.go"}, []string{"pkg/old/gone.go"})
		require.NoError(t, err)
		assert.Equal(t, []DirectoryListing{{Dir: "pkg", Entries: []string{"pkg/changed.go"}}}, listings)
	})

	t.Run("submodule is listed once", func(t *testing.T) {
		t.Parallel()
		repo := testutil.CreateTempGitRepo(t)
		testutil.CreateFile(t, repo, "vendor/x.go", "package vendor")
		testutil.RunGitCmd(t, repo, "add", ".")
		testutil.RunGitCmd(t, repo, "commit", "-q", "-m", "initial")
		head := testutil.RunGitCmd(t, repo, "rev-parse", "HEAD")
		testutil.RunGitCmd(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+head+",vendor/lib")
		testutil.RunGitCmd(t, repo, "commit", "-q", "-m", "submodule")

		g, err := New(repo, nil)
		require.NoError(t, err)

		listings, err := g.ListChangedDirectories(t.Context(), []string{"vendor/x.go"}, nil)
		require.NoError(t, err)
		assert.Equal(t, []DirectoryListing{{Dir: "vendor", Entries: []string{"vendor/lib", "vendor/x.go"}}}, listings)
	})

	t.Run("directory name that is not valid UTF-8", func(t *testing.T) {
		t.Parallel()
		repo := testutil.CreateTempGitRepo(t)
		testutil.CreateFile(t, repo, "seed.txt", "seed")
		blob := testutil.RunGitCmd(t, repo, "hash-object", "-w", "seed.txt")
		const dir = "caf\xe9"
		for _, name := range []string{"x.go", "y.go"} {
			testutil.RunGitCmd(t, repo, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+dir+"/"+name)
		}

		g, err := New(repo, nil)
		require.NoError(t, err)

		listings, err := g.ListChangedDirectories(t.Context(), []string{dir + "/x.go"}, nil)
		require.NoError(t, err)
		assert.Equal(t, []DirectoryListing{{Dir: dir, Entries: []string{dir + "/x.go", dir + "/y.go"}}}, listings)
	})

	t.Run("unborn branch lists staged and changed files", func(t *testing.T) {
		t.Parallel()
		repo := testutil.CreateTempGitRepo(t)
		testutil.CreateFile(t, repo, "staged.go", "package main")
		testutil.CreateFile(t, repo, "src/new.go", "package src")
		testutil.RunGitCmd(t, repo, "add", "staged.go")

		g, err := New(repo, nil)
		require.NoError(t, err)

		listings, err := g.ListChangedDirectories(t.Context(), []string{"staged.go", "src/new.go"}, nil)
		require.NoError(t, err)
		assert.Equal(t, []DirectoryListing{
			{Dir: "", Entries: []string{"src/", "staged.go"}},
			{Dir: "src", Entries: []string{"src/new.go"}},
		}, listings)
	})

	t.Run("no changed files", func(t *testing.T) {
		t.Parallel()
		repo := testutil.CreateTempGitRepo(t)
		g, err := New(repo, nil)
		require.NoError(t, err)

		listings, err := g.ListChangedDirectories(t.Context(), nil, nil)
		require.NoError(t, err)
		assert.Nil(t, listings)
	})

	t.Run("bounds entries per directory and in total", func(t *testing.T) {
		t.Parallel()
		repo := testutil.CreateTempGitRepo(t)
		const dirs = 12
		perDir := maxListingEntriesPerDir + 5
		changed := make([]string, 0, dirs)
		for d := range dirs {
			for f := range perDir {
				testutil.CreateFile(t, repo, fmt.Sprintf("d%02d/f%03d.go", d, f), "package d")
			}
			changed = append(changed, fmt.Sprintf("d%02d/f000.go", d))
		}
		testutil.RunGitCmd(t, repo, "add", ".")
		testutil.RunGitCmd(t, repo, "commit", "-q", "-m", "initial")

		g, err := New(repo, nil)
		require.NoError(t, err)

		listings, err := g.ListChangedDirectories(t.Context(), changed, nil)
		require.NoError(t, err)

		total := 0
		for _, l := range listings {
			assert.LessOrEqual(t, len(l.Entries), maxListingEntriesPerDir)
			assert.Equal(t, perDir, len(l.Entries)+l.Omitted, "dir %s", l.Dir)
			total += len(l.Entries)
		}
		assert.Equal(t, maxListingEntries, total)
		require.Len(t, listings, dirs)
		// Ten full directories exhaust the total; the rest list only a count.
		assert.Len(t, listings[9].Entries, maxListingEntriesPerDir)
		for _, l := range listings[10:] {
			assert.Empty(t, l.Entries)
			assert.Equal(t, perDir, l.Omitted)
		}

		rendered := FormatDirectoryListings(listings)
		assert.NotContains(t, rendered, "d10/")
		assert.Contains(t, rendered, "\n(2 more directories not listed)\n")
	})
}

func TestFormatDirectoryListings(t *testing.T) {
	t.Parallel()

	got := FormatDirectoryListings([]DirectoryListing{
		{Dir: "", Entries: []string{"cmd/", "main.go"}},
		{Dir: "internal/a", Entries: []string{"internal/a/a.go"}, Omitted: 7},
	})
	assert.Equal(t, "(repository root)\n- cmd/\n- main.go\n\ninternal/a/\n- internal/a/a.go\n- (7 more not listed)\n", got)
	assert.Empty(t, FormatDirectoryListings(nil))

	got = FormatDirectoryListings([]DirectoryListing{
		{Dir: "a", Entries: []string{"a/x.go"}},
		{Dir: "b", Omitted: 5},
		{Dir: "c", Omitted: 9},
	})
	assert.Equal(t, "a/\n- a/x.go\n\n(2 more directories not listed)\n", got)
}
