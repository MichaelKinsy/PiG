package extension

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 0.87.1 system-prompt.ts:54-69 copies the authored collections and supplies defaults only for absent ones.
func TestNormalizeBuildSystemPromptOptions(t *testing.T) {
	for _, selected := range [][]string{nil, {}, {"custom"}} {
		opts := NormalizeBuildSystemPromptOptions(BuildSystemPromptOptions{Cwd: "/work", SelectedTools: selected})
		wantTools := selected
		if selected == nil {
			wantTools = []string{"read", "bash", "edit", "write"}
		}
		if !reflect.DeepEqual(opts.SelectedTools, wantTools) {
			t.Errorf("selectedTools = %v, want %v", opts.SelectedTools, wantTools)
		}
		data, err := json.Marshal(opts)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{"toolSnippets": "{}", "toolGuidelines": "{}", "promptGuidelines": "[]", "appendSystemPrompt": `""`, "sections": "{}", "contextFiles": "[]", "skills": "[]"} {
			if string(got[key]) != want {
				t.Errorf("%s = %s, want %s", key, got[key], want)
			}
		}
		for _, key := range []string{"customPrompt", "forceSystemPrompt"} {
			if _, present := got[key]; present {
				t.Errorf("absent %s serialized in %s", key, data)
			}
		}
	}
}

// A present empty customPrompt survives normalization and the wire without making an absent customPrompt present.
func TestNormalizeBuildSystemPromptOptionsEmptyCustomPrompt(t *testing.T) {
	var input BuildSystemPromptOptions
	if err := json.Unmarshal([]byte(`{"customPrompt":""}`), &input); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(NormalizeBuildSystemPromptOptions(input))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !input.CustomPromptSet || string(got["customPrompt"]) != `""` {
		t.Fatalf("empty customPrompt lost: %s", data)
	}
}

func BenchmarkNormalizeBuildSystemPromptOptions(b *testing.B) {
	input := BuildSystemPromptOptions{
		Cwd: "/work", SelectedTools: []string{"read", "bash", "edit", "write"},
		ToolSnippets:   map[string]string{"read": "Read file contents"},
		ToolGuidelines: map[string][]string{"read": {"Use read."}},
		ContextFiles:   []SystemPromptContextFile{{Path: "/work/AGENTS.md", Content: strings.Repeat("project instructions\n", 4096)}},
		Skills:         []SystemPromptSkill{{Name: "review", BaseDir: "/skills/review", Description: "Review code"}},
		Sections:       &ai.OrderedSections{{Name: "review", Value: new("Review first.")}},
	}
	b.ReportAllocs()
	for b.Loop() {
		got := NormalizeBuildSystemPromptOptions(input)
		if len(got.ContextFiles) != len(input.ContextFiles) {
			b.Fatal("lost context files")
		}
	}
}
