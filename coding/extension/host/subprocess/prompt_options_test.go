package subprocess

import (
	"encoding/json"
	"slices"
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

// A before_agent_start handler receives `{...trigger, type, get systemPrompt, systemPromptOptions}` (runner.ts:1446-1452), whose accessor V8 defines after the data members of a literal that spreads: Object.keys under real Pi (Node 24) is messageType, prompt, images, type, systemPromptOptions, systemPrompt whose options follow normalizeBuildSystemPromptOptions (system-prompt.ts:61-75). Re-encoding the event to carry the retained selectedTools sorted every member alphabetically, so Object.keys(event) read messageType, prompt, systemPrompt, systemPromptOptions, type in PiG.
func TestPromptOptionsEventArgsKeepPiMemberOrder(t *testing.T) {
	options := &extension.BuildSystemPromptOptions{Cwd: "/work", SelectedTools: []string{"read"}}
	systemPrompt := extension.NewSystemPrompt(func() (string, error) { return "sys", nil })
	event := extension.BeforeAgentStartEvent{Type: "before_agent_start", MessageType: extension.BeforeAgentStartUser, Prompt: "hi", SystemPrompt: systemPrompt, SystemPromptOptions: *options}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	ctx := extension.WithBeforeAgentStartOptions(t.Context(), options)
	got, err := promptOptionsEventArgs(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	keys := func(raw json.RawMessage) []string {
		members, err := objectMembers(raw)
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, len(members))
		for i, member := range members {
			names[i] = member.key
		}
		return names
	}
	if want, have := keys(raw), keys(got); !slices.Equal(want, have) {
		t.Fatalf("event members = %v, want the order the event marshals in, %v", have, want)
	}
	var optionsOf = func(raw json.RawMessage) []string {
		for _, member := range mustMembers(t, raw) {
			if member.key == "systemPromptOptions" {
				return keys(member.value)
			}
		}
		return nil
	}
	if want, have := optionsOf(raw), optionsOf(got); !slices.Equal(want, have) {
		t.Fatalf("options members = %v, want %v", have, want)
	}
	if want := []string{"messageType", "prompt", "type", "systemPromptOptions", "systemPrompt"}; !slices.Equal(keys(got), want) {
		t.Fatalf("event members = %v, want %v", keys(got), want)
	}
}

func mustMembers(t *testing.T, raw json.RawMessage) []objectMember {
	t.Helper()
	members, err := objectMembers(raw)
	if err != nil {
		t.Fatal(err)
	}
	return members
}
