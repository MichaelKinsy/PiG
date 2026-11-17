//go:build !pig_strip_self_update

package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pig divergence (D39): "The update note prints as plain text". A configured update command's release note is printed
// verbatim under a heading: Markdown, HTML and a URL stay as written, with no link or other rendering.
func TestConfiguredSelfUpdatePrintsTheReleaseNoteAsPlainText(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	_, _ = prepareSelfUpdateCommand(t, "9.9.9", 0)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_SELF_UPDATE_COMMAND", "pi-wrapper upgrade")
	const note = "**Bold** `code` <b>tag</b> [link](https://example.test/x)\n- item one\nhttps://example.test/page"
	body, err := json.Marshal(note)
	if err != nil {
		t.Fatal(err)
	}
	server := releaseManifestServer(t, fmt.Sprintf(`{"version":"9.9.9","packageName":%q,"notes":%s,"binaries":{}}`, codingagent.PackageName, body))
	defer server.Close()
	t.Setenv("PIG_UPDATE_URL", server.URL)

	stdout, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })

	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "\nUpdate note\n"+note+"\n\n") {
		t.Fatalf("the note was not printed verbatim under its heading:\n%q", stdout)
	}
	if strings.Contains(stdout, "\x1b]8;") {
		t.Fatalf("the note carries a terminal hyperlink:\n%q", stdout)
	}
}
