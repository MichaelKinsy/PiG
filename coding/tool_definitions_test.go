package coding

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// .upstream/current/packages/coding-agent/src/core/tools/write.ts:44-57 createWriteToolDefinition(cwd, options): a ToolDefinition named and labelled "write" with the write description, the write guidelines, the path/content schema and strict constrained sampling, whose execute writes through options.operations at the cwd-resolved path (write.ts:65-82).
func TestCreateWriteToolDefinitionIsPiToolDefinition(t *testing.T) {
	var wrote, made []string
	cwd := t.TempDir()
	definition := CreateWriteToolDefinition(cwd, &tools.WriteToolOptions{Operations: &tools.WriteOperations{
		WriteFile: func(path, content string) error { wrote = append(wrote, path+"="+content); return nil },
		Mkdir:     func(dir string) error { made = append(made, dir); return nil },
	}})
	if definition.Name != "write" || definition.Label != "write" || definition.Description != "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories." {
		t.Fatalf("identity = %q %q %q", definition.Name, definition.Label, definition.Description)
	}
	if len(definition.PromptGuidelines) == 0 || definition.PromptSnippet == "" {
		t.Fatalf("write prompt contribution missing: %q %v", definition.PromptSnippet, definition.PromptGuidelines)
	}
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(definition.Parameters, &schema); err != nil || len(schema.Properties) != 2 || len(schema.Required) != 2 {
		t.Fatalf("parameters = %s (%v)", definition.Parameters, err)
	}
	var sampling struct{ Type, Strict string }
	if err := json.Unmarshal(definition.ConstrainedSampling, &sampling); err != nil || sampling.Type != "json_schema" || sampling.Strict != "prefer" {
		t.Fatalf("constrainedSampling = %s (%v)", definition.ConstrainedSampling, err)
	}
	if definition.Execute == nil {
		t.Fatal("a definition without execute")
	}
	if _, err := definition.Execute(context.Background(), "call-1", json.RawMessage(`{"path":"a/b.txt","content":"hi"}`), nil); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cwd, "a", "b.txt"); len(wrote) != 1 || wrote[0] != want+"=hi" || len(made) != 1 || made[0] != filepath.Dir(want) {
		t.Fatalf("operations saw wrote=%v made=%v", wrote, made)
	}
}

// create<Tool>ToolDefinition exists for the built-in tools other than bash (tools.CreateBashToolDefinition) (edit.ts:143, find.ts:70, grep.ts:70, ls.ts:54, powershell.ts:49, read.ts:86, write.ts:44): each returns the definition named for its tool, with an execute.
func TestCreateToolDefinitionsNameTheirTools(t *testing.T) {
	cwd := t.TempDir()
	for name, definition := range map[string]string{
		"edit":       CreateEditToolDefinition(cwd, nil).Name,
		"find":       CreateFindToolDefinition(cwd, nil).Name,
		"grep":       CreateGrepToolDefinition(cwd, nil).Name,
		"ls":         CreateLsToolDefinition(cwd, nil).Name,
		"powershell": CreatePowerShellToolDefinition(cwd, nil).Name,
		"read":       CreateReadToolDefinition(cwd, nil).Name,
		"write":      CreateWriteToolDefinition(cwd, nil).Name,
	} {
		if definition != name {
			t.Errorf("create%sToolDefinition returned %q", name, definition)
		}
	}
}
