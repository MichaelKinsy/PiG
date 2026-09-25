package tui

// Goldens snapshot the output of stable renderer functions. If the
// rendered output changes the test fails, surfacing silent visual
// regressions between worker sessions ().
//
// To regenerate: run `go test ./tui/ -run TestGolden -update`
// (the -update flag causes the test to rewrite the golden files).

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGoldens = flag.Bool("update", false, "rewrite golden files instead of comparing")

// goldenPath returns the path to the golden file for the given scenario name.
func goldenPath(name string) string {
	return filepath.Join("testdata", "goldens", name+".txt")
}

// checkGolden compares got to the named golden file.
// When -update is set it writes got to the file instead.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := goldenPath(name)
	if *updateGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("updated golden %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v: run with -update to create it", path, err)
	}
	if string(want) != got {
		t.Errorf("golden %s mismatch:\nwant:\n%s\ngot:\n%s", name, want, got)
	}
}

// TestGolden_FormatBashHeader: FormatBashHeader output is stable for
// typical command strings (plain command, ampersands, long truncation).
func TestGolden_FormatBashHeader(t *testing.T) {
	withTrueColor(t, true)
	SetTheme("dark")
	cases := []struct {
		name string
		raw  string
	}{
		{"simple", `{"command":"ls -la"}`},
		{"ampersand", `{"command":"git add . && git commit -m 'fix'"}`},
		{"long", `{"command":"find /usr/local/lib -name '*.go' -type f -exec grep -l 'context' {} \\; | sort | head -20"}`},
		{"empty", `{}`},
		{"timeout", `{"command":"sleep 10","timeout":30}`},
	}
	var out strings.Builder
	for _, tc := range cases {
		result := FormatBashHeader(json.RawMessage(tc.raw))
		out.WriteString(tc.name + ":\n" + result + "\n")
	}
	checkGolden(t, "format-bash-header", out.String())
}

// TestGolden_FormatToolArgs: FormatToolArgs output is stable for
// object, array, and string inputs.
func TestGolden_FormatToolArgs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"object", `{"path":"/tmp/foo.go","content":"package main\n"}`},
		{"array", `["one","two","three"]`},
		{"string", `"hello world"`},
		{"empty_obj", `{}`},
	}
	var out strings.Builder
	for _, tc := range cases {
		result := FormatToolArgs(json.RawMessage(tc.raw))
		out.WriteString(tc.name + ":\n" + result + "\n")
	}
	checkGolden(t, "format-tool-args", out.String())
}

// TestGolden_MarkdownRender: NewMarkdown's Render output is stable for
// headers, bold/italic, code fences, and bullet lists at width=80.
func TestGolden_MarkdownRender(t *testing.T) {
	withTrueColor(t, true)
	SetTheme("dark")
	src := "# Header One\n\n## Header Two\n\nPlain text with **bold** and *italic* and `inline code`.\n\n```go\nfunc Hello() string {\n\treturn \"world\"\n}\n```\n\n- bullet one\n- bullet two\n- bullet three\n"
	md := NewMarkdown(src)
	lines := md.Render(80)
	var out strings.Builder
	for _, l := range lines {
		out.WriteString(l + "\n")
	}
	checkGolden(t, "markdown-render", out.String())
}
