package codingagent

import (
	"context"
	"slices"
	"testing"
)

// Upstream handleDebugCommand confirms with Spacer(1) and
// Text(accent "✓ Debug log written" + "\n" + muted path, 1, 1)
// (interactive-mode.ts:6698-6701). The path is plain text, so a Windows path
// keeps every backslash; Markdown would read "\." as an escaped period.
func TestDebugCommandShowsThePathVerbatim(t *testing.T) {
	m := sessionChromeMode(t)
	m.chatContainer.Clear()
	const path = `C:\Users\someone\.pig\agent\pig-debug.log`
	sc := m.buildSlashContext(context.Background())
	sc.WriteDebugLog = func() (string, error) { return path, nil }
	if err := debugHandler(sc); err != nil {
		t.Fatal(err)
	}
	want := []string{"", "", " ✓ Debug log written", " " + path, ""}
	if got := renderedPlain(m.chatContainer); !slices.Equal(got, want) {
		t.Fatalf("transcript after /debug = %q, want %q", got, want)
	}
}
