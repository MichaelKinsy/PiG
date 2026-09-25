package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Streaming a reply must stay on the differential path. A full render clears
// the screen and scrollback, so taking it per chunk flickers and destroys the
// history the user is reading. Viewport growth alone does not require recovery;
// only a changed range outside the visible viewport does.
func TestStreamingDoesNotForceFullRepaints(t *testing.T) {
	for _, height := range []int{40, 20, 12} {
		t.Run(fmt.Sprintf("height-%d", height), func(t *testing.T) {
			w := &countingWriter{}
			ui := NewWithOutput(w, 100, height)
			for i := range 30 {
				ui.Add(NewText(fmt.Sprintf("seed line %d", i)))
			}
			streaming := NewText("")
			ui.Add(streaming)
			ui.Render()

			before := w.clears
			var body strings.Builder
			for i := range 40 {
				fmt.Fprintf(&body, "streamed row %d\n", i)
				streaming.SetText(body.String())
				ui.Render()
			}
			if got := w.clears - before; got != 0 {
				t.Errorf("streaming 40 chunks at height %d caused %d full clear+scrollback repaints, want 0",
					height, got)
			}
		})
	}
}

func TestViewportTailShrinkKeepsPiViewportOrigin(t *testing.T) {
	t.Setenv("PI_CLEAR_ON_SHRINK", "")
	w := &countingWriter{}
	ui := NewWithOutput(w, 100, 12)
	component := &fixedLinesComponent{lines: make([]string, 63)}
	for i := range component.lines {
		component.lines[i] = fmt.Sprintf("row %d", i)
	}
	ui.Add(component)
	ui.Render()
	if ui.prevViewportTop != 51 {
		t.Fatalf("initial viewport top = %d, want 51", ui.prevViewportTop)
	}

	w.seen.Reset()
	beforeBytes := w.bytes
	beforeClears := w.clears
	component.lines = append([]string(nil), component.lines[:61]...)
	component.lines[56] = "changed"
	ui.Render()
	if got := w.clears - beforeClears; got != 0 {
		t.Fatalf("viewport-tail shrink cleared screen or scrollback %d times", got)
	}
	written := w.bytes - beforeBytes
	if written > 12*150 {
		t.Fatalf("viewport-tail shrink wrote %d bytes for a 12-row viewport", written)
	}
	if strings.Contains(w.seen.String(), "\x1b[H") {
		t.Fatal("viewport-tail shrink homed the cursor and repainted the viewport")
	}
	if strings.Contains(w.seen.String(), "row 49") {
		t.Fatal("viewport-tail shrink exposed history above Pi's existing viewport")
	}
	if !strings.Contains(w.seen.String(), "changed") || !strings.Contains(w.seen.String(), "row 60") {
		t.Fatalf("viewport-tail shrink omitted changed visible rows: %q", w.seen.String())
	}
	if ui.prevViewportTop != 51 {
		t.Fatalf("shrunk viewport top = %d, want Pi's retained origin 51", ui.prevViewportTop)
	}

	w.seen.Reset()
	component.lines[60] = "changed tail"
	ui.Render()
	if strings.Contains(w.seen.String(), clearSeq) || !strings.Contains(w.seen.String(), "changed tail") {
		t.Fatalf("post-shrink differential frame is invalid: %q", w.seen.String())
	}
}

func TestSlashAutocompleteShrinkDoesNotReplayTranscript(t *testing.T) {
	t.Setenv("PI_CLEAR_ON_SHRINK", "")
	commands := []SlashCommand{
		{Name: "session"}, {Name: "settings"}, {Name: "select"}, {Name: "send"},
		{Name: "search"}, {Name: "skills"}, {Name: "status"}, {Name: "switch"},
		{Name: "export"}, {Name: "resume"}, {Name: "help"}, {Name: "model"},
	}
	measure := func(turns int) (clears, maxFrameBytes int) {
		w := &countingWriter{}
		ui, _, _ := buildConversation(w, turns)
		editor := NewEditor()
		editor.Focused = true
		editor.SetAutocomplete(NewSlashOnlyProvider(commands))
		ui.Add(editor)
		ui.Render()
		for _, key := range "/session" {
			beforeBytes := w.bytes
			beforeClears := w.clears
			editor.HandleInput(string(key))
			ui.Render()
			clears += w.clears - beforeClears
			maxFrameBytes = max(maxFrameBytes, w.bytes-beforeBytes)
		}
		return clears, maxFrameBytes
	}

	shortClears, shortBytes := measure(40)
	longClears, longBytes := measure(400)
	t.Logf("slash autocomplete max frame: 40 turns=%d bytes, 400 turns=%d bytes", shortBytes, longBytes)
	if shortClears != 0 || longClears != 0 {
		t.Fatalf("typing /session caused full clears: 40 turns=%d, 400 turns=%d", shortClears, longClears)
	}
	if longBytes > shortBytes+500 {
		t.Fatalf("autocomplete frame output scales with transcript: 40 turns=%d bytes, 400 turns=%d bytes", shortBytes, longBytes)
	}
}

func TestAtFileAutocompleteShrinkDoesNotReplayTranscript(t *testing.T) {
	t.Setenv("PI_CLEAR_ON_SHRINK", "")
	dir := t.TempDir()
	for _, name := range []string{"session.go", "session_test.go", "selector.go", "settings.go", "shell.go", "status.go", "alpha.go", "beta.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	measure := func(turns int) (clears, maxFrameBytes int) {
		w := &countingWriter{}
		ui, _, _ := buildConversation(w, turns)
		editor := NewEditor()
		editor.Focused = true
		editor.SetAutocomplete(NewCombinedProvider(nil, dir, ""))
		ui.Add(editor)
		ui.Render()
		for _, key := range "@session" {
			beforeBytes := w.bytes
			beforeClears := w.clears
			editor.HandleInput(string(key))
			ui.Render()
			clears += w.clears - beforeClears
			maxFrameBytes = max(maxFrameBytes, w.bytes-beforeBytes)
		}
		return clears, maxFrameBytes
	}

	shortClears, shortBytes := measure(40)
	longClears, longBytes := measure(400)
	t.Logf("@ autocomplete max frame: 40 turns=%d bytes, 400 turns=%d bytes", shortBytes, longBytes)
	if shortClears != 0 || longClears != 0 {
		t.Fatalf("typing @session caused full clears: 40 turns=%d, 400 turns=%d", shortClears, longClears)
	}
	if longBytes > shortBytes+500 {
		t.Fatalf("@ completion frame output scales with transcript: 40 turns=%d bytes, 400 turns=%d bytes", shortBytes, longBytes)
	}
}

func TestFilterableMenuShrinkDoesNotReplayTranscript(t *testing.T) {
	t.Setenv("PI_CLEAR_ON_SHRINK", "")
	w := &countingWriter{}
	ui, _, _ := buildConversation(w, 400)
	labels := make([]string, 20)
	for i := range labels {
		labels[i] = fmt.Sprintf("model-%02d", i)
	}
	labels = append(labels, "unique-choice")
	menu := NewFilterableList("Models", labels)
	ui.Add(menu)
	ui.Render()

	beforeBytes := w.bytes
	beforeClears := w.clears
	menu.HandleInput("unique")
	ui.Render()
	if got := w.clears - beforeClears; got != 0 {
		t.Fatalf("filtering interactive menu caused %d full clears", got)
	}
	if written := w.bytes - beforeBytes; written > 40*150 {
		t.Fatalf("filtering interactive menu wrote %d bytes for a 40-row viewport", written)
	}
}

func TestInteractiveOverlayCloseDoesNotReplayTranscript(t *testing.T) {
	t.Setenv("PI_CLEAR_ON_SHRINK", "")
	w := &countingWriter{}
	ui, _, _ := buildConversation(w, 400)
	ui.OpenOverlay(NewExtensionSelector("Choose", []string{"one", "two", "three"}), OverlayOptions{})
	ui.Render()
	beforeBytes := w.bytes
	beforeClears := w.clears
	ui.hideOverlay()
	ui.Render()
	if got := w.clears - beforeClears; got != 0 {
		t.Fatalf("closing interactive overlay caused %d full clears", got)
	}
	if written := w.bytes - beforeBytes; written > 40*150 {
		t.Fatalf("closing interactive overlay wrote %d bytes for a 40-row viewport", written)
	}
}

// A tall component collapse begins above the visible viewport. Pi handles this
// with fullRender(true), which clears obsolete physical history before replaying
// the current logical transcript once.
func TestViewportShrinkUsesPiFullRender(t *testing.T) {
	w := &countingWriter{}
	ui := NewWithOutput(w, 100, 20)
	for i := range 10 {
		ui.Add(NewText(fmt.Sprintf("seed %d", i)))
	}
	tall := NewText("")
	var body strings.Builder
	for i := range 40 {
		fmt.Fprintf(&body, "row %d\n", i)
	}
	tall.SetText(body.String())
	ui.Add(tall)
	ui.Render()

	w.seen.Reset()
	tall.SetText("collapsed")
	ui.Render()
	if !strings.Contains(w.seen.String(), "\x1b[2J\x1b[H\x1b[3J") {
		t.Error("collapsing a tall block did not use Pi's fullRender(true)")
	}
	if !strings.Contains(w.seen.String(), "seed 0") {
		t.Error("collapsing a tall block did not replay the current logical transcript")
	}
}
