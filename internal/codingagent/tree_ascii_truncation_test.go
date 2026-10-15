package codingagent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// The /tree listing cuts a message at 60 characters. A byte cut inside a multi-byte character left invalid UTF-8 on screen.
func TestRenderTreeASCIICutsMessagesAtCharactersNotBytes(t *testing.T) {
	session := NewSession("tree-ascii", t.TempDir())
	if _, err := session.AppendMessage(mkUserMsg(strings.Repeat("界", 100))); err != nil {
		t.Fatal(err)
	}
	tree := session.GetTree()
	if len(tree) != 1 {
		t.Fatalf("tree roots = %d", len(tree))
	}
	out := renderTreeASCII(&SessionTreeNode{Children: tree})
	if !utf8.ValidString(out) || !strings.Contains(out, strings.Repeat("界", 60)+"…") || strings.Contains(out, strings.Repeat("界", 61)) {
		t.Fatalf("listing = %q, want the first 60 characters then an ellipsis", out)
	}
}

// The cut counts UTF-16 units, as a JavaScript slice does. A cut between the two halves of a surrogate pair writes U+FFFD, as Node writes a
// lone surrogate, never invalid UTF-8.
func TestRenderTreeASCIICutInsideASurrogatePairWritesTheReplacementCharacter(t *testing.T) {
	session := NewSession("tree-ascii-emoji", t.TempDir())
	if _, err := session.AppendMessage(mkUserMsg("a" + strings.Repeat("😀", 40))); err != nil {
		t.Fatal(err)
	}
	out := renderTreeASCII(&SessionTreeNode{Children: session.GetTree()})
	if want := "a" + strings.Repeat("😀", 29) + "\uFFFD…"; !utf8.ValidString(out) || !strings.Contains(out, want) {
		t.Fatalf("listing = %q, want %q", out, want)
	}
}

// A legacy PiG bash_execution entry labels its command cut at 50 UTF-16 units.
func TestTreeRowCutsALegacyBashCommandAtCharacters(t *testing.T) {
	for _, tc := range []struct{ command, want string }{
		{strings.Repeat("界", 60), "[bash: !" + strings.Repeat("界", 50) + "…]"},
		{"a" + strings.Repeat("😀", 30), "[bash: !a" + strings.Repeat("😀", 24) + "\uFFFD…]"},
	} {
		raw, err := json.Marshal(map[string]any{"type": "bash_execution", "id": "b1", "parentId": nil, "timestamp": "2026-05-11T00:00:00Z", "role": "bashExecution", "command": tc.command, "output": "", "exitCode": 0})
		if err != nil {
			t.Fatal(err)
		}
		got := newTreeRowFormatter(nil).FormatTreeRow(sessionentry.DecodeSessionEntry(raw))
		if !utf8.ValidString(got) || !strings.Contains(got, tc.want) {
			t.Fatalf("row = %q, want it to contain %q", got, tc.want)
		}
	}
}
