package sdk

import (
	"encoding/json"
	"reflect"
	"testing"
)

// An empty file is a present customPrompt, distinct from undefined in Pi's normalized options.
func TestSystemPromptOptionsEmptyCustomPrompt(t *testing.T) {
	var options SystemPromptOptions
	if err := json.Unmarshal([]byte(`{"customPrompt":""}`), &options); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !options.CustomPromptSet || string(got["customPrompt"]) != `""` {
		t.Fatalf("empty customPrompt lost: %s", data)
	}
}

// Pi system-prompt.ts:9-40 and skills.ts:74-82 expose registry guidelines and complete skill metadata to command contexts.
func TestSystemPromptOptionsRetainRegistryAndSkillMetadata(t *testing.T) {
	const wire = `{"customPrompt":"custom","forceSystemPrompt":"","toolGuidelines":{"read":["Read carefully."]},"skills":[{"name":"review","description":"Review code","filePath":"/review/SKILL.md","baseDir":"/review","sourceInfo":{"path":"/review/SKILL.md","scope":"project","source":"local","origin":"top-level"},"disableModelInvocation":false}]}`
	var options SystemPromptOptions
	if err := json.Unmarshal([]byte(wire), &options); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(wire), &want); err != nil {
		t.Fatal(err)
	}
	for key, value := range want {
		if !reflect.DeepEqual(got[key], value) {
			t.Errorf("%s = %#v, want %#v", key, got[key], value)
		}
	}
}
