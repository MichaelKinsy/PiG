package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// TestReaddirFileSuggestions tests direct path listing.
func TestReaddirFileSuggestions(t *testing.T) {
	// Create temp dir structure.
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "README.md"), []byte("hello"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "readme.txt"), []byte("yo"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "src", "main.go"), []byte("package main"), 0o644)
	_ = os.MkdirAll(filepath.Join(tmp, ".git"), 0o755)

	p := &CombinedProvider{baseDir: tmp, fdPath: ""}

	t.Run("empty query lists root", func(t *testing.T) {
		items := p.readdirFileSuggestions("", false)
		if len(items) == 0 {
			t.Fatal("expected items")
		}
		// Pi autocomplete.ts:623-634 includes .git in direct listings; only fd attachment search excludes it.
		found := map[string]bool{}
		for _, it := range items {
			found[it.Label] = true
		}
		for _, want := range []string{".git/", "README.md", "readme.txt", "src/"} {
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
		if len(items) < 2 || items[0].Label != ".git/" || items[1].Label != "src/" {
			t.Fatalf("expected .git/ and src/ before files, got %v", items)
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

	// Pi autocomplete.ts:750 requires fd for attachment queries.
	p := NewCombinedProvider(nil, tmp, requireFDForTest(t))

	t.Run("@hel suggests hello.go", func(t *testing.T) {
		lines := []string{"@hel"}
		res := p.GetSuggestions(context.Background(), lines, 0, 4, AutocompleteSuggestionOptions{})
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

	// Pi autocomplete.ts:750 returns no attachment suggestions without fd.
	t.Run("@hel without fd suggests nothing", func(t *testing.T) {
		withoutFd := NewCombinedProvider(nil, tmp, "")
		if res := withoutFd.GetSuggestions(context.Background(), []string{"@hel"}, 0, 4, AutocompleteSuggestionOptions{}); res != nil {
			t.Fatalf("suggestions without fd = %+v, want none", res.Items)
		}
	})

	t.Run("slash still works", func(t *testing.T) {
		cmds := []SlashCommand{{Name: "help", Description: "Show help"}}
		cp := NewCombinedProvider(cmds, tmp, "")
		lines := []string{"/hel"}
		res := cp.GetSuggestions(context.Background(), lines, 0, 4, AutocompleteSuggestionOptions{})
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

	// upstream: packages/tui/src/autocomplete.ts:380-393. The provider answers path queries; the Editor, not the provider, gates automatic queries.
	natural := p.GetSuggestions(context.Background(), []string{"./"}, 0, 2, AutocompleteSuggestionOptions{})
	if natural == nil || natural.Prefix != "./" || len(natural.Items) != 2 || natural.Items[0].Value != "./beta/" || natural.Items[1].Value != "./alpha.txt" {
		t.Fatalf("path suggestions=%+v, want ./beta/ then ./alpha.txt", natural)
	}
	editor := NewEditor()
	editor.SetAutocomplete(p)
	editor.HandleInput(".")
	editor.HandleInput("/")
	if editor.AutocompleteOpen() {
		t.Fatal("typing a naked path opened the editor popup")
	}
	// Plain word without force returns nil.
	if res := p.GetSuggestions(context.Background(), []string{"hello"}, 0, 5, AutocompleteSuggestionOptions{}); res != nil {
		t.Errorf("plain word triggered popup: %+v", res)
	}
	// Force triggers file suggestions, including for `./`.
	res := p.GetSuggestions(context.Background(), []string{"./"}, 0, 2, AutocompleteSuggestionOptions{Force: true})
	if res == nil || len(res.Items) == 0 {
		t.Fatalf("expected forced ./ to suggest entries, got %+v", res)
	}
	if res.Prefix != "./" {
		t.Errorf("prefix = %q, want ./", res.Prefix)
	}
	// Force also triggers on empty buffer.
	if res := p.GetSuggestions(context.Background(), []string{""}, 0, 0, AutocompleteSuggestionOptions{Force: true}); res == nil {
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
	e.HandleInput("@scope/pro")
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
	if res := p.GetSuggestions(context.Background(), lines, 0, col, AutocompleteSuggestionOptions{}); res == nil || len(res.Items) == 0 {
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
	if res := p.GetSuggestions(context.Background(), lines, 0, col, AutocompleteSuggestionOptions{}); res != nil {
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
	path, err := lookFD()
	if err != nil {
		t.Skip("fd not available")
	}
	return path
}

// requireFDForTest fails when fd is absent: Pi autocomplete.ts:750 requires fd for attachment queries.
func requireFDForTest(t *testing.T) string {
	t.Helper()
	path, err := lookFD()
	if err != nil {
		t.Fatal("attachment queries require fd:", err)
	}
	return path
}

// lookFD resolves fd as the tools manager does (tools.go systemToolConfigs): fd, then Debian and Ubuntu's fdfind.
func lookFD() (string, error) {
	var firstErr error
	for _, name := range []string{"fd", "fdfind"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", firstErr
}

type countingAsyncFileProvider struct {
	calls atomic.Int32
}

func (f *countingAsyncFileProvider) GetSuggestions(context.Context, []string, int, int, AutocompleteSuggestionOptions) *AutocompleteSuggestions {
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

func (f *fakeAsyncFileProvider) GetSuggestions(context.Context, []string, int, int, AutocompleteSuggestionOptions) *AutocompleteSuggestions {
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

// blockedFdStub writes a stub "fd" that records "<pid> <arguments>" in the returned started file, stays running until the returned release file
// exists, then prints keep.txt. The test decides when the subprocess ends, so no assertion depends on how fast a process starts or on a sleep
// outlasting the machine's load. Creating release lets every stub, current and future, finish; the stubs are released at cleanup so no fd
// is left running when the temp dir holding it is removed (Windows cannot delete a running image).
func blockedFdStub(t *testing.T, dir string) (fd, started, release string) {
	t.Helper()
	started = filepath.Join(dir, "fd-started")
	release = filepath.Join(dir, "fd-release")
	script := "#!/bin/sh\necho \"$$ $*\" >> '" + filepath.ToSlash(started) + "'\n" +
		"while [ ! -e '" + filepath.ToSlash(release) + "' ]; do sleep 0.05; done\n" +
		"echo 'keep.txt'\n"
	fd = writeStubScript(t, filepath.Join(dir, "fd"), script)
	t.Cleanup(func() { releaseFdStubs(release) })
	return fd, started, release
}

// releaseFdStubs lets every blockedFdStub waiting on release finish.
func releaseFdStubs(release string) {
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		panic(err)
	}
}

// awaitFdStart blocks until a stub fd has recorded an invocation whose last argument is query, so the search for that query is known to be
// running in a subprocess.
func awaitFdStart(t *testing.T, started, query string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		recorded, _ := os.ReadFile(started)
		for line := range strings.SplitSeq(string(recorded), "\n") {
			if strings.HasSuffix(line, " "+query) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no fd was started for %q; started: %q", query, recorded)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestEditor_AsyncFileSearch_RealSubprocessDoesNotBlock drives the real
// production path (CombinedProvider.FileSearchTask -> fdFileSuggestionsCtx ->
// exec.CommandContext) with a stub "fd" that keeps running until the test
// releases it. It proves HandleInput returns while the subprocess is still
// running, that nothing is delivered while it runs, and that the search the
// last keystroke started delivers its result once the subprocess exits.
func TestEditor_AsyncFileSearch_RealSubprocessDoesNotBlock(t *testing.T) {
	fakeFd, started, release := blockedFdStub(t, t.TempDir())
	prov := NewCombinedProvider(nil, t.TempDir(), fakeFd)
	prov.SetAsyncFileSearch(true)

	e := NewEditor()
	e.SetAutocomplete(prov)
	rendered := make(chan struct{}, 8)
	e.SetAsyncApply(func(apply func()) { apply(); rendered <- struct{}{} })

	// No stub can exit before release, so a HandleInput that waits for fd never returns.
	typed := make(chan struct{})
	go func() {
		defer close(typed)
		e.HandleInput("@")
		e.HandleInput("k")
	}()
	select {
	case <-typed:
	case <-time.After(time.Minute):
		t.Fatal("HandleInput is waiting for the fd subprocess: input thread not async")
	}

	// The search for "k" is running in a real subprocess, and the subprocess has not finished.
	awaitFdStart(t, started, "k")
	select {
	case <-rendered:
		t.Fatal("a search delivered a result while its fd was still running")
	default:
	}

	releaseFdStubs(release)
	select {
	case <-rendered:
	case <-time.After(time.Minute):
		t.Fatal("the surviving search never completed")
	}
	if got := e.autocompleteItems; len(got) != 1 || got[0].Value != "@keep.txt" {
		t.Fatalf("items = %+v, want the one @keep.txt from the fd that ran for %q", got, "@k")
	}
}

// TestFileSearchTask_RealSubprocessIsCancelled proves cancelling the context a
// search runs under ends its fd subprocess: the stub cannot exit before the
// test releases it, and the test never does, so the search returns only
// because exec.CommandContext killed the process.
//
// Not on Windows: the stub is a #!/bin/sh script, and killing the sh.exe
// launcher leaves the shell that runs the loop holding stdout open. Upstream's
// search also kills only the process it spawned and waits for "close"
// (packages/tui/src/autocomplete.ts:207-221), so it waits the same way; a real
// fd.exe has no such child.
func TestFileSearchTask_RealSubprocessIsCancelled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a #!/bin/sh stub fd outlives its launcher on Windows; upstream waits for stdout close the same way")
	}
	fakeFd, started, _ := blockedFdStub(t, t.TempDir())
	prov := NewCombinedProvider(nil, t.TempDir(), fakeFd)
	prov.SetAsyncFileSearch(true)
	prefix, run, ok := prov.FileSearchTask([]string{"@k"}, 0, len("@k"))
	if !ok || prefix != "@k" {
		t.Fatalf("FileSearchTask = %q, %v, want @k, true", prefix, ok)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan []AutocompleteItem, 1)
	go func() { finished <- run(ctx) }()
	awaitFdStart(t, started, "k")
	select {
	case items := <-finished:
		t.Fatalf("the search returned %+v while its fd was still running", items)
	default:
	}

	cancel()
	select {
	case items := <-finished:
		if len(items) != 0 {
			t.Fatalf("a cancelled search returned %+v", items)
		}
	case <-time.After(time.Minute):
		t.Fatal("cancelling the search left its fd subprocess running")
	}
}
