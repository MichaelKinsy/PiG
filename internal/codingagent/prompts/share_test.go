// SPDX-License-Identifier: MIT

package prompts

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/ai"
)

// The text is what ai.GetCurrentSystemPrompt renders for the sections, every section keeps its bytes, and every non-empty section value lies inside the text:
// the prompt is one copy, not the sections and a joined text.
func TestRenderSharingSectionsHoldsThePromptOnce(t *testing.T) {
	big := strings.Repeat("project rule line\n", 4000)
	options := Options{
		Cwd: "/work", Tools: []string{"read", "bash"}, ToolHints: map[string]string{"read": "Read files", "bash": "Run commands"},
		ContextFiles: []struct{ Path, Content string }{{Path: "/work/AGENTS.md", Content: big}}, AppendSystemPrompt: "tail",
	}
	sections := BuildSystemPromptSections(options)
	want := ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: BuildSystemPromptSections(options)}})
	before := make([]string, len(sections))
	for i, section := range sections {
		before[i] = *section.Value
	}
	text := RenderSharingSections(sections)
	if text != want {
		t.Fatalf("rendered text differs from ai.GetCurrentSystemPrompt: %d bytes, want %d", len(text), len(want))
	}
	if got := ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: sections}}); got != want {
		t.Fatalf("the sections render %d bytes after sharing, want %d", len(got), len(want))
	}
	start, end := uintptr(unsafe.Pointer(unsafe.StringData(text))), uintptr(unsafe.Pointer(unsafe.StringData(text)))+uintptr(len(text))
	for i, section := range sections {
		if *section.Value != before[i] {
			t.Errorf("section %q changed its value", section.Name)
		}
		if p := uintptr(unsafe.Pointer(unsafe.StringData(*section.Value))); p < start || p >= end {
			t.Errorf("section %q is not backed by the rendered text", section.Name)
		}
	}
}

// Sections with no value or an empty one add nothing to the text, and an empty list renders as empty text.
func TestRenderSharingSectionsSkipsEmptyValues(t *testing.T) {
	if got := RenderSharingSections(nil); got != "" {
		t.Fatalf("no sections rendered %q", got)
	}
	sections := ai.OrderedSections{{Name: "a", Value: new("one")}, {Name: "gone"}, {Name: "empty", Value: new("")}, {Name: "b", Value: new("two")}}
	if got := RenderSharingSections(sections); got != "one\n\ntwo" {
		t.Fatalf("rendered %q, want %q", got, "one\n\ntwo")
	}
	if *sections[0].Value != "one" || *sections[3].Value != "two" || *sections[2].Value != "" {
		t.Fatalf("values changed: %q %q %q", *sections[0].Value, *sections[3].Value, *sections[2].Value)
	}
}
