package coding

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// createXToolDefinition (core/tools/{read,bash,edit,write,grep,find,ls,powershell}.ts) returns the ToolDefinition of each built-in tool. The data members of
// every Go definition equal those of the definition Pi's installed build returns: name, label, description, prompt snippet and guidelines, parameters,
// output schema and constrained sampling.
func TestCreateBuiltInToolDefinitionsMatchPi(t *testing.T) {
	cwd := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "node", "testdata/tool_definitions.mjs", pigversion.UpstreamVersion, cwd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want map[string]map[string]any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	creators := map[string]func(string) (extension.ToolDefinition, error){
		"Read":  func(cwd string) (extension.ToolDefinition, error) { return CreateReadToolDefinition(cwd, nil), nil },
		"Bash":  func(cwd string) (extension.ToolDefinition, error) { return tools.CreateBashToolDefinition(cwd, nil) },
		"Edit":  func(cwd string) (extension.ToolDefinition, error) { return CreateEditToolDefinition(cwd, nil), nil },
		"Write": func(cwd string) (extension.ToolDefinition, error) { return CreateWriteToolDefinition(cwd, nil), nil },
		"Grep":  func(cwd string) (extension.ToolDefinition, error) { return CreateGrepToolDefinition(cwd, nil), nil },
		"Find":  func(cwd string) (extension.ToolDefinition, error) { return CreateFindToolDefinition(cwd, nil), nil },
		"Ls":    func(cwd string) (extension.ToolDefinition, error) { return CreateLsToolDefinition(cwd, nil), nil },
		"PowerShell": func(cwd string) (extension.ToolDefinition, error) {
			return CreatePowerShellToolDefinition(cwd, nil), nil
		},
	}
	if len(creators) != len(want) {
		t.Fatalf("%d creators, Pi returns %d definitions", len(creators), len(want))
	}
	for name, create := range creators {
		t.Run(name, func(t *testing.T) {
			definition, err := create(cwd)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(definition)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if definition.Execute == nil {
				t.Error("definition has no execute")
			}
			if !reflect.DeepEqual(got, want[name]) {
				for key := range got {
					if !reflect.DeepEqual(got[key], want[name][key]) {
						g, _ := json.Marshal(got[key])
						w, _ := json.Marshal(want[name][key])
						t.Errorf("%s differs from Pi's\n got: %s\nwant: %s", key, g, w)
					}
				}
				for key := range want[name] {
					if _, ok := got[key]; !ok {
						t.Errorf("Pi's definition has %s, Go's has not", key)
					}
				}
			}
		})
	}
}

// A session registers the built-in definitions as createXToolDefinition returns them (agent-session.ts registers createAllToolDefinitions): getToolDefinition
// reports edit's renderShell "self" (core/tools/edit.ts:157) and no other built-in tool names a render shell or an execution mode.
func TestSessionRegistersBuiltInToolDefinitionsAsPiDoes(t *testing.T) {
	session := newRegistryPortSession(t, []string{"read", "edit"}, SessionOptions{}, nil, nil)
	for _, name := range []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls"} {
		definition, ok := session.GetToolDefinition(name)
		if !ok {
			t.Fatalf("no definition for %s", name)
		}
		wantShell := extension.ToolRenderShell("")
		if name == "edit" {
			wantShell = extension.ToolRenderShellSelf
		}
		if definition.RenderShell != wantShell || definition.ExecutionMode != "" {
			t.Errorf("%s: renderShell %q executionMode %q, want %q and none", name, definition.RenderShell, definition.ExecutionMode, wantShell)
		}
	}
}
