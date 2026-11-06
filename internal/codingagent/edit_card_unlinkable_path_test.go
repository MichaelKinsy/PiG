package codingagent

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// edit.ts renderCall resolves its path through renderToolPath, which throws for a file: URL that names a host or holds an encoded slash when the
// terminal supports hyperlinks, and tool-execution.ts then draws the call fallback: the tool name and its arguments as key=value pairs. (Pi's own edit
// card cannot be compared here: its preview rejects with no handler for these paths and ends the process.) Without hyperlink support, or for a path
// that resolves, the card draws the edit header. Which paths throw is Node's fileURLToPath for the host platform: on win32 file://host/x is the UNC path
// \\host\x and resolves, while file:///tmp/a%20b has no drive letter and throws "File URL path must be absolute"; on POSIX the host throws and the
// absolute path resolves. An encoded slash throws on both.
func TestEditCardDrawsTheCallFallbackForAnUnlinkablePath(t *testing.T) {
	previous := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previous) })
	for _, tc := range []struct {
		path       string
		hyperlinks bool
		fallback   bool
	}{
		{"file://host/x", true, runtime.GOOS != "windows"},
		{"file:///a%2Fb", true, true},
		{"file://host/x", false, false},
		{"file:///tmp/a%20b", true, runtime.GOOS == "windows"},
		{"plain.txt", true, false},
	} {
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true, Hyperlinks: tc.hyperlinks})
		raw, err := json.Marshal(map[string]any{"path": tc.path, "edits": []any{map[string]any{"oldText": "a", "newText": "b"}}})
		if err != nil {
			t.Fatal(err)
		}
		f := toolComponentRaw(t, "edit", "id-"+tc.path, raw, nil)
		f.card.SetArgsComplete()
		rows := f.plain(120)
		fallback := strings.Contains(rows, `path=`+`"`+tc.path+`"`)
		if fallback != tc.fallback {
			t.Errorf("path %q hyperlinks=%v: call fallback = %v, want %v\n%s", tc.path, tc.hyperlinks, fallback, tc.fallback, rows)
		}
	}
}
