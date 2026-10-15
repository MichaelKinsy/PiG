package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// toggleEntryComponent mirrors the subprocess entry proxy's observable surface
// (Render + Invalidate + SetExpanded), so the in-proc test exercises the same
// expand-tracking path addCustomEntryToChat uses in production.
type toggleEntryComponent struct {
	content  string
	expanded bool
}

func (c *toggleEntryComponent) Render(int) []string {
	return []string{fmt.Sprintf("entry:%s:expanded=%t", c.content, c.expanded)}
}
func (c *toggleEntryComponent) Invalidate()        {}
func (c *toggleEntryComponent) SetExpanded(e bool) { c.expanded = e }

func TestCustomEntryUsesRegisteredEntryRenderer(t *testing.T) {
	var gotType string
	var gotExpanded bool
	ext := extension.Extension{
		EntryRenderers: map[string]extension.EntryRenderer{
			"note": func(entry extension.CustomEntry, options extension.EntryRenderOptions, _ extension.Theme) extension.Component {
				ce := entry
				// session-manager.ts:128 CustomEntry: the renderer receives the whole entry, not only its type and data.
				parent := "parent-1"
				if ce.Type != "custom" || ce.ID != "entry-1" || ce.ParentID == nil || *ce.ParentID != parent || ce.Timestamp != "2026-01-01T00:00:00.000Z" {
					t.Errorf("renderer got %+v, want the entry's type, id, parentId and timestamp", ce)
				}
				gotType = ce.CustomType
				gotExpanded = options.Expanded
				return &toggleEntryComponent{content: fmt.Sprintf("%v", ce.Data), expanded: options.Expanded}
			},
		},
	}
	m := &InteractiveMode{
		newRunner:     inproc.NewRunner([]extension.Extension{ext}, t.TempDir()),
		chatContainer: tui.NewContainer(),
		toolsExpanded: true,
	}
	parent := "parent-1"
	m.addCustomEntryToChat(CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom", ID: "entry-1", ParentID: &parent, Timestamp: "2026-01-01T00:00:00.000Z"}, CustomType: "note", Data: "hi"})

	// Host owns transcript spacing: a Spacer(1) blank line above the content.
	lines := m.chatContainer.Render(80)
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != "" || strings.TrimSpace(lines[1]) != "entry:hi:expanded=true" {
		t.Fatalf("rendered lines = %#v", lines)
	}
	if gotType != "note" || !gotExpanded {
		t.Fatalf("renderer input type=%q expanded=%t", gotType, gotExpanded)
	}

	// Ctrl+O toggles all tool output; the tracked entry proxy re-renders
	// collapsed, and the status reports the new state as upstream's does.
	m.toggleAllTools()
	lines = m.chatContainer.Render(80)
	if len(lines) != 4 || strings.TrimSpace(lines[1]) != "entry:hi:expanded=false" || !strings.Contains(lines[3], "Tool output: collapsed") {
		t.Fatalf("collapsed lines = %#v", lines)
	}
}

func TestCustomEntryNotRenderedWithoutRenderer(t *testing.T) {
	m := &InteractiveMode{
		newRunner:     inproc.NewRunner([]extension.Extension{}, t.TempDir()),
		chatContainer: tui.NewContainer(),
	}
	m.addCustomEntryToChat(CustomEntry{CustomType: "note", Data: "hi"})
	for _, l := range m.chatContainer.Render(80) {
		if strings.Contains(l, "entry:") {
			t.Fatalf("entry rendered without a registered renderer: %q", l)
		}
	}
}

func TestCustomEntryRendersBareComponent(t *testing.T) {
	// A renderer that returns a plain tui.Component (no SetExpanded) still renders,
	// just without expand tracking: mirrors appendCustomMessage's fallback.
	ext := extension.Extension{
		EntryRenderers: map[string]extension.EntryRenderer{
			"note": func(entry extension.CustomEntry, _ extension.EntryRenderOptions, _ extension.Theme) extension.Component {
				return tui.NewText("bare-entry")
			},
		},
	}
	m := &InteractiveMode{
		newRunner:     inproc.NewRunner([]extension.Extension{ext}, t.TempDir()),
		chatContainer: tui.NewContainer(),
	}
	m.addCustomEntryToChat(CustomEntry{CustomType: "note", Data: "x"})
	lines := m.chatContainer.Render(80)
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != "" || strings.TrimSpace(lines[1]) != "bare-entry" {
		t.Fatalf("bare-component render = %#v", lines)
	}
}

// interactive-mode.ts:3834-3840: an entry added while an assistant message streams goes before that message; with none streaming it appends.
func TestCustomEntryIsInsertedBeforeTheStreamingMessage(t *testing.T) {
	ext := extension.Extension{
		EntryRenderers: map[string]extension.EntryRenderer{
			"note": func(entry extension.CustomEntry, options extension.EntryRenderOptions, _ extension.Theme) extension.Component {
				return &toggleEntryComponent{content: "note", expanded: options.Expanded}
			},
		},
	}
	streaming := tui.NewText("streaming")
	m := &InteractiveMode{
		newRunner:     inproc.NewRunner([]extension.Extension{ext}, t.TempDir()),
		chatContainer: tui.NewContainer(tui.NewText("earlier"), streaming),
	}
	m.addCustomEntryToChat(CustomEntry{CustomType: "note"})
	if got := m.chatContainer.Children(); len(got) != 3 || got[1] != tui.Component(streaming) {
		t.Fatalf("without a streaming message the entry must append: %v", got)
	}
	m.chatContainer.Clear()
	m.chatContainer.Add(tui.NewText("earlier"))
	block := m.newAssistantMessageBlock()
	m.chatContainer.Add(block)
	m.evCurrentBlock = block
	m.addCustomEntryToChat(CustomEntry{CustomType: "note"})
	children := m.chatContainer.Children()
	if len(children) != 3 || children[2] != tui.Component(block) {
		t.Fatalf("entry must precede the streaming block: %v", children)
	}
}
