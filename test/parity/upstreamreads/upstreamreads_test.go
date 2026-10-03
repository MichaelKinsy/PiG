// Package upstreamreads guards the pinned-upstream mirror contract: code reads
// Pi's source through .upstream/current, never through a version directory.
//
// A public checkout materializes only the pinned release (make
// upstream-mirror), so a read of .upstream/v<other version> passes where an
// older release happens to be on disk and fails on CI. Comments, docstrings and
// citations may name any version; only code may not.
package upstreamreads

import (
	"bytes"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// versionedUpstream matches a version directory of the mirror inside code: a
// single path literal (.upstream/v0.99.1/...) or the same path split across
// join arguments (".upstream", "v0.99.1").
var versionedUpstream = regexp.MustCompile("\\.upstream[\\\\/]+v\\d+\\.\\d+\\.\\d+|\\.upstream[\"'`]\\s*,\\s*[\"'`]v\\d+\\.\\d+\\.\\d+")

// syntax describes the comment forms of one language family.
type syntax struct {
	line        []string // line-comment openers
	blockOpen   string
	blockClose  string
	tripleDoc   bool // a statement-level triple-quoted string is prose, not code
	rawBacktick bool // a backtick string has no escapes (Go)
	hashNeedsWS bool // # opens a comment only at line start or after whitespace (shell, Make)
}

var syntaxes = map[string]syntax{
	"c":      {line: []string{"//"}, blockOpen: "/*", blockClose: "*/"},
	"hash":   {line: []string{"#"}, hashNeedsWS: true},
	"python": {line: []string{"#"}, tripleDoc: true},
}

func syntaxFor(name string) (syntax, bool) {
	base := path.Base(name)
	switch base {
	case "Makefile", "Dockerfile":
		return syntaxes["hash"], true
	}
	switch strings.ToLower(path.Ext(base)) {
	case ".go":
		goSyntax := syntaxes["c"]
		goSyntax.rawBacktick = true
		return goSyntax, true
	case ".mjs", ".cjs", ".js", ".jsx", ".ts", ".mts", ".cts", ".tsx", ".rs", ".c":
		return syntaxes["c"], true
	case ".py":
		return syntaxes["python"], true
	case ".sh", ".toml", ".yml", ".yaml", ".mk":
		return syntaxes["hash"], true
	}
	return syntax{}, false
}

// code returns src with comments (and, for Python, statement-level docstrings)
// replaced by spaces, keeping string literals. Newlines survive so a finding
// keeps its line number.
func (s syntax) code(src string) string {
	out := []byte(src)
	blank := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	lineStart := 0
	for i := 0; i < len(src); {
		c := src[i]
		if c == '\n' {
			lineStart = i + 1
			i++
			continue
		}
		if s.blockOpen != "" && strings.HasPrefix(src[i:], s.blockOpen) {
			end := strings.Index(src[i+len(s.blockOpen):], s.blockClose)
			stop := len(src)
			if end >= 0 {
				stop = i + len(s.blockOpen) + end + len(s.blockClose)
			}
			blank(i, stop)
			i = stop
			continue
		}
		if s.opensLineComment(src, i) {
			stop := strings.IndexByte(src[i:], '\n')
			if stop < 0 {
				stop = len(src)
			} else {
				stop += i
			}
			blank(i, stop)
			i = stop
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			triple := strings.Repeat(string(c), 3)
			if s.tripleDoc && c != '`' && strings.HasPrefix(src[i:], triple) {
				end := strings.Index(src[i+3:], triple)
				stop := len(src)
				if end >= 0 {
					stop = i + 3 + end + 3
				}
				if strings.TrimSpace(src[lineStart:i]) == "" {
					blank(i, stop)
				}
				i = stop
				continue
			}
			i = s.skipString(src, i)
			continue
		}
		i++
	}
	return string(out)
}

func (s syntax) opensLineComment(src string, i int) bool {
	for _, opener := range s.line {
		if !strings.HasPrefix(src[i:], opener) {
			continue
		}
		if !s.hashNeedsWS || i == 0 {
			return true
		}
		if prev := src[i-1]; prev == ' ' || prev == '\t' || prev == '\n' {
			return true
		}
	}
	return false
}

// skipString returns the index after the string literal that opens at i. A
// quote that never closes on its line (a Rust lifetime, a shell apostrophe in
// prose) ends at the line so one stray quote cannot hide the rest of the file.
func (s syntax) skipString(src string, i int) int {
	quote := src[i]
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			if quote == '`' && s.rawBacktick {
				continue
			}
			j++
		case quote:
			return j + 1
		case '\n':
			if quote != '`' {
				return j
			}
		}
	}
	return len(src)
}

func versionedReads(name, src string) []string {
	s, ok := syntaxFor(name)
	if !ok {
		return nil
	}
	code := s.code(src)
	var hits []string
	for _, loc := range versionedUpstream.FindAllStringIndex(code, -1) {
		line := 1 + strings.Count(code[:loc[0]], "\n")
		hits = append(hits, name+":"+strconv.Itoa(line)+": "+code[loc[0]:loc[1]])
	}
	return hits
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate the repository root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestNoVersionedUpstreamReadsInCode(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.CommandContext(t.Context(), "git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	listing, err := cmd.Output()
	if err != nil {
		t.Fatalf("list repository files: %v", err)
	}
	var hits []string
	scanned := 0
	for name := range bytes.SplitSeq(listing, []byte{0}) {
		rel := string(name)
		if rel == "" || strings.HasPrefix(rel, "test/parity/upstreamreads/") {
			continue
		}
		if _, ok := syntaxFor(rel); !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", rel, err)
		}
		scanned++
		hits = append(hits, versionedReads(rel, string(data))...)
	}
	if scanned < 1000 {
		t.Fatalf("scanned %d source files; the guard is not seeing the tree", scanned)
	}
	if len(hits) > 0 {
		t.Errorf("code reads a versioned .upstream directory; a public checkout mirrors only the pinned release, so read .upstream/current instead:\n%s", strings.Join(hits, "\n"))
	}
}

func TestVersionedReadScannerSeparatesCodeFromCitations(t *testing.T) {
	old := ".upstream/v0.87.1/packages/ai/test/data/red-circle.png"
	cases := []struct {
		name string
		file string
		src  string
		want int
	}{
		{"go string literal", "a_test.go", "x := os.ReadFile(\"../" + old + "\")\n", 1},
		{"go split join arguments", "a_test.go", "p := filepath.Join(root, \".upstream\", \"v0.87.1\", \"packages\")\n", 1},
		{"go raw string", "a_test.go", "const src = `import x from \"./" + old + "\"`\n", 1},
		{"go line comment", "a_test.go", "// " + old + ":12\n", 0},
		{"go trailing comment", "a_test.go", "name := \"x\" // " + old + ":134\n", 0},
		{"go block comment", "a_test.go", "/* " + old + "\n continues */\nx := 1\n", 0},
		{"go current", "a_test.go", "p := \"../.upstream/current/packages\"\n", 0},
		{"go current join", "a_test.go", "p := filepath.Join(root, \".upstream\", \"current\")\n", 0},
		{"go comment then code", "a_test.go", "// " + old + "\nx := \"" + old + "\"\n", 1},
		{"go url slashes in string do not open a comment", "a_test.go", "u := \"https://x.test\"; p := \"" + old + "\"\n", 1},
		{"js import", "a.mjs", "import x from \"../" + old + "\";\n", 1},
		{"js comment", "a.mjs", "// " + old + "\nconst x = 1;\n", 0},
		{"mts import", "a.mts", "import x from \"../" + old + "\";\n", 1},
		{"cts require", "a.cts", "const x = require(\"../" + old + "\");\n", 1},
		{"tsx path", "a.tsx", "const p = \"" + old + "\";\n", 1},
		{"python docstring", "a.py", "\"\"\"Cites " + old + ".\"\"\"\n", 0},
		{"python statement docstring on its own line", "a.py", "def f():\n    \"\"\"\n    " + old + "\n    \"\"\"\n", 0},
		{"python comment", "a.py", "# " + old + "\n", 0},
		{"python path", "a.py", "p = root / \"" + old + "\"\n", 1},
		{"python triple string used as value", "a.py", "src = \"\"\"" + old + "\"\"\"\n", 1},
		{"toml path", "a.toml", "pig_extensions = [\"../../" + old + "\"]\n", 1},
		{"toml comment", "a.toml", "# " + old + "\nx = 1\n", 0},
		{"toml trailing comment", "a.toml", "x = 1 # " + old + "\n", 0},
		{"shell hash inside word", "a.sh", "echo a#b " + old + "\n", 1},
		{"unknown extension", "a.json", "{\"source\": \"" + old + ":1\"}\n", 0},
		{"windows separator", "a.go", "p := `.upstream\\v0.87.1\\packages`\n", 1},
		{"go raw string ending in a backslash does not swallow the next comment", "a.go", "p := `C:\\`\n// " + old + "\n", 0},
		{"stray apostrophe in a comment-free line does not hide code", "a.rs", "fn f<'a>(x: &'a str) {}\nlet p = \"" + old + "\";\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionedReads(tc.file, tc.src); len(got) != tc.want {
				t.Fatalf("versionedReads(%s) = %v, want %d finding(s)", tc.file, got, tc.want)
			}
		})
	}
}

func TestVersionedReadScannerReportsTheLine(t *testing.T) {
	src := "// header\n\nx := \".upstream/v0.99.1/a\"\n"
	got := versionedReads("a.go", src)
	if len(got) != 1 || !strings.HasPrefix(got[0], "a.go:3: ") {
		t.Fatalf("versionedReads = %v, want one finding on line 3", got)
	}
}
