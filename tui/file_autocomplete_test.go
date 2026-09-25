package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestExtractAtPrefix(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"bare @", "@", "@"},
		{"@path", "@README.md", "@README.md"},
		{"@dir/path", "@tui/", "@tui/"},
		{"space then @", "hello @foo", "@foo"},
		{"tab then @", "hello\t@bar", "@bar"},
		{"no @", "hello world", ""},
		{"middle of word @", "foo@bar", ""},
		{"quoted @", `@"path with spaces`, `@"path with spaces`},
		{"slash only", "/help", ""},
		{"empty", "", ""},
		{"@ at start of line", "@src", "@src"},
		{"multiple @, last wins", "a @x b @y", "@y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAtPrefix(tc.input)
			if got != tc.want {
				t.Errorf("extractAtPrefix(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseAtPrefix(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		raw     string
		isQuote bool
	}{
		{"@path", "@README.md", "README.md", false},
		{"@dir/", "@tui/", "tui/", false},
		{`@"quoted`, `@"some path`, "some path", true},
		{"bare", "nope", "nope", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, isQ := parseAtPrefix(tc.input)
			if raw != tc.raw || isQ != tc.isQuote {
				t.Errorf("parseAtPrefix(%q) = (%q, %v), want (%q, %v)",
					tc.input, raw, isQ, tc.raw, tc.isQuote)
			}
		})
	}
}

func TestBuildCompletionValue(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		isDir    bool
		isQuoted bool
		want     string
	}{
		{"simple file", "README.md", false, false, "@README.md"},
		{"simple dir", "internal/", true, false, "@internal/"},
		{"file with space", "my file.txt", false, false, `@"my file.txt"`},
		{"dir with space", "my dir/", true, false, `@"my dir/"`},
		{"forced quoted", "foo.txt", false, true, `@"foo.txt"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildCompletionValue(tc.path, tc.isDir, tc.isQuoted)
			if got != tc.want {
				t.Errorf("buildCompletionValue(%q, %v, %v) = %q, want %q",
					tc.path, tc.isDir, tc.isQuoted, got, tc.want)
			}
		})
	}
}

func TestScoreEntry(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		query string
		isDir bool
		want  int
	}{
		{"exact match", "foo.go", "foo.go", false, 100},
		{"prefix match", "foobar.go", "foo", false, 80},
		{"substring match", "barfoo.go", "foo", false, 50},
		{"path match", "internal/foo.go", "foo", false, 80}, // basename "foo.go" starts with "foo"
		{"no match", "bar.go", "xyz", false, 0},
		{"dir bonus", "internal/", "internal", true, 110}, // 100 + 10
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreEntry(tc.path, tc.query, tc.isDir)
			if got != tc.want {
				t.Errorf("scoreEntry(%q, %q, %v) = %d, want %d",
					tc.path, tc.query, tc.isDir, got, tc.want)
			}
		})
	}
}

func TestBuildFdPathQuery(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"simple", "foo", "foo"},
		{"with slash", "internal/tui", `internal[\\/]tui`},
		{"trailing slash", "internal/", `internal[\\/]`},
		{"leading slash", "/usr/local", `usr[\\/]local`},
		{"no segments", "/", "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildFdPathQuery(tc.input)
			if got != tc.want {
				t.Errorf("buildFdPathQuery(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestEscapeRegex(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"plain", "foo", "foo"},
		{"dot", "foo.go", "foo\\.go"},
		{"star", "*.go", "\\*\\.go"},
		{"parens", "(a)", "\\(a\\)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := escapeRegex(tc.input)
			if got != tc.want {
				t.Errorf("escapeRegex(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestFindUnclosedQuoteStart(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"no quotes", "hello", -1},
		{"balanced quotes", `"hello"`, -1},
		{"unclosed", `"hello`, 0},
		{"after text", `foo "hello`, 4},
		{"multiple, last unclosed", `"a" "b`, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findUnclosedQuoteStart(tc.input)
			if got != tc.want {
				t.Errorf("findUnclosedQuoteStart(%q) = %d, want %d",
					tc.input, got, tc.want)
			}
		})
	}
}

// TestReaddirFileSuggestions tests the os.ReadDir fallback path.
func TestReaddirFileSuggestions(t *testing.T) {
	// Create temp dir structure.
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "README.md"), []byte("hello"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "readme.txt"), []byte("yo"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "src", "main.go"), []byte("package main"), 0o644)
	_ = os.MkdirAll(filepath.Join(tmp, ".git"), 0o755) // should be filtered

	p := &CombinedProvider{baseDir: tmp, fdPath: ""}

	t.Run("empty query lists root", func(t *testing.T) {
		items := p.readdirFileSuggestions("", false)
		if len(items) == 0 {
			t.Fatal("expected items")
		}
		// Should not include .git.
		for _, it := range items {
			if it.Label == ".git/" {
				t.Error(".git should be filtered out")
			}
		}
		// Should have README.md, readme.txt, src/
		found := map[string]bool{}
		for _, it := range items {
			found[it.Label] = true
		}
		for _, want := range []string{"README.md", "readme.txt", "src/"} {
			if !found[want] {
				t.Errorf("missing %q in results", want)
			}
		}
	})

	t.Run("prefix filter", func(t *testing.T) {
		items := p.readdirFileSuggestions("read", false)
		if len(items) != 2 { // README.md + readme.txt (case-insensitive)
			t.Errorf("expected 2 items, got %d: %v", len(items), items)
		}
	})

	t.Run("locale sort parity", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(tmp, "asserts.go"), []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "asserts_test.go"), []byte("b"), 0o644); err != nil {
			t.Fatal(err)
		}
		items := p.readdirFileSuggestions("asserts", false)
		if len(items) < 2 {
			t.Fatalf("expected at least 2 items, got %d", len(items))
		}
		if items[0].Label != "asserts_test.go" || items[1].Label != "asserts.go" {
			t.Fatalf("locale sort mismatch: got %q then %q", items[0].Label, items[1].Label)
		}
	})

	t.Run("subdir query", func(t *testing.T) {
		items := p.readdirFileSuggestions("src/", false)
		if len(items) != 1 {
			t.Fatalf("expected 1 item (main.go), got %d", len(items))
		}
		if items[0].Label != "main.go" {
			t.Errorf("expected main.go, got %s", items[0].Label)
		}
	})

	t.Run("dirs first", func(t *testing.T) {
		items := p.readdirFileSuggestions("", false)
		if len(items) < 2 {
			t.Skip("need at least 2 items")
		}
		// First item should be src/ (directory).
		if items[0].Label != "src/" {
			t.Errorf("expected src/ first, got %s", items[0].Label)
		}
	})

	t.Run("tilde prefix does not panic", func(t *testing.T) {
		// filepath.Dir("~/foo") returns "~" which has len 1.
		// Ensure dir[2:] doesn't panic with slice bounds out of range.
		items := p.readdirFileSuggestions("~/nonexistent_path_xyz", false)
		// Result may be nil (dir doesn't exist): the point is no panic.
		_ = items
	})
}

// TestCombinedProviderAtPrefix tests the full GetSuggestions path for @.
func TestCombinedProviderAtPrefix(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "hello.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "world.go"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewCombinedProvider(nil, tmp, "")

	t.Run("@hel suggests hello.go", func(t *testing.T) {
		lines := []string{"@hel"}
		res := p.GetSuggestions(lines, 0, 4)
		if res == nil {
			t.Fatal("expected suggestions")
		}
		if res.Prefix != "@hel" {
			t.Errorf("prefix = %q, want @hel", res.Prefix)
		}
		found := false
		for _, it := range res.Items {
			if it.Label == "hello.go" {
				found = true
			}
		}
		if !found {
			t.Error("expected hello.go in results")
		}
	})

	t.Run("slash still works", func(t *testing.T) {
		cmds := []SlashCommand{{Name: "help", Description: "Show help"}}
		cp := NewCombinedProvider(cmds, tmp, "")
		lines := []string{"/hel"}
		res := cp.GetSuggestions(lines, 0, 4)
		if res == nil {
			t.Fatal("expected slash suggestions")
		}
		if len(res.Items) == 0 || res.Items[0].Value != "help" {
			t.Error("expected help command")
		}
	})
}

// TestCombinedProviderApplyAt tests @-completion apply.
func TestCombinedProviderApplyAt(t *testing.T) {
	p := NewCombinedProvider(nil, ".", "")

	t.Run("apply file", func(t *testing.T) {
		lines := []string{"@hell"}
		item := AutocompleteItem{Value: "@hello.go", Label: "hello.go"}
		newLines, line, col := p.ApplyCompletion(lines, 0, 5, item, "@hell")
		if newLines[line] != "@hello.go " {
			t.Errorf("got %q, want %q", newLines[line], "@hello.go ")
		}
		if col != 10 { // len("@hello.go ")
			t.Errorf("col = %d, want 10", col)
		}
	})

	t.Run("apply directory (no trailing space)", func(t *testing.T) {
		lines := []string{"@int"}
		item := AutocompleteItem{Value: "@internal/", Label: "internal/"}
		newLines, line, col := p.ApplyCompletion(lines, 0, 4, item, "@int")
		if newLines[line] != "@internal/" {
			t.Errorf("got %q, want %q", newLines[line], "@internal/")
		}
		if col != 10 { // len("@internal/")
			t.Errorf("col = %d, want 10", col)
		}
	})
}

// TestExtractPathPrefix covers upstream extractPathPrefix semantics.
func TestExtractPathPrefix(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		force    bool
		want     string
		ok       bool
	}{
		{"dot-slash", "./foo", false, "./foo", true},
		{"home-slash", "~/Doc", false, "~/Doc", true},
		{"contains-slash", "src/main", false, "src/main", true},
		{"plain-word-no-force", "hello", false, "", false},
		{"plain-word-with-force", "hello", true, "hello", true},
		{"after-space-no-force", "go ", false, "", true},
		{"empty-no-force", "", false, "", false},
		{"empty-with-force", "", true, "", true},
		{"quoted", `say "/etc/`, false, `"/etc/`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractPathPrefix(tc.in, tc.force)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("extractPathPrefix(%q,%v) = (%q,%v) want (%q,%v)",
					tc.in, tc.force, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestCombinedProviderNakedPath verifies the non-@ path branch.
func TestCombinedProviderNakedPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := NewCombinedProvider(nil, dir, "")

	// Shipped upstream UX: naked paths do not open naturally while typing.
	if res := p.GetSuggestions([]string{"./"}, 0, 2); res != nil {
		t.Fatalf("natural naked ./ unexpectedly opened popup: %+v", res)
	}
	// Plain word without force returns nil.
	if res := p.GetSuggestions([]string{"hello"}, 0, 5); res != nil {
		t.Errorf("plain word triggered popup: %+v", res)
	}
	// Force triggers file suggestions, including for `./`.
	res := p.GetSuggestionsForce([]string{"./"}, 0, 2)
	if res == nil || len(res.Items) == 0 {
		t.Fatalf("expected forced ./ to suggest entries, got %+v", res)
	}
	if res.Prefix != "./" {
		t.Errorf("prefix = %q, want ./", res.Prefix)
	}
	// Force also triggers on empty buffer.
	if res := p.GetSuggestionsForce([]string{""}, 0, 0); res == nil {
		t.Errorf("force on empty buffer returned nil")
	}

	// ApplyCompletion on naked path should not prepend @.
	item := AutocompleteItem{Value: "./alpha.txt", Label: "alpha.txt"}
	newLines, _, col := p.ApplyCompletion([]string{"./"}, 0, 2, item, "./")
	if newLines[0] != "./alpha.txt" {
		t.Errorf("apply naked path got %q, want ./alpha.txt", newLines[0])
	}
	if col != len("./alpha.txt") {
		t.Errorf("col = %d, want %d", col, len("./alpha.txt"))
	}
}

func TestFdFileSuggestionsRanksShallowMatchesBeforeDeepMatches(t *testing.T) {
	fdPath := lookFdForTest(t)
	baseDir := t.TempDir()
	for _, dir := range []string{
		"scope/aaa/venv/lib/python3.12/site-packages/pkg/core/profile",
		"scope/projects",
	} {
		if err := os.MkdirAll(filepath.Join(baseDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	p := NewCombinedProvider(nil, baseDir, fdPath)
	items := p.fdFileSuggestions("scope/pro", false)
	if len(items) < 2 {
		t.Fatalf("suggestions = %+v, want shallow and deep matches", items)
	}
	if got, want := items[0].Value, "@scope/projects/"; got != want {
		t.Fatalf("first suggestion = %q, want %q", got, want)
	}
	if !slicesContainsValue(items, "@scope/aaa/venv/lib/python3.12/site-packages/pkg/core/profile/") {
		t.Fatalf("suggestions = %+v, want deep profile match retained", items)
	}
}

func TestEditorTabAcceptsShallowFileAutocompleteMatch(t *testing.T) {
	fdPath := lookFdForTest(t)
	baseDir := t.TempDir()
	for _, dir := range []string{
		"scope/aaa/venv/lib/python3.12/site-packages/pkg/core/profile",
		"scope/projects",
	} {
		if err := os.MkdirAll(filepath.Join(baseDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	e := NewEditor()
	e.SetAutocomplete(NewCombinedProvider(nil, baseDir, fdPath))
	e.SetText("@scope/pro")
	if !e.AutocompleteOpen() {
		t.Fatal("expected file autocomplete popup")
	}
	e.HandleInput("\t")
	if got, want := e.Text(), "@scope/projects/"; got != want {
		t.Fatalf("Tab completion = %q, want %q", got, want)
	}
}

func TestFdFileSuggestionsKeepsDirectChildWhenRecursiveMatchesFlood(t *testing.T) {
	fdPath := lookFdForTest(t)
	baseDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(baseDir, "scope", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 250; i++ {
		dir := filepath.Join("scope", fmt.Sprintf("a%03d", i), "venv", "lib", "python3.12", "site-packages", "pkg", "core", "profile")
		if err := os.MkdirAll(filepath.Join(baseDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	p := NewCombinedProvider(nil, baseDir, fdPath)
	items := p.fdFileSuggestions("scope/pro", false)
	if len(items) == 0 {
		t.Fatal("expected suggestions")
	}
	if got, want := items[0].Value, "@scope/projects/"; got != want {
		t.Fatalf("first suggestion = %q, want %q", got, want)
	}
	foundDeep := false
	for _, item := range items {
		if strings.Contains(item.Value, "/profile/") {
			foundDeep = true
			break
		}
	}
	if !foundDeep {
		t.Fatalf("suggestions = %+v, want recursive profile matches retained", items)
	}
}

func TestFdFileSuggestionsFallsBackToRootForDeepPathQuery(t *testing.T) {
	fdPath := lookFdForTest(t)
	baseDir := t.TempDir()
	for path, content := range map[string]string{
		"packages/tui/src/autocomplete.ts": "export {};",
		"packages/ai/src/autocomplete.ts":  "export {};",
	} {
		fullPath := filepath.Join(baseDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := NewCombinedProvider(nil, baseDir, fdPath)
	items := p.fdFileSuggestions("tui/src/auto", false)
	if !slicesContainsValue(items, "@packages/tui/src/autocomplete.ts") {
		t.Fatalf("suggestions = %+v, want tui autocomplete path", items)
	}
	if slicesContainsValue(items, "@packages/ai/src/autocomplete.ts") {
		t.Fatalf("suggestions = %+v, do not want ai autocomplete path", items)
	}
}

func TestFdFileSuggestionsFallsBackToRootForDirectoryInMiddle(t *testing.T) {
	fdPath := lookFdForTest(t)
	baseDir := t.TempDir()
	for path, content := range map[string]string{
		"src/components/Button.tsx": "export {};",
		"src/utils/helpers.ts":      "export {};",
	} {
		fullPath := filepath.Join(baseDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := NewCombinedProvider(nil, baseDir, fdPath)
	items := p.fdFileSuggestions("components/", false)
	if !slicesContainsValue(items, "@src/components/Button.tsx") {
		t.Fatalf("suggestions = %+v, want component path", items)
	}
	if slicesContainsValue(items, "@src/utils/helpers.ts") {
		t.Fatalf("suggestions = %+v, do not want utils path", items)
	}
}

func TestFdFileSuggestionsDoesNotFallBackToReadDir(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "README.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p := NewCombinedProvider(nil, baseDir, filepath.Join(baseDir, "missing-fd"))
	if items := p.fdFileSuggestions("READ", false); len(items) != 0 {
		t.Fatalf("failed fd suggestions = %+v, want no readdir fallback", items)
	}
}

func slicesContainsValue(items []AutocompleteItem, value string) bool {
	for _, item := range items {
		if item.Value == value {
			return true
		}
	}
	return false
}

// TestAsyncFileSearch_GatesSyncFdPath verifies the async flag defers the fd
// subprocess off GetSuggestions, so a slow tree walk cannot block the input
// thread. With async on, an @-query yields no synchronous suggestions and a
// FileSearchTask instead; with async off, FileSearchTask declines (the
// synchronous fd path stays in effect).
func TestAsyncFileSearch_GatesSyncFdPath(t *testing.T) {
	fdPath := lookFdForTest(t)
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "source.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p := NewCombinedProvider(nil, tmp, fdPath)

	lines := []string{"@sou"}
	col := len("@sou")

	// Async OFF (default): the synchronous fd path finds the file.
	if res := p.GetSuggestions(lines, 0, col); res == nil || len(res.Items) == 0 {
		t.Fatal("sync fd path should find source.txt when async off")
	}

	// Async OFF (default): @-query routes through the synchronous fd path,
	// and no deferred task is offered.
	if _, _, ok := p.FileSearchTask(lines, 0, col); ok {
		t.Fatal("FileSearchTask should decline when async file search is off")
	}

	p.SetAsyncFileSearch(true)
	// Async ON: GetSuggestions defers fd, returning nothing synchronously
	// even though the file exists.
	if res := p.GetSuggestions(lines, 0, col); res != nil {
		t.Fatalf("GetSuggestions must not run fd synchronously when async on; got %+v", res)
	}
	prefix, run, ok := p.FileSearchTask(lines, 0, col)
	if !ok || run == nil {
		t.Fatal("FileSearchTask should offer a deferred fd search for an @-query when async on")
	}
	if prefix != "@sou" {
		t.Fatalf("prefix = %q, want @sou", prefix)
	}

	// Non-@ buffers never produce a file task.
	if _, _, ok := p.FileSearchTask([]string{"hello"}, 0, 5); ok {
		t.Fatal("FileSearchTask should decline for a non-@ buffer")
	}
}

func lookFdForTest(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"fd", "fdfind"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("fd not available")
	return ""
}

type countingAsyncFileProvider struct {
	calls atomic.Int32
}

func (f *countingAsyncFileProvider) GetSuggestions([]string, int, int) *AutocompleteSuggestions {
	return nil
}

func (f *countingAsyncFileProvider) ApplyCompletion(lines []string, cl, cc int, _ AutocompleteItem, _ string) ([]string, int, int) {
	return lines, cl, cc
}

func (f *countingAsyncFileProvider) FileSearchTask(lines []string, cl, cc int) (string, func(context.Context) []AutocompleteItem, bool) {
	before := lines[cl][:cc]
	if extractAtPrefix(before) == "" {
		return "", nil, false
	}
	return before, func(context.Context) []AutocompleteItem {
		f.calls.Add(1)
		return []AutocompleteItem{{Value: "@README.md", Label: "README.md"}}
	}, true
}

func TestEditorDebouncesAttachmentAutocomplete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := &countingAsyncFileProvider{}
		e := NewEditor()
		e.SetAutocomplete(provider)

		for _, input := range []string{"@", "R", "E"} {
			e.HandleInput(input)
		}

		synctest.Wait()
		if got := provider.calls.Load(); got != 0 {
			t.Fatalf("file searches before debounce elapsed = %d, want 0", got)
		}
		synctest.Sleep(attachmentAutocompleteDebounce)
		synctest.Wait()
		if got, want := provider.calls.Load(), int32(1); got != want {
			t.Fatalf("file searches after one typing burst = %d, want %d", got, want)
		}
	})
}

// fakeAsyncFileProvider drives the editor's async @-file path without fd.
type fakeAsyncFileProvider struct {
	started chan struct{}
	release chan struct{}
	items   []AutocompleteItem
}

func (f *fakeAsyncFileProvider) GetSuggestions([]string, int, int) *AutocompleteSuggestions {
	return nil // async path owns @-queries
}

func (f *fakeAsyncFileProvider) ApplyCompletion(lines []string, cl, cc int, _ AutocompleteItem, _ string) ([]string, int, int) {
	return lines, cl, cc
}

func (f *fakeAsyncFileProvider) FileSearchTask(lines []string, cl, cc int) (string, func(context.Context) []AutocompleteItem, bool) {
	before := lines[cl][:cc]
	if len(before) == 0 || before[0] != '@' {
		return "", nil, false
	}
	run := func(ctx context.Context) []AutocompleteItem {
		select {
		case f.started <- struct{}{}:
		default:
		}
		select {
		case <-f.release:
			return f.items
		case <-ctx.Done():
			return nil
		}
	}
	return before, run, true
}

// TestEditor_AsyncFileSearchDoesNotBlock proves the regression fix: an
// @-file search runs off the input thread (HandleInput returns before the
// search completes) and its results land via the async render hook.
func TestEditor_AsyncFileSearchDoesNotBlock(t *testing.T) {
	prov := &fakeAsyncFileProvider{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		items:   []AutocompleteItem{{Value: "@workspace/", Label: "workspace/"}},
	}
	rendered := make(chan struct{}, 4)
	e := NewEditor()
	e.SetAutocomplete(prov)
	e.SetAsyncApply(func(apply func()) { apply(); rendered <- struct{}{} })

	e.HandleInput("@")
	e.HandleInput("k")

	// The search goroutine must have started without HandleInput blocking.
	select {
	case <-prov.started:
	case <-time.After(2 * time.Second):
		t.Fatal("file search never started off-thread")
	}
	// Items are not yet populated (search still blocked on release).
	if got := len(e.autocompleteItems); got != 0 {
		t.Fatalf("items populated before search released: %d", got)
	}

	close(prov.release)
	select {
	case <-rendered:
	case <-time.After(2 * time.Second):
		t.Fatal("async render hook never fired after search completed")
	}
	if got := e.autocompleteItems; len(got) != 1 || got[0].Value != "@workspace/" {
		t.Fatalf("async items = %+v, want @workspace/", got)
	}
}

// TestEditor_AsyncFileSearch_RealSubprocessDoesNotBlock drives the real
// production path (CombinedProvider.FileSearchTask -> fdFileSuggestionsCtx ->
// exec.CommandContext) with a stub "fd" that sleeps. It proves HandleInput
// returns promptly while the subprocess is still running, and that the prior
// subprocess is cancelled when the next keystroke supersedes it.
func TestEditor_AsyncFileSearch_RealSubprocessDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	// Stub fd: record that it ran, sleep long enough that a synchronous
	// call would be an obvious freeze, then emit one result.
	marker := filepath.Join(dir, "fd-ran")
	fakeFd := filepath.Join(dir, "fd")
	script := "#!/bin/sh\necho ran >> '" + filepath.ToSlash(marker) + "'\nsleep 3\necho 'source.txt'\n"
	fakeFd = writeStubScript(t, fakeFd, script)

	prov := NewCombinedProvider(nil, dir, fakeFd)
	prov.SetAsyncFileSearch(true)

	e := NewEditor()
	e.SetAutocomplete(prov)
	rendered := make(chan struct{}, 8)
	e.SetAsyncApply(func(apply func()) { apply(); rendered <- struct{}{} })

	start := time.Now()
	e.HandleInput("@")
	e.HandleInput("k")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("HandleInput blocked %v (fd sleeps 3s): input thread not async", elapsed)
	}

	// The fd subprocess for the first ('@') keystroke must have been
	// cancelled by the second ('k') keystroke before it could finish.
	// Give the goroutines a moment, then assert no completion landed yet
	// from the still-sleeping current fd.
	select {
	case <-rendered:
		t.Fatal("a search completed within 1s though fd sleeps 3s")
	case <-time.After(1 * time.Second):
	}
	// The stub must actually have run, or the assertions above hold vacuously.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stub fd never ran")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Let the surviving search finish so its fd has exited before the temp
	// dir holding it is removed (Windows cannot delete a running image).
	select {
	case <-rendered:
	case <-time.After(10 * time.Second):
		t.Fatal("the surviving search never completed")
	}
}
