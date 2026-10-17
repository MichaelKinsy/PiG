package inproc_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// runner.ts:1462 returns the shared options object after every handler ran. A handler that clears the shared options' sections (the Go
// Sections pointer, Pi's `sections = undefined`) must leave the combined options with a usable, empty section list: the Session
// dereferences it to build the prompt (runner.go EmitBeforeAgentStart re-normalizes a nil collection).
// mutation-checked: dropping the nil re-normalization (`Sections == nil` guard) panics on the dereference.
func TestEmitBeforeAgentStartAHandlerThatClearsTheSectionsLeavesAnEmptyList(t *testing.T) {
	exts := []extension.Extension{
		extWithBeforeAgentStartHandler("/ext/a", func(_ extension.BeforeAgentStartEvent, ctx context.Context) *extension.BeforeAgentStartEventResult {
			extension.BeforeAgentStartOptions(ctx).Sections = nil
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	keep := "keep"
	combined, err := r.EmitBeforeAgentStart(context.Background(), extension.UserPromptTrigger("p", nil), extension.BuildSystemPromptOptions{CustomPrompt: "base", Sections: &ai.OrderedSections{{Name: "notes", Value: &keep}}})
	if err != nil {
		t.Fatal(err)
	}
	// Clearing a section the options had is an edit, so the combined result carries the options (runner.ts:1470 always does).
	if combined == nil || combined.SystemPromptOptions == nil {
		t.Fatalf("combined = %+v, want the edited options", combined)
	}
	if combined.SystemPromptOptions.Sections == nil {
		t.Fatal("combined options carry a nil Sections")
	}
	if got := len(*combined.SystemPromptOptions.Sections); got != 0 {
		t.Fatalf("sections = %d, want none", got)
	}
}
