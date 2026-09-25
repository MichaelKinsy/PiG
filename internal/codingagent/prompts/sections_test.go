package prompts

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ported from system-prompt-updates.test.ts: patches replace only changed
// sections, retain order, and use explicit null for removed sections.
func TestDiffSystemPromptSections(t *testing.T) {
	previous := ai.OrderedSections{{Name: "preamble", Value: new("You are A.")}, {Name: "plan_mode", Value: new("<plan_mode>\nPlan only.\n</plan_mode>")}}
	current := ai.OrderedSections{{Name: "preamble", Value: new("You are A.")}, {Name: "plan_mode", Value: new("<plan_mode>\nImplementation allowed.\n</plan_mode>")}}
	if got := DiffSystemPromptSections(previous, current); !reflect.DeepEqual(got, current[1:]) {
		t.Fatalf("replacement %#v", got)
	}
	if got := DiffSystemPromptSections(previous, previous); got != nil {
		t.Fatalf("unchanged patch %#v", got)
	}
	if got := DiffSystemPromptSections(previous, current[:1]); !reflect.DeepEqual(got, ai.OrderedSections{{Name: "plan_mode"}}) {
		t.Fatalf("removal %#v", got)
	}
	current[0].Value = new("You are B.")
	current[1].Value = previous[1].Value
	if got := DiffSystemPromptSections(previous, current); !reflect.DeepEqual(got, current[:1]) {
		t.Fatalf("preamble patch %#v", got)
	}
}

func TestBuildSystemPromptSectionsRetainsSourceOrderAndEmptyContent(t *testing.T) {
	sections := BuildSystemPromptSections(Options{Cwd: "/tmp", Tools: []string{"read"}, ToolHints: DefaultToolSnippets()})
	var names []string
	for _, section := range sections {
		names = append(names, section.Name)
	}
	if want := []string{"preamble", "tools", "rules", "docs", "cwd"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("keys %v want%v", names, want)
	}
	custom := BuildSystemPromptSections(Options{Cwd: "/tmp", AppendMode: "replace", CustomPrompt: "You are A.", AppendSystemPrompt: "Extra."})
	want := ai.OrderedSections{{Name: "preamble", Value: new("You are A.")}, {Name: "addendum", Value: new("<addendum>\nExtra.\n</addendum>")}, {Name: "cwd", Value: new("<cwd>\n/tmp\n</cwd>")}}
	if !reflect.DeepEqual(custom, want) {
		t.Fatalf("custom sections %#v", custom)
	}
}

func TestStructuredAddendumRetainsAgentAndCLIInstructions(t *testing.T) {
	sections := BuildSystemPromptSections(Options{CustomPrompt: "Agent rule.", AppendSystemPrompt: "CLI rule."})
	var count int
	for _, section := range sections {
		if section.Name == "addendum" {
			count++
			if section.Value == nil || *section.Value != "<addendum>\nAgent rule.\n\nCLI rule.\n</addendum>" {
				t.Fatalf("addendum = %#v", section)
			}
		}
	}
	if count != 1 {
		t.Fatalf("addendum count = %d", count)
	}
}
