package tools

// pi: packages/coding-agent/test/tools.test.ts "grep tool": 8da141c2a (#11705, #9276, #11423) and 6bf2ab9bb (#11895, #11807).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
)

func grepText(t *testing.T, dir string, params map[string]any) string {
	t.Helper()
	return runGrep(t, dir, params).Text()
}

func grepDetails(t *testing.T, dir string, params map[string]any) *GrepDetails {
	t.Helper()
	details, _ := runGrep(t, dir, params).Details.(*GrepDetails)
	return details
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGrepPrintsOverlappingContextOnceAndKeepsTheLastMatchsTrailingContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rg-context.txt")
	writeTestFile(t, path, []byte(strings.Join([]string{"a", "match one", "b", "match two", "match three", "c", "match four"}, "\n")))
	got := strings.SplitN(grepText(t, dir, map[string]any{"pattern": "match", "path": path, "context": 1, "limit": 2}), "\n\n", 2)[0]
	want := strings.Join([]string{
		"rg-context.txt-1- a",
		"rg-context.txt:2: match one",
		"rg-context.txt-3- b",
		"rg-context.txt:4: match two",
		// A match inside the last allowed match's trailing context is shown as context.
		"rg-context.txt-5- match three",
	}, "\n")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGrepKeepsCountingMatchesPastTheByteCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "byte-cap.txt")
	line := "match " + strings.Repeat("x", 400)
	writeTestFile(t, path, []byte(strings.Repeat(line+"\n", 199)+line))
	params := map[string]any{"pattern": "match", "path": path, "limit": 150}
	output := grepText(t, dir, params)
	details := grepDetails(t, dir, params)
	if !strings.Contains(output, "150 matches limit reached") || !strings.Contains(output, "50.0KB limit reached") {
		t.Fatalf("notices missing: %q", output[len(output)-200:])
	}
	if details.MatchLimitReached != 150 || details.Truncation == nil || details.Truncation.TotalLines != 150 {
		t.Fatalf("details = %+v", details)
	}
}

func TestGrepReportsTruncationWhenStoredOutputEndsExactlyAtTheByteCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x")
	// 103 rows of 500 bytes plus one of 12 bytes, with newlines, join to exactly 50KB; one more match follows.
	rows := make([]string, 0, 105)
	for range 103 {
		rows = append(rows, strings.Repeat("x", 490))
	}
	rows = append(rows, strings.Repeat("x", 7), "extra x")
	writeTestFile(t, path, []byte(strings.Join(rows, "\n")))
	params := map[string]any{"pattern": "x", "path": path, "limit": 1000}
	if output := grepText(t, dir, params); !strings.Contains(output, "[50.0KB limit reached]") {
		t.Fatalf("output tail = %q", output[len(output)-100:])
	}
	if details := grepDetails(t, dir, params); details.Truncation == nil || details.Truncation.TotalLines != 105 {
		t.Fatalf("details = %+v", details)
	}
}

func TestGrepAcceptsAHugeContextValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge-context.txt")
	writeTestFile(t, path, []byte("before\nneedle\nafter"))
	got := grepText(t, dir, map[string]any{"pattern": "needle", "path": path, "context": 1e30})
	want := "huge-context.txt-1- before\nhuge-context.txt:2: needle\nhuge-context.txt-3- after"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Context lines come from ripgrep, which decodes UTF-16; rereading the file as UTF-8 corrupted them (#11423).
func TestGrepDecodesUTF16FilesWithContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "utf16.txt")
	var data []byte
	for _, unit := range utf16.Encode([]rune("\uFEFFbefore\nneedle 中文\nafter\n")) {
		data = append(data, byte(unit), byte(unit>>8))
	}
	writeTestFile(t, path, data)
	got := grepText(t, dir, map[string]any{"pattern": "needle", "path": path, "context": 1})
	want := "utf16.txt-1- before\nutf16.txt:2: needle 中文\nutf16.txt-3- after"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGrepKeepsALoneCRInsideALineAndStripsCRLFTerminators(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed-cr.txt")
	writeTestFile(t, path, []byte("one\rtwo needle\r\nnext\r\n"))
	if got := grepText(t, dir, map[string]any{"pattern": "needle", "path": path}); got != "mixed-cr.txt:1: one\rtwo needle" {
		t.Fatalf("plain = %q", got)
	}
	got := grepText(t, dir, map[string]any{"pattern": "needle", "path": path, "context": 1})
	if want := "mixed-cr.txt:1: one\rtwo needle\nmixed-cr.txt-2- next"; got != want {
		t.Fatalf("with context = %q, want %q", got, want)
	}
}

func TestGrepDoesNotReportTheMatchLimitWhenExactlyLimitMatchesExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exact-limit.txt")
	writeTestFile(t, path, []byte("match one\nx\nmatch two\ny"))
	for _, context := range []float64{0, 1} {
		exact := map[string]any{"pattern": "match", "path": path, "limit": 2, "context": context}
		if out := grepText(t, dir, exact); strings.Contains(out, "limit reached") {
			t.Fatalf("exact, context %v: %q", context, out)
		}
		if details := grepDetails(t, dir, exact); details != nil && details.MatchLimitReached != 0 {
			t.Fatalf("exact, context %v: details %+v", context, details)
		}
		over := map[string]any{"pattern": "match", "path": path, "limit": 1, "context": context}
		if out := grepText(t, dir, over); !strings.Contains(out, "[1 matches limit reached. Use limit=2 for more, or refine pattern]") {
			t.Fatalf("over, context %v: %q", context, out)
		}
		if details := grepDetails(t, dir, over); details == nil || details.MatchLimitReached != 1 {
			t.Fatalf("over, context %v: details %+v", context, details)
		}
	}
	fractional := grepText(t, dir, map[string]any{"pattern": "match", "path": path, "limit": 1.5})
	if !strings.Contains(fractional, "[1 matches limit reached. Use limit=2 for more, or refine pattern]") {
		t.Fatalf("fractional = %q", fractional)
	}
}

// https://github.com/earendil-works/pi/issues/11807
func TestGrepResolvesFolderGlobsRelativeToPath(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	for _, sub := range []string{filepath.Join("migrations", "main"), filepath.Join("nested", "migrations", "main")} {
		if err := os.MkdirAll(filepath.Join(repo, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(repo, "migrations", "main", "a.js"), []byte("hello\n"))
	writeTestFile(t, filepath.Join(repo, "nested", "migrations", "main", "b.js"), []byte("hello\n"))
	searchPaths := []string{repo}
	// rg canonicalizes its cwd, so a symlinked path must still anchor the glob.
	link := filepath.Join(dir, "repo-link")
	if err := os.Symlink(repo, link); err == nil && runtime.GOOS != "windows" {
		searchPaths = append(searchPaths, link)
	}
	for _, searchPath := range searchPaths {
		// The decoy under nested/ stays out: the glob is anchored at path.
		if got := grepText(t, dir, map[string]any{"pattern": "hello", "path": searchPath, "glob": "migrations/main/*.js"}); got != "migrations/main/a.js:1: hello" {
			t.Fatalf("%s anchored: %q", searchPath, got)
		}
		anyDepth := strings.Split(grepText(t, dir, map[string]any{"pattern": "hello", "path": searchPath, "glob": "**/migrations/main/*.js"}), "\n")
		if len(anyDepth) != 2 || !(anyDepth[0] == "migrations/main/a.js:1: hello" && anyDepth[1] == "nested/migrations/main/b.js:1: hello" ||
			anyDepth[1] == "migrations/main/a.js:1: hello" && anyDepth[0] == "nested/migrations/main/b.js:1: hello") {
			t.Fatalf("%s any depth: %q", searchPath, anyDepth)
		}
		// rg matches nothing for a leading "./", so the tool drops it.
		if got := grepText(t, dir, map[string]any{"pattern": "hello", "path": searchPath, "glob": "./migrations/main/*.js"}); got != "migrations/main/a.js:1: hello" {
			t.Fatalf("%s dot slash: %q", searchPath, got)
		}
		if got := grepText(t, dir, map[string]any{"pattern": "hello", "path": searchPath, "glob": "!./migrations/**"}); got != "nested/migrations/main/b.js:1: hello" {
			t.Fatalf("%s negated dot slash: %q", searchPath, got)
		}
	}
}
