package tools

// pi: packages/durable/src/tools/write.ts

// pi: packages/durable/src/tools/edit.ts

import (
	"encoding/json"
	"strings"
	"testing"
)

// A typed tool input is the Go form of the TypeScript `Static<typeof schema>` of write.ts, edit.ts and bash.ts: it encodes to exactly the arguments the model sends (path/content, path/edits[{oldText,newText}], command/timeout with timeout absent when unset) and the tool decodes it back.
func TestTypedToolInputsEncodeToThePinnedArgumentShapes(t *testing.T) {
	timeout := 2.5
	for _, tc := range []struct {
		name  string
		input any
		want  string
	}{
		{"write", WriteToolInput{Path: "a.txt", Content: "hi"}, `{"path":"a.txt","content":"hi"}`},
		{"edit", EditToolInput{Path: "a.txt", Edits: []Edit{{OldText: "a", NewText: "b"}}}, `{"path":"a.txt","edits":[{"oldText":"a","newText":"b"}]}`},
		{"bash without timeout", BashToolInput{Command: "ls"}, `{"command":"ls"}`},
		{"bash with timeout", BashToolInput{Command: "ls", Timeout: &timeout}, `{"command":"ls","timeout":2.5}`},
		{"powershell is the bash input", PowerShellToolInput{Command: "dir"}, `{"command":"dir"}`},
	} {
		got, err := json.Marshal(tc.input)
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: got %s (%v), want %s", tc.name, got, err, tc.want)
		}
	}
}

// write.ts and edit.ts execute from their typed arguments: the write tool writes WriteToolInput.Content to WriteToolInput.Path, and the edit tool applies EditToolInput.Edits and reports EditToolDetails (diff, patch, firstChangedLine).
func TestTypedWriteAndEditInputsDriveTheToolsAndEditReportsTypedDetails(t *testing.T) {
	executionEnv := createEnv(t)
	mustRun(t, CreateWriteTool(), WriteToolInput{Path: "typed.txt", Content: "alpha\nbeta\n"}, executionEnv)
	if got := readTextFile(t, executionEnv, "typed.txt"); got != "alpha\nbeta\n" {
		t.Fatalf("file = %q", got)
	}
	result := mustRun(t, CreateEditTool(), EditToolInput{Path: "typed.txt", Edits: []Edit{{OldText: "beta\n", NewText: "BETA\n"}}}, executionEnv)
	encoded, err := json.Marshal(result.Details)
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]any
	if err := json.Unmarshal(encoded, &members); err != nil || members["firstChangedLine"] != float64(2) || members["diff"] == nil || members["patch"] == nil {
		t.Fatalf("details members = %v (%v)", members, err)
	}
	var details EditToolDetails
	if err := json.Unmarshal(encoded, &details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details.Diff, "BETA") || !strings.Contains(details.Patch, "+BETA") || details.FirstChangedLine != 2 {
		t.Fatalf("details = %+v", details)
	}
	if got := readTextFile(t, executionEnv, "typed.txt"); got != "alpha\nBETA\n" {
		t.Fatalf("file = %q", got)
	}
}
