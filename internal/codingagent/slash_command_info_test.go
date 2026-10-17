package codingagent

import (
	"encoding/json"
	"testing"
)

// core/slash-commands.ts SlashCommandInfo: {name, description?, source, sourceInfo}, the entry of getCommands() and RPC get_commands. The wire keys and the omitted description are fixed by the type.
// Pi: packages/coding-agent/src/core/slash-commands.ts:8 (SlashCommandInfo.description).
func TestSlashCommandInfoWireShape(t *testing.T) {
	info := SlashCommandInfo{Name: "plan", Source: "prompt", SourceInfo: CreateSyntheticSourceInfo("/p/plan.md", SyntheticSourceInfoOptions{Source: "local", BaseDir: "/p"})}
	got, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"plan","source":"prompt","sourceInfo":{"path":"/p/plan.md","source":"local","scope":"temporary","origin":"top-level","baseDir":"/p"}}`
	if string(got) != want {
		t.Fatalf("SlashCommandInfo = %s, want %s", got, want)
	}
	info.Description = "Plan it"
	got, _ = json.Marshal(info)
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil || decoded["description"] != "Plan it" {
		t.Fatalf("description = %v (%v)", decoded["description"], err)
	}
}
