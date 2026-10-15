package ignorerules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pi: packages/coding-agent/src/core/package-manager.ts addIgnoreRules and collectFiles: the lines of .gitignore, prefixed with the directory, go to `ig.add`; `ig.ignores(relPath)` decides a file and `ig.ignores(relPath + "/")` a directory (npm `ignore`).
func TestIgnoredMatchesPiIgnorePackage(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261010))
	names := []string{"a", "b", "src", "node_modules", "dist", "foo.ts", "bar.js", "test.spec.ts", ".hidden", "build", "lib", "Build", "SRC", "Foo.TS"}
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	// Atoms are the ordinary gitignore vocabulary. Rare syntax (classes, escapes, odd spaces) is probed by the second corpus below.
	atoms := []string{"*", "**", "?", "*.ts", "*.js", "foo*", "?oo", "**/", "/**", "*/", "a/**/b", "*.log", "/", "!", "#"}
	rare := []string{"[a-c]", "[!a]", "\\*", "\\#", "\\!", "\\ ", " ", "[", "]", "..", "{a,b}", "~", "$", "\t"}
	// Bracket expressions as the `ignore` package scans them (scanBracket): POSIX classes, a leading `]`, escaped members and range bounds, out-of-order ranges,
	// non-ASCII members, a class that names `/`, an unknown class and an unclosed class name.
	brackets := []string{"[[:alpha:]]", "[[:digit:]]", "[![:alnum:]]", "[[:punct:]]", "[[:space:]]", "[[:upper:]x]", "[]a]", "[!]]", "[\\]]", "[a-\\z]", "[c-a]", "[z-a]b",
		"[é]", "[!é]", "[a/b]", "[!/]", "[.-0]", "[[:bogus:]]", "[[:alpha]", "[[a]", "[a-]", "[-a]", "[^b]"}
	prefixes := []string{"", "", "sub/", "a/b/"}
	type probe struct {
		content string
		prefix  string
		paths   [][2]any
	}
	build := func(vocabulary []string, rareRate int) []probe {
		var probes []probe
		for range 1500 {
			var lines []string
			for range 1 + rng.Intn(4) {
				var b strings.Builder
				for range 1 + rng.Intn(3) {
					switch {
					case rng.Intn(3) != 0:
						b.WriteString(pick(names))
					case rareRate > 0 && rng.Intn(rareRate) == 0:
						b.WriteString(pick(rare))
					default:
						b.WriteString(pick(vocabulary))
					}
					if rng.Intn(3) == 0 {
						b.WriteString("/")
					}
				}
				lines = append(lines, b.String())
			}
			eol := "\n"
			if rng.Intn(4) == 0 {
				eol = "\r\n"
			}
			prefix := pick(prefixes)
			var paths [][2]any
			for range 12 {
				var parts []string
				if prefix != "" && rng.Intn(4) != 0 {
					parts = append(parts, strings.Split(strings.TrimSuffix(prefix, "/"), "/")...)
				}
				for range 1 + rng.Intn(3) {
					parts = append(parts, pick(names))
				}
				paths = append(paths, [2]any{strings.Join(parts, "/"), rng.Intn(2) == 0})
			}
			probes = append(probes, probe{strings.Join(lines, eol) + eol, prefix, paths})
		}
		return probes
	}
	bracketNames := []string{"a", "b", "z", "A", "1", "x1", "é", "É", "a b", "x]", "]", "-", "x.y", "0", "c", "B", "ab", "[", "_"}
	buildBrackets := func() []probe {
		saved := names
		names = bracketNames
		defer func() { names = saved }()
		// A class that names `/` or a range across it still never matches the separator.
		return append(build(brackets, 0),
			probe{"a[.-0]b\n", "", [][2]any{{"a/b", false}, {"a.b", false}, {"a0b", true}}},
			probe{"a[a/b]b\nx[/]y\n", "", [][2]any{{"a/b", false}, {"aab", false}, {"x/y", true}}},
			probe{"[!a]b\n", "sub/", [][2]any{{"sub/xb", false}, {"sub/ab", false}}})
	}
	// Cases the random corpora reach rarely: a trailing `**` keeps the stars before it pinned, `/**/` (from `//**/`) matches every directory even where a negation
	// re-includes the parent, and a line of only U+0085 is not blank to JavaScript's trim.
	fixed := []probe{
		{"*.ts**\n", "", [][2]any{{"test.spec.ts", false}, {"a.tsx", false}, {"test.ts", true}}},
		{"*.ts*\n", "", [][2]any{{"test.spec.ts", false}, {"a.tsx", false}}},
		{"//**/\n!a/\n", "", [][2]any{{"a/b/c", false}, {"a/b", true}, {"a/b", false}}},
		{"**/**/\n!a/\n", "", [][2]any{{"a/b/c", false}, {"a/b", true}}},
		{"/**\n!a/\n", "", [][2]any{{"a/b", false}}},
		{"\u0085\n", "", [][2]any{{"\u0085", false}, {"a", false}}},
		{"\u0085\n", "sub/", [][2]any{{"sub/\u0085", false}}},
	}
	for corpus, probes := range map[string][]probe{"ordinary": build(atoms, 0), "rare": build(atoms, 3), "brackets": buildBrackets(), "fixed": fixed} {
		var cases []map[string]any
		for _, p := range probes {
			cases = append(cases, map[string]any{"content": p.content, "prefix": p.prefix, "paths": p.paths})
		}
		in, _ := json.Marshal(cases)
		cmd := exec.Command("node", "testdata/ignore.mjs")
		cmd.Stdin = bytes.NewReader(in)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		raw, err := cmd.Output()
		if err != nil {
			t.Fatalf("node: %v\n%s", err, stderr.String())
		}
		var want [][]any
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		var differences []string
		ignoredCount, total := 0, 0
		for i, p := range probes {
			root := t.TempDir()
			dir := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(p.prefix, "/")))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(p.content), 0o644); err != nil {
				t.Fatal(err)
			}
			rules := Append(nil, dir, root)
			for j, path := range p.paths {
				rel, isDir := path[0].(string), path[1].(bool)
				got := Ignored(filepath.Join(root, filepath.FromSlash(rel)), isDir, root, rules)
				total++
				w, ok := want[i][j].(bool)
				if !ok {
					differences = append(differences, fmt.Sprintf("content %q path %q: Pi %v", p.content, rel, want[i][j]))
					continue
				}
				if w {
					ignoredCount++
				}
				if got != w {
					differences = append(differences, fmt.Sprintf("%s: prefix %q content %q path %q dir=%v: Go %v, Pi %v", corpus, p.prefix, p.content, rel, isDir, got, w))
				}
			}
		}
		if ignoredCount < total/20 || ignoredCount > total*19/20 {
			t.Fatalf("%s: Pi ignores %d of %d probes: the corpus is one-sided", corpus, ignoredCount, total)
		}
		if len(differences) > 0 {
			shown := differences
			if len(shown) > 12 {
				shown = shown[:12]
			}
			t.Errorf("%s: %d of %d probes differ from Pi:\n%s", corpus, len(differences), total, strings.Join(shown, "\n"))
		}
	}
}
