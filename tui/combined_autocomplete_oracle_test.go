package tui

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/tui/src/autocomplete.ts

// CombinedAutocompleteProvider.getSuggestions and applyCompletion for slash command names and direct path completion (no fd) over a directory tree whose names have
// spaces, quotes, dots, unicode and mixed case, with the text before the cursor shaped as command names, relative/absolute/home paths, quoted paths, and text after the cursor.
func TestCombinedAutocompleteMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(419))
	root := t.TempDir()
	home := filepath.Join(root, "home")
	base := filepath.Join(root, "base")
	names := []string{"alpha.txt", "Alpha.md", "beta", "beta two", "gamma's", `dq"x`, ".hidden", ".git", "日本語.txt", "e\u0301.go", "\u0130stanbul.txt", "istanbul.md", "Istanbul", "src", "src2", "my docs", "UPPER", "zeta.TXT", "a b c", "a,b", "(paren)", "[br]", "-dash", "node_modules", "ünï", "a#b", "tab\tname"}
	dirs := []string{base, home, filepath.Join(base, "src"), filepath.Join(base, "my docs"), filepath.Join(home, "docs")}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range dirs {
		for _, name := range names {
			path := filepath.Join(dir, name)
			if _, err := os.Lstat(path); err == nil {
				continue
			}
			if rng.Intn(3) == 0 {
				_ = os.MkdirAll(path, 0o755)
			} else if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Skipf("filesystem rejects %q: %v", name, err)
			}
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home directory is USERPROFILE on Windows, for Node and for PiG
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	stem := func() string {
		return pick("", "\u0130", "i", "\u0130s", "a", "al", "A", "b", "be", "g", "s", "src/", "src/a", "my", "my docs/", "my\\ docs/", ".", "./", "./s", "../", "../b", "~/", "~/d", "~", "docs/", "/", base+"/", base+"/s", home+"/", "日", "e", "é", ".h", ".g", "-", "(", "[", "u", "ü", "zeta", "UP", "a\\", "a b", "a#", "~x", "~/..", "~//", "..", "a//", "src//", "./src/../", "src/./", "//", "/.", "./.", ".//s", "..//", "src\\a", "src\\", "~\\", "~/src/../d")
	}
	var cases []struct {
		Before string `json:"before"`
		After  string `json:"after"`
		Force  bool   `json:"force"`
	}
	for range 3000 {
		var before string
		switch rng.Intn(9) {
		case 0:
			before = "/" + pick("", "m", "mo", "model", "h", "he", "sk", "skill", "review", "x", "x-i", "zz", "model ", "help ", "help x")
		case 1:
			before = pick("  /", "\t/", "\u00a0/", "\ufeff/", "/") + pick("m", "", "h")
		case 2:
			before = `"` + stem()
		case 3:
			before = pick("cat ", "open ", "x=", "(", "[", "'", "`", "\"", "a ", "- ", "> ") + stem()
		case 4:
			before = "@" + stem()
		case 5:
			before = pick("see ", "", "cat ") + `@"` + stem()
		case 6:
			before = "/model " + stem()
		default:
			before = stem()
		}
		after := pick("", "", "", `"`, " rest", "x", `"x`)
		cases = append(cases, struct {
			Before string `json:"before"`
			After  string `json:"after"`
			Force  bool   `json:"force"`
		}{before, after, rng.Intn(3) == 0})
	}
	type item struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}
	type result struct {
		Items   []item   `json:"items"`
		Prefix  string   `json:"prefix"`
		Null    bool     `json:"null"`
		Applied []string `json:"applied"`
		Col     int      `json:"col"`
	}
	var want []result
	pioracle.Run(t, `
const mod = await load("pi-tui/autocomplete.js");
const commands = [
	{ name: "model", description: "Select model" },
	{ name: "help", description: "Show help", argumentHint: "[topic]" },
	{ name: "hint-only", argumentHint: "<x>" },
	{ name: "skill:review", description: "Review code" },
	{ name: "skill:model-tuner" },
	{ name: "mod" },
	{ value: "x-item", label: "x-item", description: "an item" },
];
const provider = new mod.CombinedAutocompleteProvider(commands, input.base, null);
const out = [];
for (const c of input.cases) {
	const line = c.before + c.after;
	const lines = [line];
	const suggestions = await provider.getSuggestions(lines, 0, c.before.length, { signal: new AbortController().signal, force: c.force });
	if (!suggestions) { out.push({ items: [], prefix: "", null: true, applied: [], col: 0 }); continue; }
	const items = suggestions.items.map((i) => ({ value: i.value, label: i.label, description: i.description ?? "" }));
	const applied = provider.applyCompletion(lines, 0, c.before.length, suggestions.items[0], suggestions.prefix);
	out.push({ items, prefix: suggestions.prefix, null: false, applied: applied.lines, col: applied.cursorCol });
}
emit(out);`, map[string]any{"base": base, "cases": cases}, &want, "HOME="+home, "USERPROFILE="+home)
	commands := []SlashCommand{
		{Name: "model", Description: "Select model"},
		{Name: "help", Description: "Show help", ArgumentHint: "[topic]"},
		{Name: "hint-only", ArgumentHint: "<x>"},
		{Name: "skill:review", Description: "Review code"},
		{Name: "skill:model-tuner"},
		{Name: "mod"},
		{Value: "x-item", Label: "x-item", Description: "an item"},
	}
	provider := NewCombinedProvider(commands, base, "")
	failures, answered, applied := 0, 0, 0
	for i, c := range cases {
		line := c.Before + c.After
		lines := []string{line}
		got := result{Null: true, Applied: []string{}, Items: []item{}}
		if suggestions := provider.GetSuggestions(context.Background(), lines, 0, len(c.Before), AutocompleteSuggestionOptions{Force: c.Force}); suggestions != nil {
			answered++
			got.Null = false
			got.Prefix = suggestions.Prefix
			for _, it := range suggestions.Items {
				got.Items = append(got.Items, item(it))
			}
			newLines, _, col := provider.ApplyCompletion(lines, 0, len(c.Before), suggestions.Items[0], suggestions.Prefix)
			applied++
			got.Applied = newLines
			got.Col = jsstring.Length(newLines[0][:min(col, len(newLines[0]))])
		}
		if !reflect.DeepEqual(got, want[i]) {
			if failures++; failures <= 6 {
				t.Errorf("before=%q after=%q force=%v:\n  PiG %+v\n  Pi  %+v", c.Before, c.After, c.Force, summarize(got.Items, got.Prefix, got.Applied, got.Col), summarize(want[i].Items, want[i].Prefix, want[i].Applied, want[i].Col))
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d cases differ", failures, len(cases))
	}
	if answered < 700 {
		t.Fatalf("only %d of %d cases produced suggestions", answered, len(cases))
	}
}

func summarize[T any](items []T, prefix string, applied []string, col int) []any {
	first := []T(nil)
	if len(items) > 0 {
		first = items[:min(len(items), 4)]
	}
	return []any{len(items), first, prefix, applied, col}
}

// The @ attachment search: fd walks (non-recursive and recursive), scoped queries (dir/ prefixes, ~/, absolute), quoted prefixes, scoring, ordering, the 20-entry cap, and the
// value/label/description of each entry. The tree stays under fd's 100-result cap so fd's unordered output cannot change the result.
func TestCombinedAutocompleteAtSearchMatchesPi(t *testing.T) {
	fd := requireFDForTest(t)
	rng := rand.New(rand.NewSource(523))
	root := t.TempDir()
	home := filepath.Join(root, "home")
	base := filepath.Join(root, "base")
	layout := []string{
		"README.md", "readme.txt", "Makefile", "main.go", "main_test.go", "go.mod", ".env", ".hidden/secret.txt", ".git/config", "my notes.txt", "gamma's file.md", "日本語.txt", "UPPER.GO",
		"src/", "src/app.ts", "src/app.test.ts", "src/main.go", "src/components/", "src/components/Button.tsx", "src/components/button.css", "src/components/index.ts", "src/utils/", "src/utils/path.ts", "src/utils/pathx.ts",
		"docs/", "docs/readme.md", "docs/guide/", "docs/guide/intro.md", "docs/guide/advanced setup.md", "my docs/", "my docs/a.md", "my docs/b c.md",
		"internal/", "internal/pkg/", "internal/pkg/pkg.go", "internal/pkg/pkg_test.go", "node_modules/", "node_modules/lib/", "node_modules/lib/index.js", "test/", "test/app_test.go", "a/b/c/d/e/deep.txt", "\u0130dir/", "\u0130dir/notes.md", "x\u0130/", "x\u0130/a.md", "docs/\u0130", "\u0130x.md",
	}
	for _, dir := range []string{base, home} {
		for _, entry := range layout {
			path := filepath.Join(dir, entry)
			if strings.HasSuffix(entry, "/") {
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home directory is USERPROFILE on Windows, for Node and for PiG
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	queries := []string{"", "r", "re", "read", "readme", "READ", "m", "ma", "main", "go", ".go", "_test", "test", "app", "but", "button", "comp", "components", "src", "src/", "src/c", "src/comp", "src/components/", "src/components/b", "src/u", "docs/", "docs/g", "docs/guide/", "doc", "guide", "intro", "adv", "my", "my ", "my d", "my docs/", "gamma", "日", "日本", "pkg", "internal/p", "internal/pkg/", "deep", "a/b", "a/b/c/", "node", "lib", "index", ".", ".h", ".env", ".git", "ts", "x", "zzz", "~/", "~/s", "~/src/", "~/src/c", "~/docs/g", "~", "src/../", "./", "./s", "./src/", "src//", "src/../c", "/nonexistent/x", "UPPER", "upper", "P", "pa", "path", "pathx", "s/a", "c/b", "u/p", "t_t", "m.g", "r.m"}
	type atCase struct {
		Before string `json:"before"`
		After  string `json:"after"`
	}
	var cases []atCase
	for range 120 {
		query := pick(queries...)
		if rng.Intn(6) == 0 && query != "" {
			query = strings.ToUpper(query[:1]) + query[1:]
		}
		before := pick("@", "@", "@", "see @", "cat (@", "x=@", "'@", "[@") + query
		if rng.Intn(5) == 0 {
			before = pick("", "see ", "cat ") + `@"` + query
		}
		cases = append(cases, atCase{before, pick("", "", "", `"`, " rest")})
	}
	// Queries whose only match is in the directory part of a path (scoreEntry's 30-point full-path tier) and one-letter scopes that name no directory.
	for _, query := range []string{"s/a", "c/b", "u/p", "e/d", "s/b", "e/i", "g/a", "s/c"} {
		cases = append(cases, atCase{"@" + query, ""})
	}
	cases = append(cases,
		// scoreEntry lowercases with toLowerCase: U+0130 becomes "i" + U+0307, so only docs/U+0130 is an exact (100-point) match and ranks above the shallower U+0130x.md (80 points).
		atCase{"@\u0130", ""},
		// A query with a slash that names no directory searches full paths (fd --full-path): each match scores only by its path (autocomplete.ts scoreEntry 30).
		atCase{"@s/a", ""}, atCase{"see @guide/a", " rest"}, atCase{`@"utils/p`, `"`},
		// A directory named with U+0130: toLowerCase maps it to i + U+0307, so only the full mapping finds it in the path tier (autocomplete.ts scoreEntry filePath.toLowerCase()).
		atCase{"@\u0130/a", ""}, atCase{"@\u0130dir/", ""}, atCase{"@\u0130", ""},
	)
	type item struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}
	type result struct {
		Items   []item   `json:"items"`
		Prefix  string   `json:"prefix"`
		Null    bool     `json:"null"`
		Applied []string `json:"applied"`
		Col     int      `json:"col"`
	}
	var want []result
	pioracle.Run(t, `
const mod = await load("pi-tui/autocomplete.js");
const provider = new mod.CombinedAutocompleteProvider([], input.base, input.fd);
const out = [];
for (const c of input.cases) {
	const line = c.before + c.after;
	const lines = [line];
	const suggestions = await provider.getSuggestions(lines, 0, c.before.length, { signal: new AbortController().signal, force: false });
	if (!suggestions) { out.push({ items: [], prefix: "", null: true, applied: [], col: 0 }); continue; }
	const items = suggestions.items.map((i) => ({ value: i.value, label: i.label, description: i.description ?? "" }));
	const applied = provider.applyCompletion(lines, 0, c.before.length, suggestions.items[0], suggestions.prefix);
	out.push({ items, prefix: suggestions.prefix, null: false, applied: applied.lines, col: applied.cursorCol });
}
emit(out);`, map[string]any{"base": base, "fd": fd, "cases": cases}, &want, "HOME="+home, "USERPROFILE="+home)
	provider := NewCombinedProvider(nil, base, fd)
	failures, answered, scoped := 0, 0, 0
	for i, c := range cases {
		line := c.Before + c.After
		lines := []string{line}
		got := result{Null: true, Applied: []string{}, Items: []item{}}
		if suggestions := provider.GetSuggestions(context.Background(), lines, 0, len(c.Before), AutocompleteSuggestionOptions{}); suggestions != nil {
			answered++
			got.Null = false
			got.Prefix = suggestions.Prefix
			for _, it := range suggestions.Items {
				got.Items = append(got.Items, item(it))
			}
			newLines, _, col := provider.ApplyCompletion(lines, 0, len(c.Before), suggestions.Items[0], suggestions.Prefix)
			got.Applied = newLines
			got.Col = jsstring.Length(newLines[0][:min(col, len(newLines[0]))])
			if strings.Contains(suggestions.Prefix, "/") {
				scoped++
			}
		}
		if !reflect.DeepEqual(got, want[i]) {
			if failures++; failures <= 6 {
				t.Errorf("before=%q after=%q:\n  PiG %+v\n  Pi  %+v", c.Before, c.After, summarize(got.Items, got.Prefix, got.Applied, got.Col), summarize(want[i].Items, want[i].Prefix, want[i].Applied, want[i].Col))
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d cases differ", failures, len(cases))
	}
	if answered < 60 || scoped < 25 {
		t.Fatalf("%d of %d cases answered, %d scoped", answered, len(cases), scoped)
	}
}
