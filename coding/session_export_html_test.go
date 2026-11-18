//go:build !pig_strip_export_html

package coding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// upstream: agent-session.ts exportToHtml exports the Session file, naming an unwritten Session
// ("Nothing to export yet - start a conversation first") and writing the HTML to outputPath once it has entries.
// Pi: packages/coding-agent/src/core/agent-session.ts:4273 (Session.exportToHtml). A Piglet Binary built with pig_strip_export_html
// (D92) has no HTML export: its ExportToHTML says so.
func TestSessionExportToHTML(t *testing.T) {
	sess := newStateTestSession(t, fakeModel())
	out := filepath.Join(t.TempDir(), "session.html")
	if _, err := sess.ExportToHTML(out); err == nil || !strings.Contains(err.Error(), "Nothing to export yet") {
		t.Fatalf("export before the first turn = %v", err)
	}
	if _, err := sess.Send(t.Context(), "export marker prompt"); err != nil {
		t.Fatal(err)
	}
	got, err := sess.ExportToHTML(out)
	if err != nil {
		t.Fatal(err)
	}
	if got != out {
		t.Errorf("path = %q, want %q", got, out)
	}
	html, err := os.ReadFile(out)
	if err != nil || len(html) == 0 {
		t.Fatalf("exported file: %v (%d bytes)", err, len(html))
	}
}
