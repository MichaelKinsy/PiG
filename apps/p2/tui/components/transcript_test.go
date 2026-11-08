// The TUI transcript component: a render whose callbacks (OnTop, OnMissingAnchor, OnVisibleGap) sync the transcript synchronously (store change ->
// runtime.changed() -> sync) must finish on the views it rendered.
//
// pi: apps/p2/test/p2/tui-transcript.test.ts
package components

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// testContext is a TranscriptContext over the dark theme.
type testContext struct {
	themeRef *theme.ThemeRef
}

func newTestContext(t testing.TB) *testContext {
	t.Helper()
	dark, _ := theme.BuiltinTheme("dark")
	built, err := theme.CreateTheme(dark, pitui.TerminalColorModeTrueColor, theme.TerminalState{})
	if err != nil {
		t.Fatal(err)
	}
	return &testContext{themeRef: theme.NewThemeRef(built)}
}

func (c *testContext) Theme() *theme.ThemeRef                             { return c.themeRef }
func (c *testContext) KeyText(string) string                              { return "ctrl+o" }
func (c *testContext) RequestRender()                                     {}
func (c *testContext) MarkdownTransformers() []render.MarkdownTransformer { return nil }
func (c *testContext) Settings() *prefs.TuiSettings                       { return nil }
func (c *testContext) OutputPad() int                                     { return 1 }
func (c *testContext) Cwd() string                                        { return "/work" }
func (c *testContext) ToolRenderers(string) *tools.ToolRenderers          { return nil }
func (c *testContext) EntryRenderers() map[string]*EntryRenderer          { return nil }

func userEntry(id int) durable.EntryRecord {
	return durable.EntryRecord{
		Id: durable.EntryId(id), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "pi.user",
		Model: []ai.Message{ai.UserMessage{Content: ai.UserText(fmt.Sprintf("message %d", id)), Timestamp: int64(id)}},
	}
}

func transcriptOf(ids ...int) *transcript.Transcript {
	view := harness.TranscriptView{}
	for _, id := range ids {
		view.Items = append(view.Items, harness.TranscriptEntry{Entry: userEntry(id)})
	}
	built := transcript.BuildTranscript(view, nil, nil)
	return &built
}

func TestRenderWhoseOnTopSyncsALongerTranscriptDoesNotCrash(t *testing.T) {
	view := NewTranscriptView(newTestContext(t), ViewState{}, nil)
	scroll := pitui.NewScrollView(view, pitui.ScrollViewOptions{Follow: "end"})
	view.Attach(scroll)
	view.Sync(transcriptOf(1), nil)
	// Everything fits: the viewport is at the top, so render asks for older history.
	scroll.UpdateLayout(1, 40, func() {})
	synced := false
	view.OnTop = func() {
		if synced {
			return
		}
		synced = true
		// As `loadEarlier` does through the store and runtime listeners, before the render returns.
		view.Sync(transcriptOf(1, 2, 3, 4), nil)
	}
	view.Render(80)
	if !synced {
		t.Fatal("onTop not called")
	}
	if out := strings.Join(view.Render(80), "\n"); !strings.Contains(out, "message 4") {
		t.Fatalf("no message 4: %q", out)
	}
}
