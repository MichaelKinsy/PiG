package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The host sends the whole options object, including customPrompt and forceSystemPrompt whenever they are defined (BuildSystemPromptOptions.MarshalJSON), and the SDK returns the handler's whole object. runner.ts:1411-1462 shares that object between handlers, so a handler's `delete options.forceSystemPrompt` or `delete options.customPrompt` leaves the field undefined for later handlers and the run (agent-session.ts:1724), while a collection the response omits keeps its value.
func TestApplyPromptOptionEditsTreatsAnOmittedOptionalFieldAsDeleted(t *testing.T) {
	base := func() *extension.BuildSystemPromptOptions {
		return &extension.BuildSystemPromptOptions{CustomPrompt: "base custom", CustomPromptSet: true, ForceSystemPrompt: new("base forced"), AppendSystemPrompt: "base append", ToolSnippets: map[string]string{"read": "base read"}}
	}
	encoded, err := json.Marshal(base())
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(encoded, &request); err != nil {
		t.Fatal(err)
	}
	if request["customPrompt"] != "base custom" || request["forceSystemPrompt"] != "base forced" {
		t.Fatalf("the request omits a defined optional field: %s", encoded)
	}

	deleted := base()
	if err := applyPromptOptionEdits(deleted, json.RawMessage(`{"appendSystemPrompt":"edited append","cwd":"/edited"}`)); err != nil {
		t.Fatal(err)
	}
	if deleted.CustomPromptSet || deleted.CustomPrompt != "" || deleted.ForceSystemPrompt != nil {
		t.Fatalf("omitted optional fields survived: %+v", deleted)
	}
	if deleted.AppendSystemPrompt != "edited append" || deleted.Cwd != "/edited" || deleted.ToolSnippets["read"] != "base read" {
		t.Fatalf("edits = %+v", deleted)
	}

	set := base()
	if err := applyPromptOptionEdits(set, json.RawMessage(`{"customPrompt":"","forceSystemPrompt":"edited forced"}`)); err != nil {
		t.Fatal(err)
	}
	if !set.CustomPromptSet || set.CustomPrompt != "" || set.ForceSystemPrompt == nil || *set.ForceSystemPrompt != "edited forced" {
		t.Fatalf("set fields = %+v", set)
	}

	kept := base()
	if err := applyPromptOptionEdits(kept, json.RawMessage(`null`)); err != nil {
		t.Fatal(err)
	}
	if kept.CustomPrompt != "base custom" || kept.ForceSystemPrompt == nil || *kept.ForceSystemPrompt != "base forced" {
		t.Fatalf("an absent object changed the options: %+v", kept)
	}
}
