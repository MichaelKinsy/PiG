package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// clickCard is a renderer written as the posthorse oracle's (ui.ts) is: the state lives in a map keyed by the message, which Pi's WeakMap keys by the
// message object, a click on the MouseRegion toggles it, and a new global expansion resets it.
type clickCards struct {
	states     map[*extension.RenderIdentity]*clickCardState
	identities []*extension.RenderIdentity
}

type clickCardState struct{ global, expanded bool }

func (c *clickCards) render(message extension.CustomMessage, options extension.MessageRenderOptions, _ extension.Theme) extension.Component {
	c.identities = append(c.identities, options.Identity)
	if c.states == nil {
		c.states = map[*extension.RenderIdentity]*clickCardState{}
	}
	state := c.states[options.Identity]
	if state == nil || state.global != options.Expanded {
		state = &clickCardState{global: options.Expanded, expanded: options.Expanded}
		c.states[options.Identity] = state
	}
	content := &renderFunc{func() string { return fmt.Sprintf("%v:expanded=%t", message.Content, state.expanded) }}
	return tui.NewMouseRegion(content, func(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		if event.Type != tui.MouseClick || event.Button != tui.MouseButtonLeft {
			return nil
		}
		state.expanded = !state.expanded
		return &tui.TuiMouseEventResult{Handled: true}
	})
}

type renderFunc struct{ line func() string }

func (r *renderFunc) Render(int) []string { return []string{r.line()} }
func (*renderFunc) Invalidate()           {}

func clickOn(m *InteractiveMode, row int) *tui.TuiMouseDispatchResult {
	return tui.DispatchMouseEvent(m.chatContainer, tui.TuiMouseEvent{Type: tui.MouseClick, Button: tui.MouseButtonLeft, X: 2, Y: row, ScreenX: 2, ScreenY: row, Width: 80, Height: 6})
}

// Pi hands a message renderer the same message object on every rebuild; the transcript hands it the message's identity, which stays the same across
// rebuilds (a new global expansion) and differs between messages, so a click on one card toggles that card only and the state a renderer keeps by
// identity survives the rebuild.
func TestCustomMessageClickTogglesOnlyThatMessageAndIdentitySurvivesRebuilds(t *testing.T) {
	cards := &clickCards{}
	ext := extension.Extension{MessageRenderers: map[string]extension.MessageRenderer{"card": cards.render}}
	m := &InteractiveMode{newRunner: inproc.NewRunner([]extension.Extension{ext}, t.TempDir()), chatContainer: tui.NewContainer()}
	m.appendCustomMessage(CustomMessageEntry{CustomType: "card", Content: "first", Display: true})
	m.appendCustomMessage(CustomMessageEntry{CustomType: "card", Content: "second", Display: true})

	rows := func() []string {
		var out []string
		for _, line := range m.chatContainer.Render(80) {
			// The expansion notice the toggle prints below the transcript is not a card.
			if strings.TrimSpace(line) != "" && !strings.Contains(line, "Tool output") {
				out = append(out, strings.TrimSpace(line))
			}
		}
		return out
	}
	if got := strings.Join(rows(), "|"); got != "first:expanded=false|second:expanded=false" {
		t.Fatalf("rows = %q", got)
	}
	// Rows: spacer, first, spacer, second.
	if result := clickOn(m, 0); result != nil {
		t.Fatalf("a click on the spacer above a card was handled: %+v", result)
	}
	if result := clickOn(m, 1); result == nil || !result.Handled {
		t.Fatalf("click on the first card = %+v, want handled", result)
	}
	if got := strings.Join(rows(), "|"); got != "first:expanded=true|second:expanded=false" {
		t.Fatalf("after clicking the first card rows = %q", got)
	}
	if result := clickOn(m, 3); result == nil || !result.Handled {
		t.Fatalf("click on the second card = %+v, want handled", result)
	}
	if got := strings.Join(rows(), "|"); got != "first:expanded=true|second:expanded=true" {
		t.Fatalf("after clicking both cards rows = %q", got)
	}

	// Ctrl+O: a new global expansion rebuilds both cards; each renderer call carries its message's identity.
	calls := len(cards.identities)
	m.toggleAllTools()
	if got := strings.Join(rows(), "|"); got != "first:expanded=true|second:expanded=true" {
		t.Fatalf("after global expansion rows = %q", got)
	}
	rebuilt := cards.identities[calls:]
	if len(rebuilt) != 2 {
		t.Fatalf("global expansion ran the renderer %d times, want once per message", len(rebuilt))
	}
	first, second := cards.identities[0], cards.identities[1]
	if first == nil || second == nil || first == second {
		t.Fatalf("identities %p and %p, want two distinct non-nil ones", first, second)
	}
	if rebuilt[0] != first || rebuilt[1] != second {
		t.Fatalf("rebuild identities %p,%p differ from the first render's %p,%p", rebuilt[0], rebuilt[1], first, second)
	}
	// A second global change resets the local choice, as the oracle's state.global rule does, and a click then toggles from the reset value.
	m.toggleAllTools()
	if got := strings.Join(rows(), "|"); got != "first:expanded=false|second:expanded=false" {
		t.Fatalf("after collapsing globally rows = %q", got)
	}
	if result := clickOn(m, 1); result == nil || !result.Handled {
		t.Fatalf("click after the rebuild = %+v, want handled", result)
	}
	if got := strings.Join(rows(), "|"); got != "first:expanded=true|second:expanded=false" {
		t.Fatalf("after the post-rebuild click rows = %q", got)
	}
}

// A custom entry's component keeps one identity for its life, so the entry renderer's state survives its rebuilds, and another entry has its own.
func TestCustomEntryComponentHandsTheRendererOneIdentityAcrossRebuilds(t *testing.T) {
	var seen []*extension.RenderIdentity
	renderer := func(_ extension.CustomEntry, options extension.EntryRenderOptions, _ extension.Theme) extension.Component {
		seen = append(seen, options.Identity)
		return tui.NewText("entry")
	}
	first := NewCustomEntryComponent(CustomEntry{CustomType: "note", Data: "a"}, renderer, nil, 1)
	first.SetExpanded(true)
	first.Invalidate()
	second := NewCustomEntryComponent(CustomEntry{CustomType: "note", Data: "b"}, renderer, nil, 1)
	if len(seen) != 4 {
		t.Fatalf("renderer ran %d times, want 4", len(seen))
	}
	if seen[0] == nil || seen[0] != seen[1] || seen[1] != seen[2] {
		t.Fatalf("first entry's identities %p %p %p, want one non-nil identity", seen[0], seen[1], seen[2])
	}
	if seen[3] == nil || seen[3] == seen[0] {
		t.Fatalf("second entry identity %p, want its own", seen[3])
	}
	_ = second
}
