package tools

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// tools/index.ts:107-116 ToolsOptions and createAllTools: each tool receives its own options, in registry order, and CreateTool builds one named tool.
// Pi: packages/coding-agent/src/core/tools/bash.ts:218 (BashToolOptions.commandPrefix); packages/coding-agent/src/core/tools/edit.ts:99 (EditToolOptions.operations); packages/coding-agent/src/core/tools/ls.ts:50 (LsToolOptions.operations); packages/coding-agent/src/core/tools/read.ts:65 (ReadToolOptions.autoResizeImages); packages/coding-agent/src/core/tools/read.ts:68 (ReadToolOptions.operations); packages/coding-agent/src/core/tools/write.ts:29 (WriteOperations.writeFile); packages/coding-agent/src/core/tools/write.ts:31 (WriteOperations.mkdir); packages/coding-agent/src/core/tools/write.ts:40 (WriteToolOptions.operations).
func TestToolsOptionsReachEachTool(t *testing.T) {
	autoResize := false

	options := &ToolsOptions{
		Read:  &ReadToolOptions{AutoResizeImages: &autoResize, Operations: &ReadOperations{Access: func(string) error { return nil }, ReadFile: func(string) ([]byte, error) { return []byte("from ops"), nil }}},
		Bash:  &BashToolOptions{CommandPrefix: "PREFIX=1"},
		Write: &WriteToolOptions{Operations: &WriteOperations{WriteFile: func(path, _ string) error { return nil }, Mkdir: func(string) error { return nil }}},
		Edit:  &EditToolOptions{Operations: &EditOperations{ReadFile: func(string) ([]byte, error) { return []byte("a"), nil }, WriteFile: func(path, _ string) error { return nil }, Access: func(string) error { return nil }}},
		Ls:    &LsToolOptions{Operations: &LsOperations{}},
	}
	all := CreateAllTools("/work", options)
	var names []string
	for _, tool := range all {
		names = append(names, tool.Name())
	}
	if want := []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls"}; !slices.Equal(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
	read := all[0].(*ReadTool)
	if read.AutoResizeImages != &autoResize || read.Operations == nil || read.CWD != "/work" {
		t.Fatalf("read options not applied: %+v", read)
	}
	if bash := all[1].(*BashTool); bash.CommandPrefix != "PREFIX=1" {
		t.Fatalf("bash options not applied: %+v", bash)
	}
	if all[7].(*LsTool).Operations != options.Ls.Operations {
		t.Fatal("ls options not applied")
	}
	result, err := all[0].Execute(t.Context(), "id", json.RawMessage(`{"path":"x.txt"}`), nil)
	if err != nil || resultText(t, result) != "from ops" {
		t.Fatalf("read through options = %+v, %v", result, err)
	}
	// edit and write share one mutation queue, as in createAllTools' shared queue.
	if all[3].(*EditTool).Queue != all[4].(*WriteTool).Queue || all[3].(*EditTool).Queue == nil {
		t.Fatal("edit and write must share a file mutation queue")
	}

	coding := CreateCodingTools("/work", nil)
	if len(coding) != 7 {
		t.Fatalf("coding tools = %d, want 7", len(coding))
	}
	readOnly := CreateReadOnlyTools("/work", nil)
	var readOnlyNames []string
	for _, tool := range readOnly {
		readOnlyNames = append(readOnlyNames, tool.Name())
	}
	if want := []string{"read", "grep", "find", "ls"}; !slices.Equal(readOnlyNames, want) {
		t.Fatalf("read-only = %v, want %v", readOnlyNames, want)
	}
	for _, name := range builtinToolNames {
		tool, err := CreateTool(name, "/work", nil)
		if err != nil || tool.Name() != name {
			t.Fatalf("CreateTool(%q) = %v, %v", name, tool, err)
		}
	}
	if _, err := CreateTool("nope", "/work", nil); err == nil {
		t.Fatal("an unknown tool name must fail")
	}
	var _ agent.AgentTool = CreateReadTool("/work", nil)
}
