package packagecontent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// globOracleTree has names that differ from the patterns below only by case, dot entries, a directory that repeats its parent's name in another case, names that minimatch's fast tests and classes treat specially, and a link (name@target) that the walk reaches twice. No two siblings differ only by case, so the tree is the same on a file system that ignores case.
var globOracleTree = []string{
	"root.md", "ROOT2.MD", ".hidden/x.md", "a/one.md", "a/Two.MD", "a/deep/three.md", "a/more/MORE/pb.mD", "a/more/p.md",
	"more/x/PB.md", "more/x/pa.md", "more/Y/pc.md", "node_modules/dep/dep.md", "Sub/x/SUB/x.md", "Sub/x/X/pa.MD", "Sub/x/p.md",
	"prompts/a.md", "prompts/B.MD", "names/bk", "names/b\u212A", "names/x\u00E5", "names/x\u212B", "names/\u039F\u0394\u039F\u03A3", "names/s\u00DF",
	"names/]x", "names/-x", "names/a-", "names/d", "names/b[cd", "names/x[y", "names/\U0001F600", "names/\U0001F600\U0001F600", "names/ab.c", "names/.dotted",
	"names/\uFF41", "t/y", "x/x@t", "cyc/x/y", "cyc/x/a@cyc/x", "cyc/x/x@cyc/x",
}

// globOraclePatterns cover magic and literal segments in either case; minimatch's fast tests, classes and POSIX classes; an unterminated "["; "." and empty segments, which minimatch's preprocessing drops; a leading "./", a trailing "/", and ".." in its forms; literals after a globstar; and a path walked twice through a link.
var globOraclePatterns = []string{
	"**/*.md", "**/*.MD", "*.md", "*.MD", "more/*/p*.md", "MORE/*/P*.MD", "more/x/?B.md", "more/x/[!p]*", "more/x/[pP]?.md",
	"more/[x]/*.md", "more/[X]/*.md", "[m]ore/*/*", "**/more/**", "**/x/**", "**/sub/*", "**/SUB/*.md", "a/**/MORE/*",
	"a/*/more/p*", "./more/*/*.md", "more/**/", "**/", "*/x/", "a[/*", "**/[!.]*.md", "**/*[A-Z]*", "**/[a-z]*.md", "?ore/**/*.md",
	"**/?/*", "*/*/*/*", "more/x/pb.md", "MORE/X/*.md", "a/more/*/*", "./**", "a/**/..", "a/**/../*.md", "Sub/**/../*",
	"prompts/./*.md", "prompts//*.md", "./prompts/./*.md", "*/./*.md", "a//deep/*.md", "a/./deep/*.md", "././**", "a/../prompts/*",
	"../**", "**/../*", "**/../**", "a/**/../b/**/../*",
	"names/*k", "names/*K", "names/*\u00E5", "names/*\u03A3", "names/*\u00DF", "names/?k", "names/??", "names/*.*", "names/.*", "names/*",
	"names/[]a]*", "names/[a-]*", "names/[-a]*", "names/[a-c-e]*", "names/[!]]*", "names/b[c*", "names/x[*", "names/[\U0001F600]",
	"names/[\U0001F600][\U0001F600]", "names/[[:alpha:]]*", "names/[[:upper:]]*", "names/[^[:digit:]]*", "names/[[:punct:]]*",
	"**/x/**/y", "names/*ed", "names/???????", "cyc/**/../x*", "./cyc/**/../?", "../**/..",
	"*.{md,MD}", "names/{bk,d}", "{a,more}/**/*.md", "prompts/{a,B}.{md,MD}", "{a,{more,Sub}}/*", "{,a}/one.md", "a/{deep,more}/*",
	"{}x*", "names/a{b,c*", "**/*.{md,MD}", "more/{x,Y}/p?.md", "names/x{1..3}*",
	"names/@(bk|d)*", "*.@(md|MD)", "names/!(*.*)", "names/+(x)*", "a/*(more|deep)/*", "prompts/?(a).md", "names/!([a-c]*)",
	"more/@(x|Y)/p@(a|b).md", "names/*(\U0001F600)", "a/!(deep)/*", "**/!(*.md)", "names/@(.dotted|d)", "names/x?(å|Å)",
	"names/[[:alpha:]]*,x", "names/v-[[:digit:]]*", "names/@(*(|)|x)", "names/[\U0001F600-[:alpha:]]", "names/[a-\U0001F600[:digit:]]",
}

// globOracleCases are the trees and patterns of the Node oracle. The second is a "**/.." pattern that minimatch splits into two patterns sharing a rest at different indexes, which Node's Cache, keyed by that rest, then shares.
var globOracleCases = []struct{ tree, patterns []string }{
	{globOracleTree, globOraclePatterns},
	{[]string{"y/x/x", "y/t/x/a", "y/t/x/t", "y/t/a/y", "x"}, []string{"?/**/../[x]/x"}},
}

// expandPackageGlob lists what Node's globSync lists, as Pi's expandPackageGlob (package-manager.ts:294-303) resolves, filters and sorts it, when Node has fs.globSync. Node ignores case for a segment with magic on Windows and macOS only, so this checks the rule of the platform the test runs on. The walk is the qualified Node's (.node-version): Node 24.22 and 26.12 agree on every pattern here, while Node 22.19 and 24.19 differ on six ("**/x/**", "a/**/../*.md", "Sub/**/../*", "**/x/**/y", "./cyc/**/../?", "**/!(*.md)"), where their loop returns at an entry whose path was walked already and a globstar queues the segment after it below a symlink, and Node 22.19's older minimatch differs on extglobs. CI runs this test on the qualified Node only (automation/make/ci.mk, ci-node-runtime).
func TestExpandPackageGlobMatchesNodeGlobSync(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	for n, oracle := range globOracleCases {
		root, outside := t.TempDir(), t.TempDir()
		for _, file := range oracle.tree {
			if link, target, ok := strings.Cut(file, "@"); ok {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, link)), 0o755); err != nil {
					t.Fatal(err)
				}
				testenv.RequireDirectoryLink(t, filepath.Join(root, target), filepath.Join(root, link))
				continue
			}
			writeTestFile(t, filepath.Join(root, file), "x\n")
		}
		patterns := slices.Clone(oracle.patterns)
		if n == 0 {
			writeTestFile(t, filepath.Join(outside, "Out.md"), "x\n")
			// An absolute pattern starts at the file system root, or at a drive on Windows; one without a drive starts at the drive of root.
			patterns = append(patterns, filepath.ToSlash(outside)+"/*.md", filepath.ToSlash(outside)+"/*.MD",
				strings.TrimPrefix(filepath.ToSlash(outside), filepath.VolumeName(outside))+"/*.md")
		}
		input, err := json.Marshal(patterns)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(node, "--eval", `
const fs = require("node:fs");
const { relative, resolve, sep } = require("node:path");
const root = process.argv[1];
const expand = (pattern) => {
	try {
		return fs.globSync(pattern, { cwd: root })
			.map((match) => resolve(root, match))
			.filter((p) => relative(root, p).split(sep).every((segment) => segment === ".." || !segment.startsWith(".")))
			.sort((a, b) => (a < b ? -1 : a > b ? 1 : 0))
			.map((p) => relative(root, p).split(sep).join("/"));
	} catch (e) {
		return { error: e.message };
	}
};
process.stdout.write(typeof fs.globSync === "function" ? JSON.stringify(JSON.parse(fs.readFileSync(0, "utf8")).map(expand)) : "null");
`, root)
		cmd.Stdin = bytes.NewReader(input)
		cmd.Env = append(os.Environ(), "NODE_NO_WARNINGS=1")
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("node globSync: %v", err)
		}
		var results []json.RawMessage
		if err := json.Unmarshal(output, &results); err != nil {
			t.Fatalf("node globSync output %q: %v", output, err)
		}
		if results == nil {
			t.Skip("node has no fs.globSync")
		}
		if len(results) != len(patterns) {
			t.Fatalf("node globSync gave %d results for %d patterns", len(results), len(patterns))
		}
		for i, pattern := range patterns {
			var want []string
			var thrown struct{ Error *string }
			if err := json.Unmarshal(results[i], &want); err != nil {
				if err := json.Unmarshal(results[i], &thrown); err != nil || thrown.Error == nil {
					t.Fatalf("node globSync result %s: %v", results[i], err)
				}
			}
			matches, err := expandPackageGlob(root, pattern)
			if thrown.Error != nil || err != nil {
				if thrown.Error == nil || err == nil || err.Error() != *thrown.Error {
					t.Errorf("case %d: expandPackageGlob(%q) fails with %v, want Node's %v", n, pattern, err, thrown.Error)
				}
				continue
			}
			got := []string{}
			for _, match := range matches {
				rel, err := filepath.Rel(root, match)
				if err != nil {
					rel = match
				}
				if rel == "." {
					rel = ""
				}
				got = append(got, filepath.ToSlash(rel))
			}
			if !slices.Equal(got, want) {
				t.Errorf("case %d: expandPackageGlob(%q) = %q, want Node's %q", n, pattern, got, want)
			}
		}
	}
}

// fileSystemIgnoresCase reports whether dir's file system finds a file by a name that differs only by case.
func fileSystemIgnoresCase(t *testing.T, dir string) bool {
	t.Helper()
	writeTestFile(t, filepath.Join(dir, "case-probe"), "x\n")
	defer func() { _ = os.Remove(filepath.Join(dir, "case-probe")) }()
	_, err := os.Stat(filepath.Join(dir, "CASE-PROBE"))
	return err == nil
}

// A manifest glob reaches expandPackageGlob from discovery and validation (collectFilesFromManifestEntries, package-manager.ts:2380-2389): where globSync ignores case, "prompts/*.MD" lists "prompts/x.md"; where it does not, discovery lists nothing and validation reports the entry. A "." segment, which minimatch drops, changes neither.
func TestManifestGlobsFollowNodeCaseThroughDiscovery(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"case-package","pi":{"prompts":["prompts/./*.MD"]}}`)
	writeTestFile(t, filepath.Join(root, "prompts", "x.md"), "prompt\n")
	previous := globIgnoresCase
	t.Cleanup(func() { globIgnoresCase = previous })

	globIgnoresCase = true
	resources, err := Discover(root)
	if err != nil || !slices.Equal(resources.PromptFiles, []string{filepath.Join(root, "prompts", "x.md")}) {
		t.Fatalf("Discover ignoring case = %q, %v; want prompts/x.md", resources.PromptFiles, err)
	}
	if _, err := ValidatePackage(root); err != nil {
		t.Fatalf("ValidatePackage ignoring case: %v", err)
	}

	globIgnoresCase = false
	if resources, err := Discover(root); err != nil || len(resources.PromptFiles) != 0 {
		t.Fatalf("Discover with case = %q, %v; want none", resources.PromptFiles, err)
	}
	if _, err := ValidatePackage(root); err == nil || !strings.Contains(err.Error(), "matched no resources") {
		t.Fatalf("ValidatePackage with case: %v; want the entry reported", err)
	}
}

// The rules of Node's globSync where Node is not installed, for both settings of nocase (createMatcher, lib/internal/fs/glob.js): a segment with magic ignores case on Windows and macOS, and a literal segment is compared exactly with a listed name, or found with a stat, which the file system answers, when it is the only segment pending, and then keeps the pattern's spelling.
func TestExpandPackageGlobFollowsNodeCaseRules(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"more/x/PB.md", "more/x/pa.md", "more/Y/pc.md", "SKILL.MD", "x.md", "a/more/MORE/pb.mD"} {
		writeTestFile(t, filepath.Join(root, file), "x\n")
	}
	statIgnoresCase := fileSystemIgnoresCase(t, t.TempDir())
	ifStatIgnoresCase := func(matches ...string) []string {
		if statIgnoresCase {
			return matches
		}
		return nil
	}
	previous := globIgnoresCase
	t.Cleanup(func() { globIgnoresCase = previous })
	for _, ignoreCase := range []bool{true, false} {
		globIgnoresCase = ignoreCase
		for _, tc := range []struct {
			pattern          string
			withoutCase      []string
			withCase         []string
			sameWithEitherOf bool
		}{
			{pattern: "more/*/p*.md", withoutCase: []string{"more/Y/pc.md", "more/x/PB.md", "more/x/pa.md"}, withCase: []string{"more/Y/pc.md", "more/x/pa.md"}},
			{pattern: "*.md", withoutCase: []string{"SKILL.MD", "x.md"}, withCase: []string{"x.md"}},
			{pattern: "more/x/?B.md", withoutCase: []string{"more/x/PB.md"}, withCase: []string{"more/x/PB.md"}},
			{pattern: "more/x/[!p]*", withoutCase: nil, withCase: []string{"more/x/PB.md"}},
			// A literal after a globstar is compared exactly with the listed names: "a/more/MORE" is not "more", so "a/more/MORE/pb.mD" is not a match.
			{pattern: "**/more/*", withoutCase: []string{"a/more/MORE", "more/Y", "more/x"}, sameWithEitherOf: true},
			// A literal that is the only segment pending is found with a stat and keeps the pattern's spelling.
			{pattern: "more/x/pb.md", withoutCase: ifStatIgnoresCase("more/x/pb.md"), sameWithEitherOf: true},
			// A one-character class is a literal, so it is found with a stat too.
			{pattern: "more/[y]/*.md", withoutCase: ifStatIgnoresCase("more/y/pc.md"), sameWithEitherOf: true},
			// minimatch drops "." and empty segments in the middle before the walk.
			{pattern: "more/./x//?B.md", withoutCase: []string{"more/x/PB.md"}, sameWithEitherOf: true},
		} {
			want := tc.withCase
			if ignoreCase || tc.sameWithEitherOf {
				want = tc.withoutCase
			}
			matches, err := expandPackageGlob(root, tc.pattern)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, match := range matches {
				rel, err := filepath.Rel(root, match)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, filepath.ToSlash(rel))
			}
			if !slices.Equal(got, want) {
				t.Errorf("nocase %t: expandPackageGlob(%q) = %q, want %q", ignoreCase, tc.pattern, got, want)
			}
		}
	}
}

// minimatch's globParts for Node's globSync (optimizationLevel 2), as Node 24's createMatcher gives them: runs of slashes split segments, "." and empty segments in the middle go, "<p>/.." collapses, adjacent globstars merge, and "**/../<p>/<p>" splits into two patterns that the second phase may merge.
func TestGlobPatternPartsFollowMinimatch(t *testing.T) {
	for pattern, want := range map[string][][]string{
		"./more/*.md":        {{".", "more", "*.md"}},
		"a/./b/*.md":         {{"a", "b", "*.md"}},
		"a/../more/*.md":     {{"more", "*.md"}},
		"a//b/*.md":          {{"a", "b", "*.md"}},
		"**/../x":            {{"**", "..", "x"}},
		"a/**/../*.md":       {{"a", "**", "..", "*.md"}},
		"./**":               {{".", "**"}},
		"**/./*.md":          {{"**", "*.md"}},
		"a/b/":               {{"a", "b", ""}},
		"**/":                {{"**", ""}},
		"x/**/**/y":          {{"x", "**", "y"}},
		"**/**":              {{"**"}},
		"*/./*":              {{"*", "*"}},
		"../x/*.md":          {{"..", "x", "*.md"}},
		"a/../../x/*":        {{"..", "x", "*"}},
		"**/*/../*.md":       {{"**", "*.md"}},
		"a/**/b/../c/*.md":   {{"a", "**", "c", "*.md"}},
		"./a/./b/*":          {{".", "a", "b", "*"}},
		"X/**/../**/pB.mD":   {{".", "**", "pB.mD"}, {"X", "**", "pB.mD"}},
		"a/**/../b/**/../c":  {{"b", "**", "..", "c"}, {"a", "**", "b", "**", "..", "c"}},
		"**/../**/B":         {{"..", "**", "B"}, {"**", "B"}},
		"more/**/../x/*":     {{"x", "*"}, {"more", "**", "x", "*"}},
		`a\b\*.md`:           {{"a", "b", "*.md"}},
		"./":                 {{"."}},
		"./.":                {{"."}},
		"a/../**":            {{".", "**"}},
		"a/..":               {{""}},
		"a/./":               {{"a", ""}},
		"**/x/../y/z":        {{"**", "y", "z"}},
		"**/../*/*/../../..": {{"**", ".."}},
		"*.{md,txt}":         {{"*.md"}, {"*.txt"}},
		".{,a,}":             {{"."}, {".a"}},
		"":                   nil,
	} {
		if got, err := globPatternParts(pattern); err != nil || !slices.EqualFunc(got, want, slices.Equal[[]string]) {
			t.Errorf("globPatternParts(%q) = %q, %v; want %q", pattern, got, err, want)
		}
	}
	// On Windows a pattern that starts with "//" and a name is a UNC path, which keeps a second empty segment.
	want := [][]string{{"", "x", "y"}}
	if runtime.GOOS == "windows" {
		want = [][]string{{"", "", "x", "y"}}
	}
	if got, err := globPatternParts("//x/y"); err != nil || !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("globPatternParts(//x/y) = %q, %v; want %q", got, err, want)
	}
}

// minimatch keeps a segment without magic as a string (AST #parseGlob, parseClass): a one-character class is its character, and a "[" that starts no complete class is itself.
func TestGlobLiteralFollowsMinimatch(t *testing.T) {
	for _, tc := range []struct {
		segment, literal string
		ok               bool
	}{
		{"more", "more", true},
		{"[x]", "x", true},
		{"[m]ore", "more", true},
		{"[x-x]", "x", true},
		{"[]]", "]", true},
		{"a[", "a[", true},
		{"[!]", "[!]", true},
		{"[]", "[]", true},
		{"*.md", "", false},
		{"a?", "", false},
		{"[xy]", "", false},
		{"[!x]", "", false},
		{"[^x]", "", false},
		{"[a-c]", "", false},
		{"[x-]", "", false},
		{"[z-a]", "", false},
		{"[[:alpha:]]", "", false},
		{"[\U0001F600]", "", false},
		{"x[\n]", "", false},
	} {
		segment, err := newPackageGlobSegment(tc.segment, false)
		if err != nil || segment.isLiteral != tc.ok || segment.literal != tc.literal {
			t.Errorf("segment %q is literal %t %q; want %t %q", tc.segment, segment.isLiteral, segment.literal, tc.ok, tc.literal)
		}
	}
}

// minimatch's braceExpand, as Node 24's bundled minimatch gives it: brace-expansion's comma lists, numeric and letter sequences with steps and padding, nesting, and the sets it leaves alone.
func TestBraceExpandFollowsMinimatch(t *testing.T) {
	for pattern, want := range map[string][]string{
		"*.{md,txt}":         {"*.md", "*.txt"},
		"{a,b}/{c,d}":        {"a/c", "a/d", "b/c", "b/d"},
		"x{1..3}":            {"x1", "x2", "x3"},
		"x{01..10..3}":       {"x01", "x04", "x07", "x10"},
		"x{-2..2}":           {"x-2", "x-1", "x0", "x1", "x2"},
		"{a..e..2}":          {"a", "c", "e"},
		"{Z..b}":             {"Z", "[", "", "]", "^", "_", "`", "a", "b"},
		"{a,{b,c}}d":         {"ad", "bd", "cd"},
		"a{b}c":              {"a{b}c"},
		"a{b,c":              {"a{b,c"},
		"{}x":                {"{}x"},
		"x{}":                {"x{}"},
		"a${b,c}":            {"a${b,c}"},
		"{a,}b":              {"ab", "b"},
		"{,a}":               {"a"},
		"{a,b}{}":            {"a{}", "b{}"},
		"{1..0}":             {"1", "0"},
		"{a}{b,c}":           {"{a}b", "{a}c"},
		"a{,b}{,c}":          {"a", "ac", "ab", "abc"},
		"{x,y}/**/*.{md,MD}": {"x/**/*.md", "x/**/*.MD", "y/**/*.md", "y/**/*.MD"},
		"plain":              {"plain"},
		"{{a,b}}":            {"{a}", "{b}"},
		"{}a,b}":             {"{}a,b}"},
		"{},a}":              {"{},a}"},
		"a,b":                {"a,b"},
		"{a,b,,}":            {"a", "b"},
		"{-01..1}":           {"-01", "000", "001"},
		// brace-expansion computes a sequence in JavaScript numbers: a bound or step beyond 2^53 rounds, -0 prints as 0, and the step is at least 1.
		"x{1..10000000000000000000..10000000000000000000}": {"x1", "x10000000000000000000"},
		"x{0..99999999999999999999..99999999999999999999}": {"x0", "x100000000000000000000"},
		"x{-5..5..-9223372036854775808}":                   {"x-5"},
		"x{1..9223372036854775806..9223372036854775807}":   {"x1", "x9223372036854776000"},
		"x{-0..2}":                   {"x0", "x1", "x2"},
		"x{-0..-2}":                  {"x0", "x-1", "x-2"},
		"x{1..3..-0}":                {"x1", "x2", "x3"},
		"x{1e3..2}":                  {"x{1e3..2}"},
		"{src/{a,b}/*.md,docs/*.md}": {"src/a/*.md", "src/b/*.md", "docs/*.md"},
		"{a{b,c}{d,e}":               {"{abd", "{abe", "{acd", "{ace"},
		"{${a,b}}":                   {"{${a,b}}"},
		"{a},b,{,}}":                 {"a}", "b"},
		"{0..3..02}":                 {"0", "2"},
		"{1..10..02}":                {"01", "03", "05", "07", "09"},
		"x{a,b\n}":                   {"x{a,b\n}"},
		"x{a,b\u2028}":               {"x{a,b\u2028}"},
		"x{a,b\r}":                   {"x{a,b\r}"},
		"a{b}c,\r}":                  {"a{b}c,\r}"},
		"a{}{b,c}":                   {"a{}b", "a{}c"},
		"$x{a,b}":                    {"$xa", "$xb"},
		"{1..5..-2}":                 {"1", "3", "5"},
		"a${b}{c,d}":                 {"a${b}c", "a${b}d"},
		"{${a,b}}{c,d}":              {"{${a,b}}c", "{${a,b}}d"},
		"{1..010..3}":                {"001", "004", "007", "010"},
		"x{0001..0003}":              {"x0001", "x0002", "x0003"},
		// A step of 400 digits is Infinity, and a number from 1e21 up prints with an exponent.
		"x{1..100.." + strings.Repeat("9", 400) + "}":           {"x1"},
		"x{1..1000000000000000000000..1000000000000000000000}":  {"x1", "x1e+21"},
		"x{01..1000000000000000000000..1000000000000000000000}": {"x0000000000000000000001", "x000000000000000001e+21"},
		// A pattern cannot spell an escape marker, so its text stays as it is.
		"\x00OPEN\x00{a,b}": {"\x00OPEN\x00a", "\x00OPEN\x00b"},
	} {
		if got, err := braceExpand(pattern); err != nil || !slices.Equal(got, want) {
			t.Errorf("braceExpand(%q) = %q, %v; want %q", pattern, got, err, want)
		}
	}
	// A set gives at most EXPANSION_MAX expansions.
	if got, err := braceExpand("x{1..100001}"); err != nil || len(got) != braceExpansionMax || got[len(got)-1] != "x100000" {
		t.Errorf("braceExpand(x{1..100001}) gives %d expansions, %v; want %d, the last x100000", len(got), err, braceExpansionMax)
	}
}

// nodeUnicodeVersion skips t unless the installed Node's Unicode version is Go's, since both tables then agree.
func nodeUnicodeVersion(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	output, err := exec.Command(node, "--print", "process.versions.unicode").Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	nodeVersion, goVersion := strings.TrimSpace(string(output)), unicode.Version
	if goVersion != nodeVersion && !strings.HasPrefix(goVersion, nodeVersion+".") {
		t.Skipf("node has Unicode %s and Go has %s", nodeVersion, goVersion)
	}
	return node
}

// runNodeLines runs a script in Node and returns its output lines.
func runNodeLines(t *testing.T, node, script string) []string {
	t.Helper()
	output, err := exec.Command(node, "--eval", script).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	return strings.Split(strings.TrimRight(string(output), "\n"), "\n")
}

// A case-insensitive RegExp without the u flag compares Canonicalize: the one-unit uppercase, unless it maps non-ASCII to ASCII or the full uppercase has several characters. Where Node has Go's Unicode version, every Basic Multilingual Plane character is checked against V8.
func TestCanonicalizeRuneMatchesECMAScript(t *testing.T) {
	for r, want := range map[rune]rune{
		'a': 'A', 'Z': 'Z', '1': '1', '\u00E9': '\u00C9', '\u00FF': '\u0178', '\u00B5': '\u039C', '\u00DF': '\u00DF', '\u017F': '\u017F', '\u0131': '\u0131', '\u212A': '\u212A', '\u1FB3': '\u1FB3', '\u1F80': '\u1F80', '\u01C5': '\u01C4', 0xD83D: 0xD83D,
	} {
		if got := canonicalizeRune(r); got != want {
			t.Errorf("canonicalizeRune(%U) = %U, want %U", r, got, want)
		}
	}
	node := nodeUnicodeVersion(t)
	want := make(map[rune]rune)
	for _, line := range runNodeLines(t, node, `
const out = [];
for (let cp = 0; cp <= 0xffff; cp++) {
	const upper = String.fromCharCode(cp).toUpperCase();
	let canonical = cp;
	if (upper.length === 1) canonical = cp >= 128 && upper.charCodeAt(0) < 128 ? cp : upper.charCodeAt(0);
	if (canonical !== cp) out.push(cp + ":" + canonical);
}
process.stdout.write(out.join("\n"));
`) {
		var from, to rune
		if _, err := fmt.Sscanf(line, "%d:%d", &from, &to); err != nil {
			t.Fatalf("node line %q: %v", line, err)
		}
		want[from] = to
	}
	for r := rune(0); r <= 0xFFFF; r++ {
		expected, ok := want[r]
		if !ok {
			expected = r
		}
		if got := canonicalizeRune(r); got != expected {
			t.Errorf("canonicalizeRune(%U) = %U, want V8's %U", r, got, expected)
		}
	}
}

// jsToLower is toLowerCase: every Basic Multilingual Plane character and the Final_Sigma contexts are checked against V8 where Node has Go's Unicode version.
func TestJSToLowerMatchesECMAScript(t *testing.T) {
	if got := jsToLower("\u039F\u0394\u039F\u03A3"); got != "\u03BF\u03B4\u03BF\u03C2" {
		t.Errorf("jsToLower(\u039F\u0394\u039F\u03A3) = %q, want final sigma", got)
	}
	node := nodeUnicodeVersion(t)
	samples := []string{"\u03A3", "\u0391\u03A3", "\u0391\u03A3\u0391", "\u0391\u03A3.", "\u0391\u03A3'\u0392", "\u0391'\u03A3", "\u0391\u03A3\u0301", "\u03A3\u0301\u0391", "1\u03A3", "\u0391\u03A31", "\u0391\u03A3 \u0392", "\u0130", "A\u0130b", "\u212A", "\u1E9E", "\u01C5",
		"\u00AA\u03A3", "\u24D0\u03A3", "\u2170\u03A3", "\u0391\u03A3\u24D0", "\u02B0\u03A3", "\u0391\u02B0\u03A3"}
	for r := rune(0); r <= 0xFFFF; r++ {
		if r < 0xD800 || r > 0xDFFF {
			samples = append(samples, string(r))
		}
	}
	input, err := json.Marshal(samples)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--eval", `process.stdout.write(JSON.stringify(JSON.parse(require("fs").readFileSync(0, "utf8")).map((s) => s.toLowerCase())))`)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil || len(want) != len(samples) {
		t.Fatalf("node output: %v", err)
	}
	for i, s := range samples {
		if got := jsToLower(s); got != want[i] {
			t.Errorf("jsToLower(%q) = %q, want V8's %q", s, got, want[i])
		}
	}
}

// A segment matches a name as minimatch's parse makes it match (its RegExp, or the fast test that replaces it), checked against Node's path.matchesGlob, which builds the same minimatch with the same options for this platform, on seeded random segments and names.
func TestGlobSegmentsMatchMinimatch(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	pool := []string{"a", "A", "b", "B", "k", "K", "\u212A", "s", "S", "\u017F", "\u00DF", "\u1E9E", "\u00B5", "\u039C", "\u03BC", "\u03C3", "\u03C2", "\u03A3", "\u0131", "\u0130", "i", "I", "\u1FB3", "\u1FBC",
		"\u00E5", "\u00C5", "\u212B", "\u03C9", "\u03A9", "\u2126", "\u00FF", "\u0178", "\U0001F600", "-", "]", "_", ".", "1", "x", "\u00E9", "\u00C9", "\u13A0", "\uAB70"}
	pick := func(rng *rand.Rand) string { return pool[rng.IntN(len(pool))] }
	class := func(rng *rand.Rand) string {
		var b strings.Builder
		b.WriteString("[")
		if rng.IntN(4) == 0 {
			b.WriteString([]string{"!", "^"}[rng.IntN(2)])
		}
		for range 1 + rng.IntN(3) {
			switch rng.IntN(6) {
			case 0:
				b.WriteString([]string{"[:alpha:]", "[:upper:]", "[:lower:]", "[:digit:]", "[:punct:]", "[:ascii:]", "[:word:]"}[rng.IntN(7)])
			case 1:
				lo, hi := pick(rng), pick(rng)
				if lo > hi {
					lo, hi = hi, lo
				}
				b.WriteString(lo + "-" + hi)
			default:
				b.WriteString(pick(rng))
			}
		}
		b.WriteString("]")
		return b.String()
	}
	rng := rand.New(rand.NewPCG(7, 11))
	type pair struct{ Segment, Name string }
	var pairs []pair
	for range 4000 {
		var segment strings.Builder
		for range 1 + rng.IntN(3) {
			switch rng.IntN(7) {
			case 0:
				segment.WriteString("*")
			case 1:
				segment.WriteString("?")
			case 2, 3:
				segment.WriteString(class(rng))
			default:
				segment.WriteString(pick(rng))
			}
		}
		if segment.String() == "**" {
			continue
		}
		for range 3 {
			var name strings.Builder
			for range 1 + rng.IntN(3) {
				name.WriteString(pick(rng))
			}
			pairs = append(pairs, pair{segment.String(), name.String()})
		}
	}
	input, err := json.Marshal(pairs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--eval", `
const path = require("node:path");
const pairs = JSON.parse(require("fs").readFileSync(0, "utf8"));
const test = (p) => { try { return path.matchesGlob(p.Name, p.Segment); } catch (e) { return String(e.message); } };
process.stdout.write(typeof path.matchesGlob === "function" ? JSON.stringify(pairs.map(test)) : "null");
`)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), "NODE_NO_WARNINGS=1")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var want []any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("node output %q: %v", output, err)
	}
	if want == nil {
		t.Skip("node has no path.matchesGlob")
	}
	failures, thrown := 0, 0
	for i, p := range pairs {
		var got any
		if segment, err := newPackageGlobSegment(p.Segment, globIgnoresCase); err != nil {
			got = err.Error()
		} else {
			got = segment.matches(p.Name)
		}
		if _, ok := want[i].(string); ok {
			thrown++
		}
		if got != want[i] {
			if failures++; failures <= 20 {
				t.Errorf("segment %q matches %q = %v, want Node's %v", p.Segment, p.Name, got, want[i])
			}
		}
	}
	t.Logf("%d pairs, %d that Node rejects", len(pairs), thrown)
}

// Fixed cases of minimatch's segment rules: a fast test lowers an extension with toLowerCase, a RegExp compares Canonicalize, a class without the u flag reads UTF-16 code units, and a POSIX class turns the u flag on.
func TestGlobSegmentRules(t *testing.T) {
	for _, tc := range []struct {
		segment, name string
		nocase, want  bool
	}{
		{"*k", "b\u212A", true, true},
		{"*k*", "b\u212A", true, false},
		{"*\u03A3", "\u039F\u0394\u039F\u03A3", true, false},
		{"*.md", "X.MD", true, true},
		{"*.md", "X.MD", false, false},
		{"*", ".x", true, false},
		{"?", "\U0001F600", true, false},
		{"??", "\U0001F600", true, true},
		{"[\U0001F600]", "\U0001F600", false, false},
		{"[\U0001F600][\U0001F600]", "\U0001F600", false, true},
		{"[]a]*", "]x", false, true},
		{"[a-]*", "-x", false, true},
		{"[a-c-e]*", "-", false, true},
		{"[!]]*", "a", false, true},
		{"b[c*", "b[cd", false, true},
		{"[A-z]", "_", true, true},
		{"[!p]*", "PB", true, false},
		{"[.a]*", ".a", false, false},
		{".*", ".", false, false},
		{"[[:upper:]]", "a", true, true},
		{"[[:upper:]]", "a", false, false},
		{"[z-a]*", "z", false, false},
		// The fast tests keep minimatch's dot rules, though Pi drops dot names from its matches.
		{"*.*", ".x.y", false, false},
		{"*.md", ".x.md", true, false},
		{"??", ".x", false, false},
		{".?", "..", false, false},
		// A class that mixes ranges and a negated POSIX class opens with "(", so it has no dot guard.
		{"[a[:graph:]]*", ".x", false, true},
		{"[[:graph:]]", "a", false, true},
		{"[[:graph:]]", " ", false, false},
		// With the u flag a character outside the Basic Multilingual Plane is one class member.
		{"[[:alpha:]\U0001F600]", "\U0001F600", false, true},
		// A class of one line terminator stays a class, so it ignores case with the rest of the segment.
		{"x[\n]", "X\n", true, true},
	} {
		segment, err := newPackageGlobSegment(tc.segment, tc.nocase)
		if err != nil {
			t.Fatal(err)
		}
		if got := segment.matches(tc.name); got != tc.want {
			t.Errorf("nocase %t: segment %q matches %q = %t, want %t", tc.nocase, tc.segment, tc.name, got, tc.want)
		}
	}
}

// Pi sorts with JavaScript's <, which orders UTF-16 code units, so a character outside the Basic Multilingual Plane sorts before U+FFFD.
func TestCompareUTF16FollowsJavaScript(t *testing.T) {
	values := []string{"\uFFFD", "\U0001F600", "a", "ab", "\uE000", ""}
	slices.SortFunc(values, compareUTF16)
	if want := []string{"", "a", "ab", "\U0001F600", "\uE000", "\uFFFD"}; !slices.Equal(values, want) {
		t.Errorf("sorted = %q, want %q", values, want)
	}
}

// minimatch's AST, as Node 24's minimatch builds it for a segment of Node's globSync: extglobs with adoption, usurping and negation fill, empty and unclosed extglobs, its extglob depth limit, and the segments whose RegExp V8 rejects. Each row is the RegExp source and whether it needs the u flag, the literal of a segment without magic, or new RegExp's error message, all recorded from Node 24.19.0.
func TestGlobASTFollowsMinimatch(t *testing.T) {
	for _, tc := range []struct {
		segment, source string
		uflag           bool
		literal, err    string
	}{
		{"@(a|b)", "(?:a|b)", false, "", ""},
		{"+(a|b)*", "(?:a|b)+[^/]*?", false, "", ""},
		{"*(a|b).md", "(?:a|b)*\\.md", false, "", ""},
		{"?(a).md", "(?:a)?\\.md", false, "", ""},
		{"!(x)", "(?:(?!(?:x(?:$|\\/)))(?!\\.)[^/]*?)", false, "", ""},
		{"!(x)*.md", "(?:(?!(?:x[^/]*?\\.md(?:$|\\/)))(?!\\.)[^/]*?)[^/]*?\\.md", false, "", ""},
		{"!(*.md)", "(?:(?!(?:(?!\\.)[^/]*?\\.md(?:$|\\/)))(?!\\.)[^/]*?)", false, "", ""},
		{"x!(y)z", "x(?:(?!(?:yz(?:$|\\/)))[^/]*?)z", false, "", ""},
		{"!()", "(?!\\.)[^/]+?", false, "", ""},
		{"!(a|)", "(?!\\.)[^/]+?", false, "", ""},
		{"@()", "", false, "@()", ""},
		{"?()", "", false, "?()", ""},
		{"+()", "", false, "+()", ""},
		{"*()", "", false, "*()", ""},
		{"a+(b", "", false, "a+(b", ""},
		{"+(a|!(b))", "(?:(?:a|(?:(?!(?:b(?:$|\\/)))(?!\\.)[^/]*?))(?:a|(?:(?!(?:b(?:$|\\/)))[^/]*?))*?)", false, "", ""},
		{"!(a)!(b)", "(?:(?!(?:a(?:(?!(?:b(?:$|\\/)))[^/]*?)(?:$|\\/)))(?!\\.)[^/]*?)(?:(?!(?:b(?:$|\\/)))(?!\\.)[^/]*?)", false, "", ""},
		{"*(a|+(b|c))", "(?:a|b|c)*", false, "", ""},
		{"+(?(a))", "(?:a)+", false, "", ""},
		{"*(+(a))", "(?:a)*", false, "", ""},
		{"@(!(a))", "(?:(?!(?:a(?:$|\\/)))(?!\\.)[^/]*?)", false, "", ""},
		{"!(@(a|b))", "(?!\\.)[^/]+?", false, "", ""},
		{"!(!(a))", "(?:a)", false, "", ""},
		{".!(x)", "\\.(?:(?!(?:x(?:$|\\/)))[^/]*?)", false, "", ""},
		{"!(.x)", "(?:(?!(?:\\.x(?:$|\\/)))(?!\\.)[^/]*?)", false, "", ""},
		{"a@(b)c", "a(?:b)c", false, "", ""},
		{"@(@(@(@(a))))", "(?:a)", false, "", ""},
		{"+(+(+(+(a))))", "(?:a)+", false, "", ""},
		{"!(!(!(!(a))))", "(?!\\.)[^/]+?\\)", false, "", ""},
		{"@(a|@(b|@(c|@(d|@(e)))))", "(?:a|b|c|d|e)", false, "", ""},
		{"*(a|*(b|*(c|*(d))))", "(?:a|b|c|d)*", false, "", ""},
		{"?(a|?(b|?(c|?(d))))", "(?:a|b|c|d)?", false, "", ""},
		{"!(a)@(|)", "(?:(?!(?:a(?:|)(?:$|\\/)))(?!\\.)[^/]*?)@(|)", false, "", ""},
		{"!(a)*(|)", "(?:(?!(?:a(?:|)*(?:$|\\/)))(?!\\.)[^/]*?)*(|)", false, "", ""},
		{"!(a)+()", "(?:(?!(?:a(?:)+(?:$|\\/)))(?!\\.)[^/]*?)+()", false, "", ""},
		{"@(*(|)|x)", "", false, "", "Invalid regular expression: /^(?:*(|)|x)$/: Nothing to repeat"},
		{"@(x|?(|))", "(?:x)", false, "", ""},
		{"@(+(|)|x)", "", false, "", "Invalid regular expression: /^(?:+(|)|x)$/: Nothing to repeat"},
		{"+(x|*(|))", "(?:x)+", false, "", ""},
		{"!(!(a)*(|))", "(?!\\.)[^/]+?", false, "", ""},
		{"?(+(|))", "", false, "*(|)", ""},
		{"@([a-c]|x)", "(?:(?!\\.)[a-c]|x)", false, "", ""},
		{"+([[:alpha:]])", "(?:(?:(?!\\.)[\\p{L}\\p{Nl}])(?:(?!(?:^|/)\\.\\.?(?:$|/))[\\p{L}\\p{Nl}])*?)", true, "", ""},
		{"@([[:digit:]]|-)", "", false, "", "Invalid regular expression: /^(?:(?!\\.)[\\p{Nd}]|\\-)$/u: Invalid escape"},
		{"!([[:upper:]])*", "(?:(?!(?:(?!\\.)[\\p{Lu}][^/]+?(?:$|\\/)))(?!\\.)[^/]*?)[^/]*?", true, "", ""},
		{"@(a|[!b])", "(?:a|(?!\\.)[^b])", false, "", ""},
		{"*([x])", "(?:x)*", false, "", ""},
		{"@([)", "", false, "@([)", ""},
		{"@(a[)", "", false, "@(a[)", ""},
		{"@(a|[b)c)", "", false, "@(a|[b)c)", ""},
		{"@(a\U0001F600|b)", "(?:a\U0001F600|b)", false, "", ""},
		{"@(.a|b)", "(?:\\.a|b)", false, "", ""},
		{"@(..|x)", "(?:\\.\\.|x)", false, "", ""},
		{"+(.x|y)", "(?:\\.x|y)+", false, "", ""},
		{"*(.x)", "(?:\\.x)*", false, "", ""},
		{"!(.)", "(?:(?!(?:\\.(?:$|\\/)))(?!\\.)[^/]*?)", false, "", ""},
		{"@(a|b)@(c|d)", "(?:a|b)(?:c|d)", false, "", ""},
		{"a@(b|c)*d", "a(?:b|c)[^/]*?d", false, "", ""},
		{"*@(a)", "(?!\\.)[^/]*?(?:a)", false, "", ""},
		{"?@(a)", "(?!\\.)[^/](?:a)", false, "", ""},
		{"@(a)?", "(?:a)[^/]", false, "", ""},
		{"@(a,b)", "(?:a\\,b)", false, "", ""},
		{"+(a#b)", "(?:a\\#b)+", false, "", ""},
		{"@(a b)", "(?:a\\ b)", false, "", ""},
		{"x[[:alpha:]]@(a)", "x[\\p{L}\\p{Nl}](?:a)", true, "", ""},
		{"@(a|-)[[:digit:]]", "", false, "", "Invalid regular expression: /^(?:a|\\-)[\\p{Nd}]$/u: Invalid escape"},
		{"@(a)[[:digit:]]-", "", false, "", "Invalid regular expression: /^(?:a)[\\p{Nd}]\\-$/u: Invalid escape"},
		{"[[:alpha:]]*,md", "", false, "", "Invalid regular expression: /^(?!\\.)[\\p{L}\\p{Nl}][^/]*?\\,md$/u: Invalid escape"},
		{"v-[[:digit:]]*", "", false, "", "Invalid regular expression: /^v\\-[\\p{Nd}][^/]*?$/u: Invalid escape"},
		// A usurp changes the extglob's type, and the next flatten round adopts or usurps under the new type.
		{"!(!(+(a)))", "(?:a)+", false, "", ""},
		{"!(!(*(a|b)))", "(?:a|b)*", false, "", ""},
		{"!(!(?(a)))", "(?:a)", false, "", ""},
		{"?(+(a|*(b)))", "(?:a|b)*", false, "", ""},
		{"[a-\U0001F600[:digit:]]", "(?!\\.)[a-\U0001F600\\p{Nd}]", true, "", ""},
		{"[\U0001F600-a[:digit:]]", "(?!\\.)[\xed\xa0\xbd\\p{Nd}]", true, "", ""},
		{"[\U0001F600-\uE000[:digit:]]", "", false, "", "Invalid regular expression: /^(?!\\.)[\U0001F600-\uE000\\p{Nd}]$/u: Range out of order in character class"},
		{"[\U0001F600-\uE000]", "(?!\\.)[\U0001F600-\uE000]", false, "", ""},
		{"[a-\U0001F600-z[:alpha:]]", "(?!\\.)[a-\xed\xa0\xbd\\p{L}\\p{Nl}]", true, "", ""},
		{"[\U0001F600\U0001F603-\U0001F604[:alpha:]]", "(?!\\.)[\U0001F600\U0001F604\\p{L}\\p{Nl}]", true, "", ""},
		{"@(\U0001F600|[\U0001F600-\U0001F601])", "(?:\U0001F600|(?!\\.)[\U0001F601])", false, "", ""},
		{"[!a-\U0001F600[:digit:]]", "(?!\\.)[^a-\U0001F600\\p{Nd}]", true, "", ""},
		{"+(a)+(b)", "(?:a)+(?:b)+", false, "", ""},
		{"*(a)*(b)", "(?:a)*(?:b)*", false, "", ""},
		{"?(a)?(b)", "(?:a)?(?:b)?", false, "", ""},
		{"!(a)x!(b)", "(?:(?!(?:ax(?:(?!(?:b(?:$|\\/)))[^/]*?)(?:$|\\/)))(?!\\.)[^/]*?)x(?:(?!(?:b(?:$|\\/)))[^/]*?)", false, "", ""},
		{"@(a|!(b|c))d", "(?:a|(?:(?!(?:bd(?:$|\\/)|cd(?:$|\\/)))(?!\\.)[^/]*?))d", false, "", ""},
		{"!(a|b|c)", "(?:(?!(?:a(?:$|\\/)|b(?:$|\\/)|c(?:$|\\/)))(?!\\.)[^/]*?)", false, "", ""},
		{"!(*)", "(?:(?!(?:(?!\\.)[^/]+?(?:$|\\/)))(?!\\.)[^/]*?)", false, "", ""},
		{"!(**)", "(?:(?!(?:(?!\\.)[^/]+?(?:$|\\/)))(?!\\.)[^/]*?)", false, "", ""},
		{"@(*)", "(?:(?!\\.)[^/]+?)", false, "", ""},
		{"+(*)", "(?:(?:(?!\\.)[^/]+?)(?:(?!(?:^|/)\\.\\.?(?:$|/))[^/]+?)*?)", false, "", ""},
	} {
		source := globASTFromGlob(jsstring.ToUTF16(tc.segment)).toRegExpSource(nil)
		literal, _, err := compileGlobPart(tc.segment, false)
		switch {
		case tc.err != "":
			if err == nil || err.Error() != tc.err {
				t.Errorf("segment %q fails with %v, want %q", tc.segment, err, tc.err)
			}
		case tc.literal != "" || !source.hasMagic:
			if source.hasMagic || literal != tc.literal || err != nil {
				t.Errorf("segment %q is %q (magic %t, %v), want the literal %q", tc.segment, literal, source.hasMagic, err, tc.literal)
			}
		default:
			if !slices.Equal(source.re, jsstring.ToUTF16(tc.source)) || source.uflag != tc.uflag || err != nil {
				t.Errorf("segment %q has source %q u=%t (%v), want %q u=%t", tc.segment, jsstring.FromUTF16(source.re), source.uflag, err, tc.source, tc.uflag)
			}
		}
	}
}

// Random segments with extglobs, nested to minimatch's depth limit and past it, some unclosed, mixed with stars, classes and POSIX classes, against Node's path.matchesGlob, which parses a segment as globSync does: a match, or the message of the error that new RegExp throws.
func TestGlobExtglobSegmentsMatchMinimatch(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	pool := []string{"a", "A", "b", "x", "X", ".", "-", ",", " ", "k", "\u212A", "\u00DF", "\U0001F600", "1", "|", ")"}
	rng := rand.New(rand.NewPCG(19, 23))
	pick := func() string { return pool[rng.IntN(len(pool))] }
	var piece func(depth int) string
	piece = func(depth int) string {
		switch n := rng.IntN(10); {
		case n < 3 && depth < 4:
			var b strings.Builder
			b.WriteString(string("!?+*@"[rng.IntN(5)]) + "(")
			for i := range rng.IntN(3) + 1 {
				if i > 0 {
					b.WriteString("|")
				}
				for range rng.IntN(3) {
					b.WriteString(piece(depth + 1))
				}
			}
			if rng.IntN(8) != 0 {
				b.WriteString(")")
			}
			return b.String()
		case n == 3:
			return "*"
		case n == 4:
			return "?"
		case n == 5:
			return []string{"[ab]", "[!a]", "[[:alpha:]]", "[a-z]", "[[:digit:]x]", "[a-\U0001F600]", "[.x]"}[rng.IntN(7)]
		}
		return pick()
	}
	type pair struct{ Segment, Name string }
	var pairs []pair
	for range 3000 {
		var segment strings.Builder
		for range 1 + rng.IntN(3) {
			segment.WriteString(piece(0))
		}
		if s := segment.String(); s != "**" && !strings.Contains(s, "/") {
			for range 4 {
				var name strings.Builder
				for range rng.IntN(4) {
					name.WriteString(pool[rng.IntN(len(pool)-2)])
				}
				pairs = append(pairs, pair{s, name.String()})
			}
		}
	}
	input, err := json.Marshal(pairs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--eval", `
const path = require("node:path");
const pairs = JSON.parse(require("fs").readFileSync(0, "utf8"));
const test = (p) => { try { return path.matchesGlob(p.Name, p.Segment); } catch (e) { return String(e.message); } };
process.stdout.write(typeof path.matchesGlob === "function" ? JSON.stringify(pairs.map(test)) : "null");
`)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), "NODE_NO_WARNINGS=1")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var want []any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("node output %q: %v", output, err)
	}
	if want == nil {
		t.Skip("node has no path.matchesGlob")
	}
	failures, matched, thrown := 0, 0, 0
	for i, p := range pairs {
		var got any
		if segment, err := newPackageGlobSegment(p.Segment, globIgnoresCase); err != nil {
			got = err.Error()
		} else {
			got = segment.matches(p.Name)
		}
		switch want[i] {
		case true:
			matched++
		case false:
		default:
			thrown++
		}
		if got != want[i] {
			if failures++; failures <= 20 {
				t.Errorf("segment %q matches %q = %v, want Node's %v", p.Segment, p.Name, got, want[i])
			}
		}
	}
	t.Logf("%d pairs: %d match, %d that Node rejects", len(pairs), matched, thrown)
}

// With the u flag a class range that ends in a character outside the Basic Multilingual Plane covers every code point up to it, as V8 reads the source's surrogate pair as one code point; without the u flag the range ends at the pair's first unit and the second unit is its own member.
func TestGlobClassRangeEndsAtACodePoint(t *testing.T) {
	for _, tc := range []struct {
		segment, name string
		want          bool
	}{
		{"[a-\U0001F600[:digit:]]", "\uFF41", true},
		{"[a-\U0001F600[:digit:]]", "\U00010000", true},
		{"[a-\U0001F600[:digit:]]", "\U0001F601", false},
		{"[!a-\U0001F600[:digit:]]", "\uFF41", false},
		{"[a-\U0001F600]", "\uFF41", false},
		{"[a-\U0001F600][\U0001F600]", "\uFF41\U0001F600", false},
	} {
		segment, err := newPackageGlobSegment(tc.segment, false)
		if err != nil {
			t.Fatal(err)
		}
		if got := segment.matches(tc.name); got != tc.want {
			t.Errorf("segment %q matches %q = %t, want %t", tc.segment, tc.name, got, tc.want)
		}
	}
}

// brace-expansion pushes every item of a sequence, and V8 throws a RangeError for a push past 2^27 elements: in Node 24.19 with a 3 GB heap, an array's push throws "Invalid array length" at length 134,217,728, and braceExpand throws it for a sequence whose step stops advancing. A long sequence of distinct items may exhaust Node's default heap before that; PiG reports the RangeError without building the items. minimatch's assertValidPattern throws for a pattern over 65,536 UTF-16 units.
func TestGlobPatternErrorsFollowNode(t *testing.T) {
	for _, pattern := range []string{"x{9007199254740993..9007199254740995}", "x{1..134217729}", "{a,x{-134217728..0}}"} {
		if _, err := braceExpand(pattern); err == nil || err.Error() != "Invalid array length" {
			t.Errorf("braceExpand(%q) fails with %v, want Invalid array length", pattern, err)
		}
	}
	if got, err := braceExpand("x{1..134217728}"); err != nil || len(got) != braceExpansionMax {
		t.Errorf("braceExpand(x{1..134217728}) gives %d expansions, %v; want %d", len(got), err, braceExpansionMax)
	}
	for pattern, want := range map[string]error{
		strings.Repeat("a", 65537):          errGlobPatternTooLong,
		strings.Repeat("\U0001F600", 32769): errGlobPatternTooLong,
		strings.Repeat("a", 65536):          nil,
		strings.Repeat("\U0001F600", 32768): nil,
	} {
		if _, err := globPatternParts(pattern); !errors.Is(err, want) || err != nil && want == nil {
			t.Errorf("globPatternParts(%.40q) fails with %v, want %v", pattern, err, want)
		}
	}
}

// On Windows, where Node's globSync ignores case, minimatch's windowsNoMagicRoot keeps the first four segments of a UNC path ("//?/" with a device name too) and the drive of a drive path as strings, so a "?" there is not magic. Recorded from Node 24's createMatcher on Windows.
func TestGlobPatternSegmentsKeepWindowsRoots(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windowsNoMagicRoot applies on Windows")
	}
	for pattern, want := range map[string][]string{
		"//?/C:/x/*.md":   {"S:", "S:", "S:?", "S:C:", "S:x", "RE"},
		"//srv/sh?re/x/*": {"S:", "S:", "S:srv", "RE", "S:x", "RE"},
		"//srv/share/*":   {"S:", "S:", "S:srv", "S:share", "RE"},
		"C:/x*/y":         {"S:C:", "RE", "S:y"},
		"//?/x":           {"S:", "S:", "S:?", "S:x"},
	} {
		parts, err := globPatternParts(pattern)
		if err != nil || len(parts) != 1 {
			t.Fatalf("globPatternParts(%q) = %q, %v", pattern, parts, err)
		}
		segments, err := globPatternSegments(parts[0], true)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, segment := range segments {
			if segment.isLiteral {
				got = append(got, "S:"+segment.literal)
			} else {
				got = append(got, "RE")
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("segments of %q = %q, want %q", pattern, got, want)
		}
	}
}

// A manifest glob that Node's globSync throws for ends a local Package's collection, as resolveLocalExtensionSource catches the throw (package-manager.ts:1372-1389): the inventory keeps the kinds collected before it, and the kind that throws and every later kind declare nothing, not even a missing member.
func TestInspectConfiguredStopsAtAThrowingGlob(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"p","pi":{"skills":["skills"],"prompts":["prompts/good.md","prompts/[[:alpha:]]*,md"],"themes":["themes/missing.json"]}}`)
	writeTestFile(t, filepath.Join(root, "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\nBody\n")
	writeTestFile(t, filepath.Join(root, "prompts", "good.md"), "Good\n")
	resources, missing, err := InspectConfigured(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources.SkillDirs) != 1 || len(resources.PromptFiles) != 0 || len(resources.ThemeFiles) != 0 || len(missing) != 0 {
		t.Fatalf("skills %q, prompts %q, themes %q, missing %+v; want the skill only", resources.SkillDirs, resources.PromptFiles, resources.ThemeFiles, missing)
	}
}

// Pi collects extensions before skills and reads globs only from a "pi" block (collectPackageResources, package-manager.ts:2214-2246): a throw in extensions leaves no skills, a throw in PiG's own "pig" block or in plugin metadata stops nothing, and a kind that throws declares no missing member, not even one listed before the throw.
func TestDiscoveryThrowsFollowPiKindsAndOrder(t *testing.T) {
	write := func(t *testing.T, manifest string) string {
		t.Helper()
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "package.json"), manifest)
		writeTestFile(t, filepath.Join(root, "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\nBody\n")
		writeTestFile(t, filepath.Join(root, "prompts", "good.md"), "Good\n")
		writeTestFile(t, filepath.Join(root, "hooks", "a,json"), "{}\n")
		writeTestFile(t, filepath.Join(root, "extensions", "a,ts"), "x\n")
		return root
	}
	t.Run("extensions throw before skills", func(t *testing.T) {
		resources, err := Discover(write(t, `{"name":"p","pi":{"extensions":["extensions/[[:alpha:]]*,ts"],"skills":["skills"]}}`))
		if thrown, ok := errors.AsType[*PackagePatternError](err); !ok || thrown.Kind != Extensions || len(resources.SkillDirs) != 0 {
			t.Fatalf("Discover = %+v, %v; want the extensions throw and no skills", resources, err)
		}
	})
	t.Run("a pig block throw stops nothing", func(t *testing.T) {
		resources, err := Discover(write(t, `{"name":"p","pi":{"skills":["skills"],"prompts":["prompts/*.md"]},"pig":{"hooks":["hooks/[[:alpha:]]*,json"]}}`))
		if err != nil || len(resources.SkillDirs) != 1 || len(resources.PromptFiles) != 1 || len(resources.HookFiles) != 0 {
			t.Fatalf("Discover = %+v, %v; want the skill and the prompt, and no hooks", resources, err)
		}
	})
	t.Run("a plugin metadata throw stops nothing", func(t *testing.T) {
		root := write(t, `{"name":"p"}`)
		writeTestFile(t, filepath.Join(root, "plugin.json"), `{"name":"p","extensions":["extensions/[[:alpha:]]*,ts"],"skills":["skills"]}`)
		resources, err := Discover(root)
		if err != nil || len(resources.ExtensionEntries) != 0 || len(resources.SkillDirs) != 1 {
			t.Fatalf("Discover = %+v, %v; want the skill and no extensions", resources, err)
		}
	})
	t.Run("missing members of a kind that throws", func(t *testing.T) {
		_, missing, err := InspectConfigured(write(t, `{"name":"p","pi":{"prompts":["prompts/missing.md","prompts/[[:alpha:]]*,md"]}}`), nil)
		if err != nil || len(missing) != 0 {
			t.Fatalf("missing = %+v, %v; want none", missing, err)
		}
	})
	t.Run("a plugin metadata throw leaves out only its entry", func(t *testing.T) {
		root := write(t, `{"name":"p"}`)
		writeTestFile(t, filepath.Join(root, "plugin.json"), `{"name":"p","prompts":["prompts/missing.md","prompts/[[:alpha:]]*,md","prompts/gone.md"]}`)
		_, missing, err := InspectConfigured(root, nil)
		var got []string
		for _, member := range missing {
			got = append(got, member.Pattern)
		}
		if err != nil || !slices.Equal(got, []string{"prompts/missing.md", "prompts/gone.md"}) {
			t.Fatalf("missing = %q, %v; want the two missing prompts", got, err)
		}
	})
	t.Run("strict validation names the entry", func(t *testing.T) {
		_, err := Validate(write(t, `{"name":"p","pi":{"prompts":["prompts/[[:alpha:]]*,md"]}}`))
		if err == nil || !strings.HasPrefix(err.Error(), `prompts manifest entry "prompts/[[:alpha:]]*,md": Invalid regular expression`) {
			t.Fatalf("Validate = %v, want the entry's SyntaxError", err)
		}
	})
}

// Segments whose RegExp source or error depends on details Node 24 fixes: \p{C} includes unassigned code points, \p{Z} includes the line and paragraph separators, U+FEFF is whitespace that minimatch escapes (and the u flag rejects the escape), and a segment starting "..*" gets the traversal guard, which shows in the error text.
func TestGlobSegmentEdgeRowsMatchMinimatch(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	type pair struct{ Segment, Name string }
	pairs := []pair{
		{"[[:print:]]", "\u0378"}, {"[[:graph:]]", "\u0378"}, {"[[:space:]]", "\u2028"}, {"[[:space:]]", "\u2029"}, {"[[:blank:]]", "\u2028"},
		{"[[:alpha:]]\uFEFF*", "a"}, {"a\uFEFF*", "a\uFEFFb"}, {"..*[[:alpha:]],", "..a,"}, {".*[[:alpha:]],", ".a,"},
	}
	input, err := json.Marshal(pairs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--eval", `
const path = require("node:path");
const pairs = JSON.parse(require("fs").readFileSync(0, "utf8"));
const test = (p) => { try { return path.matchesGlob(p.Name, p.Segment); } catch (e) { return String(e.message); } };
process.stdout.write(typeof path.matchesGlob === "function" ? JSON.stringify(pairs.map(test)) : "null");
`)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), "NODE_NO_WARNINGS=1")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var want []any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("node output %q: %v", output, err)
	}
	if want == nil {
		t.Skip("node has no path.matchesGlob")
	}
	for i, p := range pairs {
		var got any
		if segment, err := newPackageGlobSegment(p.Segment, globIgnoresCase); err != nil {
			got = err.Error()
		} else {
			got = segment.matches(p.Name)
		}
		if got != want[i] {
			t.Errorf("segment %q matches %q = %v, want Node's %v", p.Segment, p.Name, got, want[i])
		}
	}
}

// globSync builds its matcher before it reads a directory, so whether a Package's manifest glob throws depends on package.json alone. Only globs of Pi kinds in a "pi" block count: Pi reads nothing else, and an override or an entry without "*" or "?" is not a glob.
func TestManifestGlobsThrowReadsOnlyThePiBlock(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
		want           bool
	}{
		{"a prompts glob that throws", `{"name":"p","pi":{"prompts":["prompts/[[:alpha:]]*,md"]}}`, true},
		{"an extensions glob that throws", `{"name":"p","pi":{"extensions":["extensions/*","x/[[:alpha:]]?,ts"]}}`, true},
		{"a skills glob that throws", `{"name":"p","pi":{"skills":["skills/[[:alpha:]]*,x"]}}`, true},
		{"a themes glob that throws", `{"name":"p","pi":{"themes":["themes/[[:alpha:]]*,json"]}}`, true},
		{"globs that compile", `{"name":"p","pi":{"prompts":["prompts/*.md","+(a|b)/*"],"skills":["skills/**"]}}`, false},
		{"a throwing glob in the pig block", `{"name":"p","pi":{"prompts":["prompts/*.md"]},"pig":{"hooks":["hooks/[[:alpha:]]*,json"]}}`, false},
		{"an override", `{"name":"p","pi":{"prompts":["prompts/*.md","![[:alpha:]]*,md"]}}`, false},
		{"an entry without * or ?", `{"name":"p","pi":{"prompts":["prompts/[[:alpha:]],md"]}}`, false},
		{"no pi block", `{"name":"p"}`, false},
	} {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "package.json"), tc.manifest)
		if got := ManifestGlobsThrow(root); got != tc.want {
			t.Errorf("%s: ManifestGlobsThrow = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// package-manager.ts matchesAnyPattern and node-glob decide a resource filter with minimatch (Pi 1.1.0 package-manager.ts:674-684). The expected values come from running minimatch 10.2.6 on the same pairs.
func TestMatchGlobUsesMinimatchSyntax(t *testing.T) {
	for _, tc := range []struct {
		name, pattern string
		want          bool
	}{
		{"extensions/a.ts", "extensions/{a,b}.ts", true},
		{"extensions/c.ts", "extensions/{a,b}.ts", false},
		{"skills/x/SKILL.md", "skills/+(x|y)/**", true},
		{"skills/z/SKILL.md", "!skills/z/**", false},
		{"a/b.ts", "a/@(b|c).ts", true},
		{"a/d.ts", "a/!(b|c).ts", true},
		{"a/.hid.ts", "a/*.ts", false},
		{"a/b/c/d.ts", "**/d.ts", true},
		{"a/b.ts", "a/[[:alpha:]].ts", true},
	} {
		m, err := newMinimatchFilter(tc.pattern)
		if err != nil {
			t.Fatalf("newMinimatchFilter(%q): %v", tc.pattern, err)
		}
		if got := m.match(tc.name); got != tc.want {
			t.Errorf("minimatch(%q, %q) = %v, minimatch %v", tc.name, tc.pattern, got, tc.want)
		}
	}
}

func TestMatchesAnyPatternAcceptsBraceAndExtglobFilters(t *testing.T) {
	base := "/pkg"
	for _, tc := range []struct {
		file     string
		patterns []string
		want     bool
	}{
		{"/pkg/extensions/a.ts", []string{"extensions/{a,b}.ts"}, true},
		{"/pkg/extensions/c.ts", []string{"extensions/{a,b}.ts"}, false},
		{"/pkg/extensions/c.ts", []string{"!(a|b).ts"}, true},
	} {
		got, err := matchesAnyPattern(filepath.FromSlash(tc.file), newFilterPatterns(tc.patterns), filepath.FromSlash(base), Extensions)
		if err != nil {
			t.Fatalf("matchesAnyPattern(%q, %v): %v", tc.file, tc.patterns, err)
		}
		if got != tc.want {
			t.Errorf("matchesAnyPattern(%q, %v) = %v, want %v", tc.file, tc.patterns, got, tc.want)
		}
	}
}
