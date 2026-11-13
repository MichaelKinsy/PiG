//go:build !pig_strip_export_html

package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// export-html/index.ts exportFromFile (:290) and exportSessionToHtml (:238) are async but never await: every check (file exists, the session opens, the
// output directory exists) runs before writeFileSync, and the promise resolves with the path once the file holds the document. ExportFileToHTML returns
// the same way: a failed input writes nothing, and a returned path names a complete file.
func TestExportFileToHTMLWritesNothingWhenTheInputFailsAndACompleteFileOnSuccess(t *testing.T) {
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.jsonl")
	if err := os.WriteFile(garbage, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	valid := filepath.Join(dir, "valid.jsonl")
	if err := os.WriteFile(valid, []byte(`{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{"missing": filepath.Join(dir, "none.jsonl"), "directory": dir, "not a session": garbage} {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(dir, "failed-"+strings.ReplaceAll(name, " ", "-")+".html")
			if _, err := ExportFileToHTML(input, out); err == nil {
				t.Fatal("the export of a failing input resolved")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("a failing export left an output file behind (stat error %v)", err)
			}
		})
	}
	out := filepath.Join(dir, "ok.html")
	path, err := ExportFileToHTML(valid, out)
	if err != nil || path != out {
		t.Fatalf("path %q, error %v", path, err)
	}
	if html, readErr := os.ReadFile(out); readErr != nil || !strings.HasSuffix(strings.TrimSpace(string(html)), "</html>") {
		t.Fatalf("the returned path names an incomplete export: %v", readErr)
	}
}
