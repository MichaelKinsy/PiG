package subprocess

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pi: runner.ts emitBeforeAgentStart hands every before_agent_start handler the one systemPromptOptions object, so a member a handler leaves alone is
// the string it was for the next handler and for the prompt the options render. An SDK extension sends the options back as JSON, where a lone UTF-16
// surrogate is its escape: the host writes the unit, not U+FFFD, into the options of the run.
func TestPromptOptionEditsKeepALoneSurrogate(t *testing.T) {
	const unit = "\xed\xa0\xbd"
	var target extension.BuildSystemPromptOptions
	if err := applyPromptOptionEdits(&target, []byte(`{"customPrompt":"CP[\ud83d]","appendSystemPrompt":"AP[\ud83d]","promptGuidelines":["PG[\ud83d]"],"cwd":"/work","selectedTools":["read"],"sections":{}}`)); err != nil {
		t.Fatal(err)
	}
	if want := "CP[" + unit + "]"; target.CustomPrompt != want {
		t.Errorf("customPrompt = %q, want %q", target.CustomPrompt, want)
	}
	if want := "AP[" + unit + "]"; target.AppendSystemPrompt != want {
		t.Errorf("appendSystemPrompt = %q, want %q", target.AppendSystemPrompt, want)
	}
	if want := "PG[" + unit + "]"; len(target.PromptGuidelines) != 1 || target.PromptGuidelines[0] != want {
		t.Errorf("promptGuidelines = %q, want [%q]", target.PromptGuidelines, want)
	}
}
