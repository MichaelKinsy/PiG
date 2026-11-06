package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// edit.ts renderCall resolves its path through renderToolPath, which throws for a file: URL that names a host or holds an encoded slash when the
// terminal supports hyperlinks, and tool-execution.ts then draws the call fallback: the tool name and its arguments as key=value pairs. (Pi's own edit
// card cannot be compared here: its preview rejects with no handler for these paths and ends the process.) Without hyperlink support, or for a path
// that resolves, the card draws the edit header.
func TestEditCardDrawsTheCallFallbackForAnUnlinkablePath(t *testing.T) {
	previous := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previous) })
	for _, tc := range []struct {
		path       string
		hyperlinks bool
		fallback   bool
	}{
		{"file://host/x", true, true},
		{"file:///a%2Fb", true, true},
		{"file://host/x", false, false},
		{"file:///tmp/a%20b", true, false},
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
