package codingagent

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// Init rebuilds the system prompt the caller built from the same options. An equal text keeps the caller's string: the prompt holds the project's context files, so a second copy of it was 65 KB of the live heap at an interactive start's first frame in this repository.
func TestRebuildToolSystemPromptKeepsTheCallersStringWhenTheTextIsEqual(t *testing.T) {
	options := extension.BuildSystemPromptOptions{
		Cwd:          "/work",
		ToolSnippets: map[string]string{"read": "Read file contents"},
		ContextFiles: []extension.SystemPromptContextFile{{Path: "/work/AGENTS.md", Content: strings.Repeat("rule ", 100)}},
	}
	normalized := extension.NormalizeBuildSystemPromptOptions(options)
	known := strings.Clone(prompts.BuildDefaultPrompt(prompts.FromExtensionOptions(normalized)))
	m := &InteractiveMode{opts: InteractiveModeOptions{SystemPrompt: known, SystemPromptOptions: options, CWD: "/work"}}
	m.rebuildToolSystemPrompt()
	got := m.currentSystemPrompt()
	if got != known {
		t.Fatalf("system prompt changed:\n%q\nwant\n%q", got, known)
	}
	if unsafe.StringData(got) != unsafe.StringData(known) { //nolint:gosec // G103: the test compares the data address of strings that must share
		t.Fatal("the interactive mode holds a second copy of the caller's equal system prompt")
	}
}
