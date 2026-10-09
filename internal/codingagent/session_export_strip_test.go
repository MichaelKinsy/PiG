package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A Piglet that strips export-html, at runtime or by compiling it out of its Binary, refuses /export to HTML (and RPC
// export_html, which calls ExportSessionToHTML) with the stripped message through the command error path, while
// /export to .jsonl keeps working. Stock PiG writes the HTML.
func TestStripExportHTMLReportsItAndKeepsJSONL(t *testing.T) {
	dir := chdirTemp(t)
	session, _ := branchedExportSession(t)
	if !pigstrip.Has(pigstrip.ListFeatures, pigstrip.ExportHTML) {
		sessionPath := filepath.Join(t.TempDir(), "s.jsonl")
		jsonl := `{"type":"session","version":3,"id":"strip","timestamp":"2026-10-05T12:00:00Z","cwd":"` + filepath.ToSlash(dir) + `"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-10-05T12:00:01Z","message":{"role":"user","content":"hello","timestamp":1}}
`
		if err := os.WriteFile(sessionPath, []byte(jsonl), 0o644); err != nil {
			t.Fatal(err)
		}
		written, err := ExportSessionToHTML(sessionPath, "stock.html", nil, dir, ShareState{}, "")
		if err != nil {
			t.Fatalf("stock HTML export: %v", err)
		}
		if info, statErr := os.Stat(filepath.Join(dir, written)); statErr != nil || info.Size() == 0 {
			t.Fatalf("stock HTML export wrote %q: %v", written, statErr)
		}
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.ExportHTML))

	sc, out := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return session }
	sc.Args = "out.html"
	err := exportHandler(sc)
	want := "Failed to export session: HTML export is stripped from this Piglet (strip.features: export-html)"
	if err == nil || err.Error() != want {
		t.Fatalf("/export out.html: err = %v, want %q", err, want)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "out.html")); !os.IsNotExist(statErr) {
		t.Fatalf("stripped export wrote out.html: %v", statErr)
	}
	if ExportToolRenderers(nil) != nil {
		t.Fatal("stripped export resolved tool renderers")
	}

	sc.Args = "out.jsonl"
	if err := exportHandler(sc); err != nil {
		t.Fatalf("/export out.jsonl with HTML export stripped: %v", err)
	}
	if got := out.String(); got != "Session exported to: "+filepath.Join(dir, "out.jsonl")+"\n" {
		t.Fatalf("/export out.jsonl status = %q", got)
	}
}
