//go:build !pig_strip_export_html

package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// core/export-html/index.ts exportFromFile through SessionManager.open (session-manager.ts:1766, _setSessionFile 1029-1055), as the installed Pi 1.1.0
// answered for each input (scenarios 26-30 and export-html 01-06 compare the CLI pair).
func TestExportFileToHTMLMatchesPi(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	valid := write("valid.jsonl", `{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp"}`+"\n")
	stringVersion := write("old.jsonl", `{"type":"session","id":"s2","cwd":"/tmp","timestamp":"2026-01-01T00:00:00Z","version":"0.69.0"}`+"\n")
	garbage := write("garbage.jsonl", "not json\n")
	empty := write("empty.jsonl", "")
	out := filepath.Join(dir, "out.html")
	for _, tc := range []struct {
		name, input, output, wantErr string
	}{
		{"valid", valid, out, ""},
		{"header version is a string", stringVersion, out, ""},
		{"empty file is a new session", empty, out, ""},
		{"missing", filepath.Join(dir, "none.jsonl"), out, "File not found: " + filepath.Join(dir, "none.jsonl")},
		{"directory", dir, out, "EISDIR: illegal operation on a directory, read"},
		{"not a session", garbage, out, "Session file is not a valid pi session: " + garbage},
		{"output directory missing", valid, filepath.Join(dir, "nodir", "out.html"), "ENOENT: no such file or directory, open '" + filepath.Join(dir, "nodir", "out.html") + "'"},
		{"output is a directory", valid, dir, "EISDIR: illegal operation on a directory, open '" + dir + "'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, err := ExportFileToHTML(tc.input, tc.output)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || path != tc.output {
				t.Fatalf("path %q, error %v", path, err)
			}
			if html, readErr := os.ReadFile(path); readErr != nil || !strings.HasPrefix(string(html), "<!DOCTYPE html>") {
				t.Fatalf("export is not HTML: %v", readErr)
			}
		})
	}
}

// export-html/index.ts:296 opens the input with SessionManager.open(resolvedInputPath), whose session directory is the
// file's parent, which exists. An export therefore creates no default session directory under the agent directory.
func TestExportFileToHTMLCreatesNoSessionDirectoryLikePi(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	dir := t.TempDir()
	input := filepath.Join(dir, "valid.jsonl")
	if err := os.WriteFile(input, []byte(`{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportFileToHTML(input, filepath.Join(dir, "out.html")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(agentDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("agent directory after export = %v, %v; want it left empty", entries, err)
	}
}
