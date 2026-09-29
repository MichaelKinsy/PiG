package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi agent-session.ts:1379-1394 retains the loader's custom prompt before exposing base options.
func TestSessionPromptOptionsRetainCallerCustomPromptAtConstruction(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	s, err := NewSession(h.session.services, SessionOptions{SystemPrompt: "CALLER CUSTOM", SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if got := s.GetSystemPromptOptions().CustomPrompt; got != "CALLER CUSTOM" {
		t.Fatalf("customPrompt = %q, want CALLER CUSTOM", got)
	}
}

// Pi system-prompt.ts:54-69 normalizes all collections, even for an empty registry and resource loader.
func TestSessionPromptOptionsEmptyCollectionsArePresent(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{resources: &SystemPromptResources{}})
	data, err := json.Marshal(h.session.GetSystemPromptOptions())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"selectedTools": "[]", "toolSnippets": "{}", "toolGuidelines": "{}",
		"promptGuidelines": "[]", "appendSystemPrompt": `""`, "sections": "{}", "contextFiles": "[]", "skills": "[]",
	} {
		if string(got[key]) != want {
			t.Errorf("%s = %s, want %s (options: %s)", key, got[key], want, data)
		}
	}
}

// Pi runner.ts:1317 normalizes into a new run object; system-prompt.ts:60-68 copies nested guideline arrays and resource records.
func TestSessionPromptOptionsRunDoesNotMutateBaseResources(t *testing.T) {
	resources := &SystemPromptResources{
		ContextFiles: []extension.SystemPromptContextFile{{Path: "/AGENTS.md", Content: "original"}},
		Skills:       []extension.SystemPromptSkill{{Name: "original"}},
	}
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		options := args[0].(extension.BeforeAgentStartEvent).SystemPromptOptions
		options.ToolSnippets["read"] = "changed"
		options.ToolGuidelines["read"][0] = "changed"
		options.ContextFiles[0].Content = "changed"
		options.Skills[0].Name = "changed"
		return nil, nil
	}}}}
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true, resources: resources, extension: ext})
	before, err := json.Marshal(h.session.GetSystemPromptOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.session.PreparePrompt(context.Background(), BuildUserContent("hello", nil)); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(h.session.GetSystemPromptOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("run mutated base options:\nbefore=%s\nafter=%s", before, after)
	}
}
