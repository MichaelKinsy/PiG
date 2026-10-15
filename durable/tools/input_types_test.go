package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// asArgs converts a typed tool input to the decoded JSON a model call supplies.
func asArgs(t *testing.T, input any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(input)
	mustDo(t, err)
	var args map[string]any
	mustDo(t, json.Unmarshal(raw, &args))
	return args
}

// TestToolInputTypesAreTheToolArguments: the typed inputs of the write, edit and bash/powershell tools marshal to exactly the
// arguments their schemas name (tools/write.ts, edit.ts, bash.ts), and a call built from one runs the tool.
func TestToolInputTypesAreTheToolArguments(t *testing.T) {
	executionEnv := createEnv(t)

	mustRun(t, CreateWriteTool(), asArgs(t, WriteToolInput{Path: "a.txt", Content: "one\ntwo\n"}), executionEnv)
	if got, err := executionEnv.ReadTextFile(background, "a.txt"); err != nil || got != "one\ntwo\n" {
		t.Fatalf("write left %q, %v", got, err)
	}

	edit := EditToolInput{Path: "a.txt", Edits: []Edit{{OldText: "two", NewText: "2"}}}
	if raw, _ := json.Marshal(edit); string(raw) != `{"path":"a.txt","edits":[{"oldText":"two","newText":"2"}]}` {
		t.Errorf("edit input = %s", raw)
	}
	result := mustRun(t, CreateEditTool(), asArgs(t, edit), executionEnv)
	var details EditToolDetails
	raw, err := json.Marshal(result.Details)
	mustDo(t, err)
	mustDo(t, json.Unmarshal(raw, &details))
	if details.FirstChangedLine != 2 || !strings.Contains(details.Diff, "+2 2") || !strings.Contains(details.Patch, "+2") {
		t.Errorf("edit details = %+v", details)
	}
	if shape, _ := json.Marshal(EditToolDetails{Diff: "d", Patch: "p"}); string(shape) != `{"diff":"d","patch":"p"}` {
		t.Errorf("details without a changed line = %s", shape)
	}

	timeout := 5.0
	bash := BashToolInput{Command: "echo hi", Timeout: &timeout}
	if raw, _ := json.Marshal(bash); string(raw) != `{"command":"echo hi","timeout":5}` {
		t.Errorf("bash input = %s", raw)
	}
	if raw, _ := json.Marshal(BashToolInput{Command: "x"}); string(raw) != `{"command":"x"}` {
		t.Errorf("bash input without timeout = %s", raw)
	}
	if got := mustRun(t, CreateBashTool(nil), asArgs(t, bash), executionEnv); !strings.Contains(strings.Join(got.output, ""), "hi") {
		t.Errorf("bash streamed %q", got.output)
	}
	if command := func(input PowerShellToolInput) string { return input.Command }(bash); command != "echo hi" {
		t.Errorf("powershell input command = %q", command)
	}
}
