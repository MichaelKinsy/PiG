package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type autocompleteTree struct {
	dirs  []string
	files map[string]string
}

func putAutocompleteTree(t *testing.T, base string, tree autocompleteTree) {
	t.Helper()
	for _, dir := range tree.dirs {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range tree.files {
		path = filepath.Join(base, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
func autocompleteValues(result *AutocompleteSuggestions) []string {
	if result == nil {
		return nil
	}
	out := make([]string, len(result.Items))
	for i, item := range result.Items {
		out[i] = item.Value
	}
	return out
}

// Go's editor/provider cursors use byte offsets. Each source test's UTF-16
// cursor is translated to the same text boundary; expected cursors use bytes too.
func upstreamSuggestions(p *CombinedProvider, line string, col int, force bool) *AutocompleteSuggestions {
	if force {
		return p.GetSuggestions(context.Background(), []string{line}, 0, col, AutocompleteSuggestionOptions{Force: true})
	}
	return p.GetSuggestions(context.Background(), []string{line}, 0, col, AutocompleteSuggestionOptions{})
}
func requireAutocomplete(t *testing.T, p *CombinedProvider, line string, col int, force bool) *AutocompleteSuggestions {
	t.Helper()
	r := upstreamSuggestions(p, line, col, force)
	if r == nil {
		t.Fatalf("no suggestions for %q at %d force=%v", line, col, force)
	}
	return r
}
func requireAutocompleteValues(t *testing.T, r *AutocompleteSuggestions, want []string) {
	t.Helper()
	if got := autocompleteValues(r); !slices.Equal(got, want) {
		t.Fatalf("values=%q want %q", got, want)
	}
}
func autocompleteItem(t *testing.T, r *AutocompleteSuggestions, value string) AutocompleteItem {
	t.Helper()
	if r != nil {
		for _, item := range r.Items {
			if item.Value == value {
				return item
			}
		}
	}
	t.Fatalf("missing %q in %#v", value, r)
	return AutocompleteItem{}
}
func assertAutocompleteApplied(t *testing.T, p *CombinedProvider, line string, col int, item AutocompleteItem, prefix, want string, wantCol int) {
	t.Helper()
	lines, row, column := p.ApplyCompletion([]string{line}, 0, col, item, prefix)
	if len(lines) != 1 || lines[0] != want || row != 0 || column != wantCol {
		t.Fatalf("applied=(%q,%d,%d), want (%q,0,%d)", lines, row, column, want, wantCol)
	}
}
func newAutocompleteFixture(t *testing.T, withFD bool) (root, base, outside string, p *CombinedProvider) {
	t.Helper()
	root = t.TempDir()
	base = filepath.Join(root, "cwd")
	outside = filepath.Join(root, "outside")
	for _, d := range []string{base, outside} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fd := ""
	if withFD {
		fd = requireFDForTest(t)
	}
	return root, base, outside, NewCombinedProvider(nil, base, fd)
}

func TestEditorDirectPathCompletionCaller(t *testing.T) {
	// Drive the Editor's real task owner and provider application for autocomplete.test.ts:551,582,691,740.
	for _, tc := range []struct {
		name, line, before, path, want string
		cursor                         int
	}{
		{"dot slash CJK", "查看，./文档/说 后文", "查看，./文档/说", "文档/说明.md", "查看，./文档/说明.md 后文", 13},
		{"quoted file", `查看，"资料 归档/说"后文`, `查看，"资料 归档/说`, "资料 归档/说明.md", `查看，"资料 归档/说明.md"后文`, 16},
		{"quoted directory", "my", "my", "my folder/test.txt", `"my folder/"`, 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				_, base, _, p := newAutocompleteFixture(t, false)
				putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{tc.path: "text"}})
				e, flush := completionUpstreamEditor(t)
				e.SetAutocomplete(p)
				e.SetText(tc.line)
				e.cursor = [2]int{0, utf16Length(tc.before)}
				e.HandleInput("\t")
				flush()
				if e.Text() != tc.want || e.cursor != [2]int{0, tc.cursor} {
					t.Fatalf("completion=%q cursor=%v, want %q cursor=%d", e.Text(), e.cursor, tc.want, tc.cursor)
				}
			})
		})
	}
}

func BenchmarkApplyQuotedPathCompletion(b *testing.B) {
	p := NewCombinedProvider(nil, "", "")
	line, prefix := `查看，"资料 归档/说"后文`, `"资料 归档/说`
	col := strings.Index(line, `"后文`)
	item := AutocompleteItem{Value: `"资料 归档/说明.md"`, Label: "说明.md"}
	b.ReportAllocs()
	for b.Loop() {
		p.ApplyCompletion([]string{line}, 0, col, item, prefix)
	}
}

func TestUpstreamAutocompletePathPrefix(t *testing.T) {
	for _, tc := range []struct {
		name, line, prefix string
		col                int
		required, absent   bool
	}{
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:59
		{"extracts / from 'hey /' when forced", "hey /", "/", 5, true, false},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:73
		{"extracts /A from '/A' when forced", "/A", "/A", 2, false, false},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:89
		{"does not trigger for slash commands", "/model", "", 6, false, true},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:101
		{"triggers for absolute paths after slash command argument", "/command /", "/", 10, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewCombinedProvider(nil, "/tmp", "")
			r := upstreamSuggestions(p, tc.line, tc.col, true)
			if tc.absent {
				if r != nil {
					t.Fatalf("unexpected suggestions: %#v", r)
				}
				return
			}
			if tc.required && r == nil {
				t.Fatal("required suggestions missing")
			}
			if r != nil && r.Prefix != tc.prefix {
				t.Fatalf("prefix=%q want %q", r.Prefix, tc.prefix)
			}
		})
	}
}

func TestUpstreamAutocompleteFD(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		tree, outside           autocompleteTree
		links                   map[string]string
		line                    string
		equal, include, exclude []string
		first                   string
	}{
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:134
		{name: "returns all files and folders for empty @ query", tree: autocompleteTree{dirs: []string{"src"}, files: map[string]string{"README.md": "readme"}}, line: "@", equal: []string{"@README.md", "@src/"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:223
		{name: "matches file with extension in query", tree: autocompleteTree{files: map[string]string{"file.txt": "content"}}, line: "@file.txt", include: []string{"@file.txt"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:238
		{name: "filters are case insensitive", tree: autocompleteTree{dirs: []string{"src"}, files: map[string]string{"README.md": "readme"}}, line: "@re", equal: []string{"@README.md"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:254
		{name: "ranks directories before files", tree: autocompleteTree{dirs: []string{"src"}, files: map[string]string{"src.txt": "text"}}, line: "@src", first: "@src/", include: []string{"@src.txt"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:272
		{name: "returns nested file paths", tree: autocompleteTree{files: map[string]string{"src/index.ts": "export {};\n"}}, line: "@index", include: []string{"@src/index.ts"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:287
		{name: "matches deeply nested paths", tree: autocompleteTree{files: map[string]string{"packages/tui/src/autocomplete.ts": "export {};", "packages/ai/src/autocomplete.ts": "export {};"}}, line: "@tui/src/auto", include: []string{"@packages/tui/src/autocomplete.ts"}, exclude: []string{"@packages/ai/src/autocomplete.ts"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:304
		{name: "matches directory in middle of path with --full-path", tree: autocompleteTree{files: map[string]string{"src/components/Button.tsx": "export {};", "src/utils/helpers.ts": "export {};"}}, line: "@components/", include: []string{"@src/components/Button.tsx"}, exclude: []string{"@src/utils/helpers.ts"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:321
		{name: "scopes fuzzy search to relative directories and searches recursively", outside: autocompleteTree{files: map[string]string{"nested/alpha.ts": "export {};", "nested/deeper/also-alpha.ts": "export {};", "nested/deeper/zzz.ts": "export {};"}}, line: "@../outside/a", include: []string{"@../outside/nested/alpha.ts", "@../outside/nested/deeper/also-alpha.ts"}, exclude: []string{"@../outside/nested/deeper/zzz.ts"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:340
		{name: "ranks shallower same-score @ matches before deeper matches", tree: autocompleteTree{dirs: []string{"scope/aaa/venv/lib/python3.12/site-packages/pkg/core/profile", "scope/projects"}}, line: "@scope/pro", first: "@scope/projects/", include: []string{"@scope/aaa/venv/lib/python3.12/site-packages/pkg/core/profile/"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:393
		{name: "includes hidden paths but excludes .git", tree: autocompleteTree{dirs: []string{".pi", ".github", ".git"}, files: map[string]string{".pi/config.json": "{}", ".github/workflows/ci.yml": "name: ci", ".git/config": "[core]"}}, line: "@", include: []string{"@.pi/", "@.github/"}, exclude: []string{"@.git", "@.git/"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:413
		{name: "follows symlinked directories for fuzzy @ search", tree: autocompleteTree{files: map[string]string{"dir/some_file.txt": "real"}}, outside: autocompleteTree{files: map[string]string{"some_file.txt": "symlinked"}}, links: map[string]string{"symlinked_dir": "../outside"}, line: "@some", include: []string{"@dir/some_file.txt", "@symlinked_dir/some_file.txt"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:435
		{name: "returns symlinked directories when matching their name", outside: autocompleteTree{files: map[string]string{"nested/file.txt": "symlinked"}}, links: map[string]string{"symlinked_dir": "../outside"}, line: "@symlinked", include: []string{"@symlinked_dir/"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:451
		{name: "returns symlinked files without requiring type l", tree: autocompleteTree{files: map[string]string{"original.txt": "content"}}, links: map[string]string{"link.txt": "original.txt"}, line: "@link", include: []string{"@link.txt"}},
		// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:501
		{name: "continues autocomplete inside quoted @ paths", tree: autocompleteTree{files: map[string]string{"my folder/test.txt": "content", "my folder/other.txt": "content"}}, line: `@"my folder/"`, include: []string{`@"my folder/test.txt"`, `@"my folder/other.txt"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, base, outside, p := newAutocompleteFixture(t, true)
			putAutocompleteTree(t, base, tc.tree)
			putAutocompleteTree(t, outside, tc.outside)
			for name, target := range tc.links {
				// Node's untyped symlinkSync picks a directory symlink for a directory target. A junction exercises that alias here without requiring Windows symlink privilege.
				link := filepath.Join(base, name)
				if info, err := os.Stat(filepath.Join(base, target)); err == nil && info.IsDir() {
					testenv.RequireDirectoryLink(t, target, link)
				} else {
					testenv.RequireSymlink(t, target, link)
				}
			}
			col := len(tc.line)
			if strings.HasSuffix(tc.line, `"`) {
				col--
			}
			r := requireAutocomplete(t, p, tc.line, col, false)
			values := autocompleteValues(r)
			if tc.first != "" && (len(values) == 0 || values[0] != tc.first) {
				t.Fatalf("first of %q must be %q", values, tc.first)
			}
			for _, v := range tc.include {
				if !slices.Contains(values, v) {
					t.Fatalf("missing %q in %q", v, values)
				}
			}
			for _, v := range tc.exclude {
				for _, got := range values {
					if got == v || (v == "@.git/" && strings.HasPrefix(got, v)) {
						t.Fatalf("unexpected %q in %q", v, values)
					}
				}
			}
			if tc.equal != nil {
				slices.Sort(values)
				want := slices.Clone(tc.equal)
				slices.Sort(want)
				if !slices.Equal(values, want) {
					t.Fatalf("values=%q want %q", values, want)
				}
			}
		})
	}
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:150
	t.Run("recognizes @ after CJK punctuation without consuming the preceding text", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, true)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"README.md": "readme"}})
		befores := []string{"查看，", "\u3000"}
		for _, r := range "，．：；！？（）［］｛｝“”‘’…—。、「」『』《》【】" {
			befores = append(befores, string(r))
		}
		for _, before := range befores {
			for _, force := range []bool{false, true} {
				line := before + "@REA"
				r := requireAutocomplete(t, p, line, len(line), force)
				if r.Prefix != "@REA" {
					t.Fatalf("prefix=%q", r.Prefix)
				}
				requireAutocompleteValues(t, r, []string{"@README.md"})
				want := before + "@README.md "
				assertAutocompleteApplied(t, p, line, len(line), r.Items[0], r.Prefix, want, len(want))
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:170
	t.Run("preserves CJK characters and embedded @ in attachment paths", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, true)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"文档/说明.md": "text", "文档@备份/说明.md": "backup"}})
		for _, before := range []string{"", "查看，"} {
			for _, dir := range []string{"文档", "文档@备份"} {
				prefix := "@" + dir + "/说"
				line := before + prefix
				r := requireAutocomplete(t, p, line, len(line), false)
				if r.Prefix != prefix {
					t.Fatalf("prefix=%q want %q", r.Prefix, prefix)
				}
				requireAutocompleteValues(t, r, []string{"@" + dir + "/说明.md"})
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:190
	t.Run("completes quoted CJK attachments after prose without losing path segments or quotes", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, true)
		for _, separator := range []string{" ", "\u3000", "，", "。"} {
			dir := "我的" + separator + "文档"
			putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{dir + "/说明.md": "text", "文档/说明.md": "not the quoted path"}})
			line := `查看：@"` + dir + `/说"后文`
			col := strings.Index(line, `"后文`)
			r := requireAutocomplete(t, p, line, col, false)
			if r.Prefix != `@"`+dir+`/说` {
				t.Fatalf("prefix=%q", r.Prefix)
			}
			requireAutocompleteValues(t, r, []string{`@"` + dir + `/说明.md"`})
			want := `查看：@"` + dir + `/说明.md" 后文`
			assertAutocompleteApplied(t, p, line, col, r.Items[0], r.Prefix, want, len(`查看：@"`+dir+`/说明.md" `))
		}
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:212
	t.Run("does not interpret email addresses or @ after ASCII or CJK letters as attachment prefixes", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, true)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"README.md": "readme", "example.com": "text"}})
		for _, before := range []string{"user", "查看", "あ", "カ", "한", "ㄅ", "𠮷", "か\u3099", "禰\U000e0100", "々", "Ａ"} {
			for _, name := range []string{"REA", "example.com"} {
				line := before + "@" + name
				if r := upstreamSuggestions(p, line, len(line), false); r != nil {
					t.Fatalf("unexpected suggestions for %q: %#v", line, r)
				}
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:354
	t.Run("includes scoped direct children when recursive @ matches are flooded", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, true)
		dirs := []string{"scope/projects"}
		for i := range 250 {
			dirs = append(dirs, fmt.Sprintf("scope/a%03d/venv/lib/python3.12/site-packages/pkg/core/profile", i+1))
		}
		putAutocompleteTree(t, base, autocompleteTree{dirs: dirs})
		line := "@scope/pro"
		r := requireAutocomplete(t, p, line, len(line), false)
		values := autocompleteValues(r)
		if len(values) == 0 || values[0] != "@scope/projects/" {
			t.Fatalf("direct child not first: %q", values)
		}
		if !slices.ContainsFunc(values, func(v string) bool { return strings.Contains(v, "/profile/") }) {
			t.Fatal("deep fuzzy matches missing")
		}
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:376
	t.Run("quotes paths containing whitespace or CJK punctuation for @ suggestions", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, true)
		for _, separator := range []string{" ", "\u3000", "，", "。"} {
			dir := "my" + separator + "folder"
			putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{dir + "/test.txt": "content"}})
			line := "@my"
			r := requireAutocomplete(t, p, line, len(line), false)
			item := autocompleteItem(t, r, `@"`+dir+`/"`)
			lines, _, col := p.ApplyCompletion([]string{line}, 0, len(line), item, r.Prefix)
			continued := requireAutocomplete(t, p, lines[0], col, false)
			if continued.Prefix != `@"`+dir+"/" {
				t.Fatalf("continued prefix=%q", continued.Prefix)
			}
			autocompleteItem(t, continued, `@"`+dir+`/test.txt"`)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:468
	t.Run("returns the same @ suggestions when the cwd path contains the query", func(t *testing.T) {
		root, _, _, p := newAutocompleteFixture(t, true)
		normal := filepath.Join(root, "cwd-normal")
		queryPath := filepath.Join(root, "cwd-plan-repro")
		tree := autocompleteTree{dirs: []string{"packages/coding-agent/examples/extensions/plan-mode"}, files: map[string]string{"packages/coding-agent/examples/extensions/plan-mode/README.md": "readme", "packages/tui/docs/plan.md": "plan"}}
		putAutocompleteTree(t, normal, tree)
		putAutocompleteTree(t, queryPath, tree)
		normalize := func(r *AutocompleteSuggestions) []string {
			var items []string
			if r != nil {
				for _, item := range r.Items {
					items = append(items, item.Label+" :: "+item.Description)
				}
			}
			slices.Sort(items)
			return items
		}
		line := "@plan"
		a := normalize(upstreamSuggestions(NewCombinedProvider(nil, normal, p.fdPath), line, len(line), false))
		b := normalize(upstreamSuggestions(NewCombinedProvider(nil, queryPath, p.fdPath), line, len(line), false))
		if !slices.Equal(a, b) {
			t.Fatalf("normal=%q query-in-path=%q", a, b)
		}
		for _, want := range []string{"plan-mode/ :: packages/coding-agent/examples/extensions/plan-mode", "plan.md :: packages/tui/docs/plan.md"} {
			if !slices.Contains(a, want) {
				t.Fatalf("missing %q in %q", want, a)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:519
	t.Run("applies quoted @ completion without duplicating closing quote", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, true)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"my folder/test.txt": "content"}})
		line := `@"my folder/te"`
		col := len(line) - 1
		r := requireAutocomplete(t, p, line, col, false)
		item := autocompleteItem(t, r, `@"my folder/test.txt"`)
		lines, _, _ := p.ApplyCompletion([]string{line}, 0, col, item, r.Prefix)
		if lines[0] != `@"my folder/test.txt" ` {
			t.Fatalf("applied=%q", lines)
		}
	})
}

func TestUpstreamAutocompleteDirectPaths(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:551
	t.Run("completes Chinese path prefixes after whitespace or CJK punctuation on Tab", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"说明.md": "file", "文档/说明.md": "nested file"}})
		completions := [][2]string{{"说", "说明.md"}, {"文", "文档/"}, {"文档/说", "文档/说明.md"}, {"./文档/说", "./文档/说明.md"}}
		if runtime.GOOS != "windows" {
			completions = append(completions, [2]string{base + "/文档/说", base + "/文档/说明.md"})
		}
		for _, separator := range " \t\u3000\u00a0，：；。！？（「《" {
			for _, completion := range completions {
				before := "查看𠮷" + string(separator)
				prefix, value := completion[0], completion[1]
				line := before + prefix + " 后文"
				col := len(before) + len(prefix)
				r := requireAutocomplete(t, p, line, col, true)
				if r.Prefix != prefix {
					t.Fatalf("prefix=%q want %q", r.Prefix, prefix)
				}
				requireAutocompleteValues(t, r, []string{value})
				assertAutocompleteApplied(t, p, line, col, r.Items[0], r.Prefix, before+value+" 后文", len(before)+len(value))
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:582
	t.Run("treats unquoted separators as boundaries even when a matching literal path exists", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"归档/说明.md": "other"}})
		for _, separator := range []string{" ", "\u3000", "，", "。"} {
			dir := "资料" + separator + "归档"
			putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{dir + "/说明.md": "archive"}})
			for _, marker := range []string{"", "@"} {
				line := marker + dir + "/说"
				r := requireAutocomplete(t, p, line, len(line), true)
				if r.Prefix != "归档/说" {
					t.Fatalf("prefix=%q", r.Prefix)
				}
				requireAutocompleteValues(t, r, []string{"归档/说明.md"})
			}
			line := `查看，"` + dir + `/说"后文`
			col := strings.Index(line, `"后文`)
			r := requireAutocomplete(t, p, line, col, true)
			if r.Prefix != `"`+dir+`/说` {
				t.Fatalf("quoted prefix=%q", r.Prefix)
			}
			requireAutocompleteValues(t, r, []string{`"` + dir + `/说明.md"`})
			want := `查看，"` + dir + `/说明.md"后文`
			assertAutocompleteApplied(t, p, line, col, r.Items[0], r.Prefix, want, len(`查看，"`+dir+`/说明.md"`))
			missing := `查看，"不存在` + separator + `归档/说`
			if got := upstreamSuggestions(p, missing, len(missing), true); got != nil {
				t.Fatalf("missing directory suggested: %#v", got)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:614
	t.Run("handles an empty prefix after whitespace or CJK punctuation consistently", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"说明.md": "text"}})
		for _, separator := range []string{" ", "\t", "\u3000", "，", "。"} {
			for _, force := range []bool{false, true} {
				line := "查看" + separator
				r := requireAutocomplete(t, p, line, len(line), force)
				if r.Prefix != "" {
					t.Fatalf("prefix=%q", r.Prefix)
				}
				requireAutocompleteValues(t, r, []string{"说明.md"})
			}
		}
		if r := upstreamSuggestions(p, "", 0, false); r != nil {
			t.Fatalf("empty text produced %#v", r)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:632
	t.Run("preserves CJK characters in unprefixed Tab completions", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"文档/说明.md": "text"}})
		line := "文档/说"
		r := requireAutocomplete(t, p, line, len(line), true)
		if r.Prefix != line {
			t.Fatalf("prefix=%q", r.Prefix)
		}
		requireAutocompleteValues(t, r, []string{"文档/说明.md"})
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:645
	t.Run("preserves ./ prefix when completing paths", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"update.sh": "#!/bin/bash", "utils.ts": "export {};"}})
		r := requireAutocomplete(t, p, "./up", 4, true)
		autocompleteItem(t, r, "./update.sh")
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:662
	t.Run("preserves ./ prefix for directory completions", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		putAutocompleteTree(t, base, autocompleteTree{dirs: []string{"src"}, files: map[string]string{"src/index.ts": "export {};"}})
		r := requireAutocomplete(t, p, "./sr", 4, true)
		autocompleteItem(t, r, "./src/")
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:691
	t.Run("quotes paths containing whitespace or CJK punctuation for direct completion", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		for _, separator := range []string{" ", "\u3000", "，", "。"} {
			dir := "my" + separator + "folder"
			putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{dir + "/test.txt": "content"}})
			line := "my"
			r := requireAutocomplete(t, p, line, len(line), true)
			item := autocompleteItem(t, r, `"`+dir+`/"`)
			lines, _, col := p.ApplyCompletion([]string{line}, 0, len(line), item, r.Prefix)
			continued := requireAutocomplete(t, p, lines[0], col, true)
			if continued.Prefix != `"`+dir+"/" {
				t.Fatalf("continued prefix=%q", continued.Prefix)
			}
			requireAutocompleteValues(t, continued, []string{`"` + dir + `/test.txt"`})
		}
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:711
	t.Run("keeps quoted directories before files", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		putAutocompleteTree(t, base, autocompleteTree{dirs: []string{"z folder", "z，folder"}, files: map[string]string{"a.txt": "text"}})
		r := requireAutocomplete(t, p, "", 0, true)
		var dirs []bool
		for _, item := range r.Items {
			dirs = append(dirs, strings.HasSuffix(item.Label, "/"))
		}
		if !slices.Equal(dirs, []bool{true, true, false}) {
			t.Fatalf("directory order=%v", dirs)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:722
	t.Run("continues completion inside quoted paths", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"my folder/test.txt": "content", "my folder/other.txt": "content"}})
		line := `"my folder/"`
		r := requireAutocomplete(t, p, line, len(line)-1, true)
		autocompleteItem(t, r, `"my folder/test.txt"`)
		autocompleteItem(t, r, `"my folder/other.txt"`)
	})
	// .upstream/v0.87.1/packages/tui/test/autocomplete.test.ts:740
	t.Run("applies quoted completion without duplicating closing quote", func(t *testing.T) {
		_, base, _, p := newAutocompleteFixture(t, false)
		putAutocompleteTree(t, base, autocompleteTree{files: map[string]string{"my folder/test.txt": "content"}})
		line := `"my folder/te"`
		col := len(line) - 1
		r := requireAutocomplete(t, p, line, col, true)
		item := autocompleteItem(t, r, `"my folder/test.txt"`)
		lines, _, _ := p.ApplyCompletion([]string{line}, 0, col, item, r.Prefix)
		if lines[0] != `"my folder/test.txt"` {
			t.Fatalf("applied=%q", lines)
		}
	})
}
