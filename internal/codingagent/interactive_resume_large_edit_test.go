package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// largeEditWord is one line of the edited file: an unbroken run, as a line of exported HTML is, ending in the edited word.
func largeEditWord(size int, ending string) string {
	const unit = "0123456789abcdef"
	return strings.Repeat(unit, size/len(unit)) + ending
}

// writeLargeEditSession writes a Session file as Pi persists it: a user message, an assistant edit call, the edit result whose details.diff
// holds one removed and one added line of lineBytes each, and then the given later messages.
func writeLargeEditSession(t *testing.T, lineBytes int, later ...map[string]any) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "large-edit.jsonl")
	diff := "-1 " + largeEditWord(lineBytes, "OLD") + "\n+1 " + largeEditWord(lineBytes, "NEW")
	messages := append([]map[string]any{
		{"role": "user", "content": []any{map[string]any{"type": "text", "text": "LARGE-EDIT-START"}}},
		largeEditAssistant(map[string]any{"type": "toolCall", "id": "edit-call", "name": "edit", "arguments": map[string]any{
			"path": "export.html", "edits": []any{map[string]any{"oldText": "OLD", "newText": "NEW"}},
		}}, "toolUse", ""),
		{
			"role": "toolResult", "toolCallId": "edit-call", "toolName": "edit", "isError": false, "timestamp": 1794528003000,
			"content": []any{map[string]any{"type": "text", "text": "Successfully replaced 1 block(s) in export.html."}},
			"details": map[string]any{"diff": diff, "firstChangedLine": 1},
		},
	}, later...)
	var file bytes.Buffer
	header, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": "large-edit", "timestamp": "2026-11-13T00:00:00.000Z", "cwd": dir})
	if err != nil {
		t.Fatal(err)
	}
	file.Write(append(header, '\n'))
	var parent any
	for i, message := range messages {
		id := "a000000" + string(rune('1'+i))
		line, err := json.Marshal(map[string]any{"type": "message", "id": id, "parentId": parent, "timestamp": "2026-11-13T00:00:01.000Z", "message": message})
		if err != nil {
			t.Fatal(err)
		}
		file.Write(append(line, '\n'))
		parent = id
	}
	if err := os.WriteFile(path, file.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// largeEditAssistant is a persisted assistant message with one content block.
func largeEditAssistant(block map[string]any, stopReason, errorMessage string) map[string]any {
	message := map[string]any{
		"role": "assistant", "content": []any{block}, "api": "faux", "provider": "test-faux", "model": "faux-1", "stopReason": stopReason, "timestamp": 1794528002000,
		"usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
	}
	if errorMessage != "" {
		message["errorMessage"] = errorMessage
	}
	return message
}

// Pi 5b5be2c316 (#12145): a session whose edit result holds a very large diff crashed Pi on every resume, in ToolExecutionComponent.render
// (lines.push(...contentLines) over hundreds of thousands of wrapped rows). PiG never had the argument limit; this resumes such a session
// through the production reader and transcript and pins what the Go path can lose: the card has every wrapped row (the count is derived
// from the diff and the width, not read from the output), the first and last rows are the diff's, and drawing the unchanged card again
// does not wrap the diff again (Pi's Text keeps its wrapped rows per text and width too). The history is also poisoned the usual ways:
// an errored assistant turn after the result, and the result as the last entry.
func TestResumedSessionWithVeryLargeEditDiffPi5b5be2c31(t *testing.T) {
	const (
		width     = 20
		outputPad = 1
		lineBytes = 2_700_000
	)
	word := largeEditWord(lineBytes, "NEW")
	inner := width - 2*outputPad
	// Text wrapping: "-1" stays on its row, since the word after it does not fit there, and the word breaks every inner columns.
	chunks := (len(word) + inner - 1) / inner
	lastChunk := word[(chunks-1)*inner:]
	// One blank row, the box's top padding, the header, a spacer, the two diff lines, the bottom padding.
	const blank, top, header, spacer, bottom = 1, 1, 1, 1, 1
	firstDiffRow := blank + top + header + spacer
	wantRows := firstDiffRow + 2*(1+chunks) + bottom
	if chunks < 150_000 {
		t.Fatalf("the fixture wraps to %d rows per line, want Pi's case of several hundred thousand", chunks)
	}

	for _, tc := range []struct {
		name  string
		later []map[string]any
		tail  string
	}{
		{"assistant text after the result", []map[string]any{largeEditAssistant(map[string]any{"type": "text", "text": "LARGE-EDIT-END"}, "stop", "")}, "LARGE-EDIT-END"},
		{"errored assistant turn after the result", []map[string]any{largeEditAssistant(map[string]any{"type": "text", "text": ""}, "error", "connection reset")}, ""},
		{"result is the last entry", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, path := writeLargeEditSession(t, lineBytes, tc.later...)
			loaded, err := NewSessionManagerWithDir(dir, dir).Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			var terminal bytes.Buffer
			m := &InteractiveMode{
				opts:          InteractiveModeOptions{SessionHandle: &recordingCompactHandle{inner: loaded}},
				chatContainer: tui.NewContainer(),
				tuiInst:       tui.NewWithOutput(&terminal, width, 30),
				toolByID:      make(map[string]*tui.ToolExecutionComponent),
				toolStarts:    make(map[string]time.Time),
				outputPad:     outputPad,
			}
			m.tuiInst.Add(m.chatContainer)
			m.renderSessionEntries()

			var card *tui.ToolExecutionComponent
			for _, child := range m.chatContainer.Children() {
				if tool, ok := child.(*tui.ToolExecutionComponent); ok {
					card = tool
				}
			}
			if card == nil {
				t.Fatal("the resumed transcript has no edit card")
			}
			rows := card.Render(width)
			if len(rows) != wantRows {
				t.Fatalf("edit card = %d rows, want %d", len(rows), wantRows)
			}
			plain := func(index int) string { return strings.TrimSpace(stripANSITest(rows[index])) }
			for _, row := range []struct {
				index int
				want  string
			}{
				{0, ""},
				{blank + top, "edit export.html"},
				{firstDiffRow, "-1"},
				{firstDiffRow + 1, word[:inner]},
				{firstDiffRow + chunks, "OLD"},
				{firstDiffRow + 1 + chunks, "+1"},
				{firstDiffRow + 2 + chunks, word[:inner]},
				{wantRows - bottom - 1, lastChunk},
				{wantRows - 1, ""},
			} {
				if got := plain(row.index); got != row.want {
					t.Errorf("card row %d = %q, want %q", row.index, got, row.want)
				}
			}

			allocs := testing.AllocsPerRun(3, func() { rows = card.Render(width) })
			if len(rows) != wantRows {
				t.Fatalf("second draw = %d rows, want %d", len(rows), wantRows)
			}
			if allocs > 256 {
				t.Fatalf("drawing the unchanged card allocated %.0f objects for %d rows: it wrapped or copied the diff again", allocs, wantRows)
			}

			chat := m.chatContainer.Render(width)
			if len(chat) <= wantRows {
				t.Fatalf("resumed chat = %d rows, want the card's %d and the messages around it", len(chat), wantRows)
			}
			if tc.tail != "" && !strings.Contains(stripANSITest(strings.Join(chat[len(chat)-4:], "\n")), tc.tail) {
				t.Errorf("resumed chat ends with %q, want %q after the card", chat[len(chat)-4:], tc.tail)
			}
		})
	}
}
