package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type recordingRemote struct {
	calls   []string
	changes int
}

func (r *recordingRemote) Input(data string)              { r.calls = append(r.calls, "input:"+data) }
func (r *recordingRemote) SetText(text string)            { r.calls = append(r.calls, "setText:"+text) }
func (r *recordingRemote) InsertTextAtCursor(text string) { r.calls = append(r.calls, "insert:"+text) }
func (r *recordingRemote) AddToHistory(text string)       { r.calls = append(r.calls, "history:"+text) }
func (r *recordingRemote) StateChanged()                  { r.changes++ }
func (r *recordingRemote) Mouse(event TuiMouseEvent) {
	r.calls = append(r.calls, "mouse:"+string(event.Type)+":"+strconv.Itoa(event.Y))
}

// With an extension's editor component installed, the editor forwards what
// Pi's host does to this.editor, shows the component's frames, and mirrors
// its text; removing the component keeps the mirrored text.
func TestEditorRemoteStandsInForTheEditor(t *testing.T) {
	e := NewEditor()
	e.Focused = true
	e.SetText("before")
	remote := &recordingRemote{}
	e.SetRemote(remote)
	if !e.IsRemote() || e.Remote() != remote {
		t.Fatal("remote not installed")
	}

	e.SetText("draft")
	e.Clear()
	e.InsertTextAtCursor("ins")
	e.AddToHistory("hist")
	e.HandleInput("x")
	want := []string{"setText:draft", "setText:", "insert:ins", "history:hist", "input:x"}
	if !slices.Equal(remote.calls, want) {
		t.Fatalf("remote calls = %q, want %q", remote.calls, want)
	}
	if e.Text() != "" {
		t.Fatalf("mirrored text after Clear = %q", e.Text())
	}
	if e.AutocompleteOpen() {
		t.Fatal("the host's autocomplete opened for a remote editor")
	}

	e.ApplyRemoteChange("[paste #1 +20 lines]", "expanded paste")
	if e.Text() != "[paste #1 +20 lines]" || e.GetExpandedText() != "expanded paste" {
		t.Fatalf("mirror = %q / %q", e.Text(), e.GetExpandedText())
	}
	e.ApplyRemoteChange("!ls", "!ls")
	if !e.IsBashMode() {
		t.Fatal("bash mode not read from the mirrored text")
	}

	e.SetRemoteFrame([]string{"top", "text\x1b_pi:c\x07", "bottom"}, 20, true)
	if got := e.Render(20); !slices.Equal(got, []string{"top", "text\x1b_pi:c\x07", "bottom"}) {
		t.Fatalf("Render = %q", got)
	}
	if !e.WantsKeyRelease() {
		t.Fatal("wantsKeyRelease not taken from the frame")
	}
	// A frame for another width is clipped until the component re-renders.
	e.SetRemoteFrame([]string{"0123456789ABCDEF"}, 16, false)
	if got := e.Render(10); len(got) != 1 || widthx.VisibleWidth(got[0]) != 10 || !strings.HasPrefix(got[0], "0123456789") {
		t.Fatalf("stale-width Render = %q", got)
	}

	remote.calls = nil
	if got := e.HandleMouse(TuiMouseEvent{Type: MousePress, Button: MouseButtonLeft, Y: 2}); got != nil {
		t.Fatal("a press was handled; the renderer owns selection")
	}
	if got := e.HandleMouse(TuiMouseEvent{Type: MouseClick, Button: MouseButtonLeft, Y: 2}); got == nil || !got.Handled {
		t.Fatal("a click on the editor rows was not handled")
	}
	if !slices.Equal(remote.calls, []string{"mouse:click:2"}) {
		t.Fatalf("mouse forwarded as %q", remote.calls)
	}

	changes := remote.changes
	e.SetPaddingX(2)
	e.SetAutocompleteMaxVisible(8)
	e.Focused = false
	e.ThinkingLevel = "high"
	e.Render(10)
	if remote.changes != changes+3 {
		t.Fatalf("state changes reported = %d, want %d", remote.changes-changes, 3)
	}

	e.ApplyRemoteChange("kept", "kept")
	e.SetRemote(nil)
	if e.IsRemote() || e.Text() != "kept" {
		t.Fatalf("after removal: remote=%v text=%q", e.IsRemote(), e.Text())
	}
	e.HandleInput("!")
	if e.Text() != "kept!" {
		t.Fatalf("own editing after removal: %q", e.Text())
	}
}

// A remote component's autocomplete query is answered by the editor's own
// providers, without touching the editor's buffer.
func TestEditorRemoteSuggestionsUseTheEditorsProviders(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider([]SlashCommand{{Name: "model", Description: "Select model"}, {Name: "settings"}}))
	base := e.AutocompleteProvider()
	provider := &AsyncAutocompleteProvider{GetSuggestions: func(ctx context.Context, lines []string, line, col int, force bool) (*AutocompleteSuggestions, error) {
		result, err := NewAutocompleteQuery(base, lines, line, col, force).RunResult(ctx)
		if err != nil || result == nil {
			return result, err
		}
		result.Items = append(result.Items, AutocompleteItem{Value: "ext", Label: "ext"})
		return result, nil
	}}
	e.SetAsyncAutocomplete(provider, t.Context(), nil, nil, nil)
	e.SetRemote(&recordingRemote{})
	got, err := provider.GetSuggestions(t.Context(), []string{"/mo"}, 0, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Prefix != "/mo" || len(got.Items) != 2 || got.Items[0].Value != "model" || got.Items[1].Value != "ext" {
		t.Fatalf("suggestions = %+v", got)
	}
	if e.Text() != "" {
		t.Fatalf("query changed the buffer to %q", e.Text())
	}
	if got, err := provider.GetSuggestions(t.Context(), []string{"!ls /mo"}, 0, 7, false); got != nil || err != nil {
		t.Fatalf("bash-mode suggestions = %+v", got)
	}
}
