package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Pi v1.5.1 463b8f89 (#11972, fixes #9262) 3302-find-path-glob.test.ts: the cases beyond the shared fixture that run on every platform.
func TestFindPathPatternsPi151(t *testing.T) {
	root := findRegressionTree(t, map[string]string{
		"some/parent/child/file.ext":     "",
		"some/parent/child/test.spec.ts": "",
		"src/foo/bar/example.spec.ts":    "",
		"src/direct.spec.ts":             "",
	})
	t.Run("directory-prefixed ** tail does not return the directory itself", func(t *testing.T) {
		got := outputLines(runFind(t, root, map[string]any{"pattern": "some/parent/child/**"}).Text())
		if slices.Contains(got, "some/parent/child/") {
			t.Fatalf("find returned the directory itself: %q", got)
		}
	})
	t.Run("path patterns support braces, classes, and literal regex characters", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(root, "src", "a&b (1).ts"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		assertFindRegressionPaths(t, root, "{src,some}/**/[et]*.spec.ts", []string{"some/parent/child/test.spec.ts", "src/foo/bar/example.spec.ts"})
		assertFindRegressionPaths(t, root, "src/[!x]&b (1).ts", []string{"src/a&b (1).ts"})
	})
	t.Run("path patterns stop at the result limit", func(t *testing.T) {
		result := runFind(t, root, map[string]any{"pattern": "src/**/*.spec.ts", "limit": 1})
		if got := limitDetails(t, result); got != `{"resultLimitReached":1}` {
			t.Fatalf("details = %s", got)
		}
		first, _, _ := strings.Cut(result.Text(), "\n")
		if first != "src/direct.spec.ts" && first != "src/foo/bar/example.spec.ts" {
			t.Fatalf("first line = %q", first)
		}
	})
	t.Run("backslash still escapes glob characters on POSIX", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("on Windows a backslash is a path separator")
		}
		for _, name := range []string{"*.ts", "other.ts"} {
			if err := os.WriteFile(filepath.Join(root, "src", name), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		assertFindRegressionPaths(t, root, `src/\*.ts`, []string{"src/*.ts"})
	})
	// On Windows `\` is a path separator (#9262). Only Windows CI runs real fd with these patterns; the rows below the matcher test prove the
	// matching on every platform.
	t.Run("Windows backslash patterns match like forward-slash patterns", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("Windows only")
		}
		assertFindRegressionPaths(t, root, `src\**\*.spec.ts`, []string{"src/direct.spec.ts", "src/foo/bar/example.spec.ts"})
		assertFindRegressionPaths(t, root, `src\*.spec.ts`, []string{"src/direct.spec.ts"})
		assertFindRegressionPaths(t, root, `**\parent\child\*`, []string{"some/parent/child/file.ext", "some/parent/child/test.spec.ts"})
		assertFindRegressionPaths(t, root, `src\**/*.spec.ts`, []string{"src/direct.spec.ts", "src/foo/bar/example.spec.ts"})
	})
}

// The Windows branch of find.ts, run with the platform injected. fd lists every entry and the matcher picks the paths; no fd glob argument carries
// the pattern, so the result cannot depend on the fd version.
func TestFindFdInvocationWindowsPathPattern(t *testing.T) {
	const search = `C:\work\proj`
	paths := []string{
		`C:\work\proj\src\direct.spec.ts`,
		`C:\work\proj\src\foo\bar\example.spec.ts`,
		`C:\work\proj\src\foo\`,
		`C:\work\proj\src\.hidden.ts`,
		`C:\work\proj\src\a&b (1).ts`,
		`C:\work\proj\some\parent\child\file.ext`,
		`C:\work\proj\some\parent\child\`,
		`C:\work\proj\Src\Upper.TS`,
		`C:\work\proj\src\+(a|b).ts`,
		`C:\work\proj\src\a.ts`,
	}
	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{`src/**/*.spec.ts`, []string{paths[0], paths[1]}},
		{`src\**\*.spec.ts`, []string{paths[0], paths[1]}},
		{`src\**/*.spec.ts`, []string{paths[0], paths[1]}},
		{`src/*.spec.ts`, []string{paths[0]}},
		{`src\*.spec.ts`, []string{paths[0]}},
		{`**\parent\child\*`, []string{paths[5]}},
		// Like fd's glob, 'dir/**' matches entries below 'dir', not 'dir' itself.
		{`some/parent/child/**`, []string{paths[5]}},
		{`{src,some}/**/[et]*.spec.ts`, []string{paths[1]}},
		{`src/[!x]&b (1).ts`, []string{paths[4]}},
		// dot:true: `*` matches dotfiles; the lower-case pattern also matches `Src/Upper.TS` (smart case).
		{`src/*.ts`, []string{paths[0], paths[3], paths[4], paths[7], paths[8], paths[9]}},
		// noext:true: `+(a|b)` is literal.
		{`src/+(a|b).ts`, []string{paths[8]}},
		// fd's smart case: an all-lowercase pattern matches case-insensitively, a pattern with a capital does not.
		{`src/upper.ts`, []string{paths[7]}},
		{`Src/Upper.TS`, []string{paths[7]}},
		{`Src/upper.ts`, nil},
	} {
		inv, err := newFindFdInvocation(tc.pattern, search, 1000, true, true)
		if err != nil {
			t.Fatal(err)
		}
		if inv.pathMatcher == nil {
			t.Fatalf("%q: no path matcher", tc.pattern)
		}
		wantArgs := []string{"--color=never", "--hidden", "--", "", search}
		if !slices.Equal(inv.args, wantArgs) {
			t.Fatalf("%q: args = %q, want %q", tc.pattern, inv.args, wantArgs)
		}
		var got []string
		for _, path := range paths {
			if inv.pathMatcher.Match(path) {
				got = append(got, path)
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q matched %q, want %q", tc.pattern, got, tc.want)
		}
	}
}

// Basename patterns on Windows and every pattern on POSIX still go to fd's glob matching; a backslash stays a glob escape on POSIX and a
// separator on Windows.
func TestFindFdInvocationFdGlobArguments(t *testing.T) {
	const search = "/work/proj"
	for _, tc := range []struct {
		name      string
		pattern   string
		windows   bool
		insideGit bool
		want      []string
	}{
		{"basename on POSIX", "*.ts", false, true, []string{"--color=never", "--hidden", "--glob", "--max-results", "1001", "--", "*.ts", search}},
		{"basename on Windows", "*.ts", true, true, []string{"--color=never", "--hidden", "--glob", "--max-results", "1001", "--", "*.ts", search}},
		{"outside a repo", "*.ts", false, false, []string{"--color=never", "--hidden", "--no-require-git", "--glob", "--max-results", "1001", "--", "*.ts", search}},
		{"path pattern on POSIX", "src/**/*.ts", false, true, []string{"--color=never", "--hidden", "--glob", "--max-results", "1001", "--full-path", "--", "**/src/**/*.ts", search}},
		{"absolute path pattern on POSIX", "/abs/*.ts", false, true, []string{"--color=never", "--hidden", "--glob", "--max-results", "1001", "--full-path", "--", "/abs/*.ts", search}},
		{"backslash escape on POSIX", `src/\*.ts`, false, true, []string{"--color=never", "--hidden", "--glob", "--max-results", "1001", "--full-path", "--", `**/src/\*.ts`, search}},
		{"backslash basename on POSIX", `a\*b`, false, true, []string{"--color=never", "--hidden", "--glob", "--max-results", "1001", "--", `a\*b`, search}},
	} {
		inv, err := newFindFdInvocation(tc.pattern, search, findDefaultLimit, tc.windows, tc.insideGit)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(inv.args, tc.want) || inv.pathMatcher != nil {
			t.Errorf("%s: args = %q matcher = %v, want %q", tc.name, inv.args, inv.pathMatcher, tc.want)
		}
	}
	// A Windows pattern whose backslash makes it a path pattern goes to the matcher, not to fd.
	inv, err := newFindFdInvocation(`a\b`, search, findDefaultLimit, true, true)
	if err != nil || inv.pathMatcher == nil || slices.Contains(inv.args, "--glob") {
		t.Fatalf("backslash pattern on Windows: %+v, %v", inv, err)
	}
}

// find.ts: with a path matcher fd has no --max-results, so pi stops fd once one match past the limit arrives and keeps nothing after it.
func TestFindPathFilterStopsFdAtTheLimit(t *testing.T) {
	inv, err := newFindFdInvocation("src/*.ts", "/s", 2, true, true)
	if err != nil {
		t.Fatal(err)
	}
	stops := 0
	filter := &findPathFilter{matcher: inv.pathMatcher, limit: 2, stop: func() { stops++ }}
	// Chunks split a line, mix in non-matching entries and CRLF, and end with one entry past the limit.
	for _, chunk := range []string{"/s/src/a.t", "s\r\n/s/other/x.ts\n/s/src/b", ".ts\n  \n/s/src/c.ts\n", "/s/src/d.ts\n/s/src/e.ts\n"} {
		if n, err := filter.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	filter.finish()
	if got, want := filter.kept.String(), "/s/src/a.ts\n/s/src/b.ts\n/s/src/c.ts\n"; got != want {
		t.Fatalf("kept = %q, want %q", got, want)
	}
	if stops != 1 {
		t.Fatalf("fd was stopped %d times, want once", stops)
	}
}

func TestFindPathFilterKeepsAFinalLineWithoutANewline(t *testing.T) {
	inv, err := newFindFdInvocation("src/*.ts", "/s", 5, true, true)
	if err != nil {
		t.Fatal(err)
	}
	filter := &findPathFilter{matcher: inv.pathMatcher, limit: 5, stop: func() { t.Fatal("stopped below the limit") }}
	_, _ = filter.Write([]byte("/s/src/a.ts\n/s/src/b.ts"))
	filter.finish()
	if got, want := filter.kept.String(), "/s/src/a.ts\n/s/src/b.ts\n"; got != want {
		t.Fatalf("kept = %q, want %q", got, want)
	}
}

// Pi v1.5.1 a07f2512 (#11974) find-abort-before-spawn.test.ts: an abort during the .git lookup above the search path rejects the call and fd is never
// started. The fake fd records every start in a file.
func TestFindDoesNotStartFdAfterAbortDuringGitLookup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake fd is a shell script")
	}
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	fd := filepath.Join(dir, "fd")
	if err := os.WriteFile(fd, []byte("#!/bin/sh\necho started >> '"+started+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tool := &FindTool{CWD: dir, FdPath: fd, gitRepoLookup: func(string) bool {
		cancel()
		return false
	}}
	params, err := json.Marshal(map[string]any{"pattern": "*.ts"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(ctx, "call-1", params, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || result.Text() != "Operation aborted" {
		t.Fatalf("result = %q (error %v), want the aborted rejection", result.Text(), result.IsError)
	}
	if _, err := os.Stat(started); err == nil {
		t.Fatal("fd was started after the abort")
	}
}
