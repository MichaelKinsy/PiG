package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func runPromptText(sections ai.OrderedSections) string {
	return ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Content: ai.SystemText(""), Sections: sections}})
}

// agent-session.ts:1674-1677: the tool list of the run's prompt omits hidden tools, also when a handler edited their snippet.
func TestBeforeAgentStartRunPromptSectionsOmitHiddenSnippets(t *testing.T) {
	run := BeforeAgentStartRun{Options: extension.BuildSystemPromptOptions{
		ToolSnippets: map[string]string{"read": "edited read snippet", "codemode": "run code"},
	}}
	got := runPromptText(run.BaseSections([]string{"read", "codemode"}, map[string]struct{}{"read": {}}))
	if strings.Contains(got, "\n- read: ") || !strings.Contains(got, "\n- codemode: run code") {
		t.Fatalf("prompt:\n%s", got)
	}
	if run.Options.ToolSnippets["read"] != "edited read snippet" {
		t.Fatalf("the filter edited the run's options: %v", run.Options.ToolSnippets)
	}
	got = runPromptText(run.BaseSections([]string{"read", "codemode"}, nil))
	if !strings.Contains(got, "\n- read: edited read snippet") {
		t.Fatalf("an unhidden snippet is missing:\n%s", got)
	}
}

// agent-session.ts:697-709: a later turn lists tools registered during the run and lets the run's edits win.
func TestBeforeAgentStartRunNextTurnOptionsMergeBaseUnderRunEdits(t *testing.T) {
	run := BeforeAgentStartRun{Options: extension.BuildSystemPromptOptions{
		ToolSnippets:   map[string]string{"read": "edited"},
		ToolGuidelines: map[string][]string{"read": {"edited guideline"}},
	}}
	base := extension.BuildSystemPromptOptions{
		ToolSnippets:   map[string]string{"read": "base", "late": "registered mid-run"},
		ToolGuidelines: map[string][]string{"read": {"base guideline"}, "late": {"late guideline"}},
	}
	got := run.NextTurnOptions(base)
	if got.ToolSnippets["read"] != "edited" || got.ToolSnippets["late"] != "registered mid-run" {
		t.Errorf("snippets = %v", got.ToolSnippets)
	}
	if g := got.ToolGuidelines; g["read"][0] != "edited guideline" || g["late"][0] != "late guideline" {
		t.Errorf("guidelines = %v", g)
	}
	if base.ToolSnippets["read"] != "base" || run.Options.ToolSnippets["late"] != "" {
		t.Error("the merge mutated an input")
	}
}
