package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// custom-entry.ts:25-31,53-63: setExpanded re-runs the renderer with the new expansion state, hasContent reports whether the
// renderer produced a component, and the output follows one Spacer(1).
func TestCustomEntryComponentRerendersOnExpandAndReportsContent(t *testing.T) {
	var calls []bool
	renderer := func(_ extension.CustomEntry, options extension.EntryRenderOptions, _ extension.Theme) extension.Component {
		calls = append(calls, options.Expanded)
		return tui.NewText(fmt.Sprintf("expanded=%t", options.Expanded))
	}
	component := NewCustomEntryComponent(CustomEntry{CustomType: "note"}, renderer, 1)
	if !component.HasContent() || len(component.Children()) != 2 {
		t.Fatalf("content %t, children %d", component.HasContent(), len(component.Children()))
	}
	component.SetExpanded(true)
	component.SetExpanded(true)
	if len(calls) != 2 || calls[0] || !calls[1] {
		t.Fatalf("renderer calls = %v, want [false true]", calls)
	}
	lines := component.Render(30)
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != "" || !strings.Contains(widthx.StripAnsi(lines[1]), "expanded=true") {
		t.Fatalf("render = %q", lines)
	}

	none := NewCustomEntryComponent(CustomEntry{CustomType: "note"}, func(extension.CustomEntry, extension.EntryRenderOptions, extension.Theme) extension.Component {
		return nil
	}, 1)
	if none.HasContent() || len(none.Children()) != 0 {
		t.Fatalf("a renderer returning nothing must leave the component empty")
	}
}

// custom-entry.ts:46-52: a throwing renderer becomes an error box naming the custom type.
func TestCustomEntryComponentShowsAnErrorBoxWhenTheRendererFails(t *testing.T) {
	component := NewCustomEntryComponent(CustomEntry{CustomType: "note"}, func(extension.CustomEntry, extension.EntryRenderOptions, extension.Theme) extension.Component {
		panic(fmt.Errorf("boom"))
	}, 1)
	if !component.HasContent() {
		t.Fatal("the error box counts as content")
	}
	if got := widthx.StripAnsi(strings.Join(component.Render(60), "\n")); !strings.Contains(got, "[note] renderer failed: boom") {
		t.Fatalf("render = %q", got)
	}
}
