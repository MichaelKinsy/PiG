package subprocess

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Both realizations execute the same factories and compare their complete returned turn event, including chained drafts, context snapshots and errors.
func TestTurnBoundaryPackedFactoriesMatchIsolated(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	for _, language := range []string{"go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			configs := []ExtConfig{}
			for _, suffix := range []string{"a", "b"} {
				name := "boundary-" + language + "-" + suffix
				tool := "tool-" + suffix
				switch language {
				case "go":
					module := "example.com/boundary/" + suffix
					configs = append(configs, packedFactoryConfig(name, writePackedFactoryModule(t, module, name, tool), module, "h"+suffix))
				case "python":
					module := "boundary_py_" + suffix
					configs = append(configs, packedPythonFactoryConfig(name, writePackedPythonFactoryModule(t, module, name, tool), module, "h"+suffix))
				case "rust":
					module := "boundary_rs_" + suffix
					configs = append(configs, packedRustFactoryConfig(name, writePackedRustFactoryCrate(t, module, name, tool), module, "h"+suffix))
				}
			}
			var baseline any
			for _, packed := range []bool{false, true} {
				name := "isolated"
				if packed {
					name = "packed"
				}
				t.Run(name, func(t *testing.T) {
					host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					defer host.Shutdown("boundary complete")
					bridge := NewUIBridge(func() {})
					bridge.SetNotifyFunc(func(string, string) {})
					host.SetUIBridge(bridge)
					selected := slices.Clone(configs)
					if !packed {
						for i := range selected {
							selected[i].Isolation = "isolated"
						}
					}
					loaded, errs := host.LoadAll(t.Context(), selected)
					if len(errs) != 0 {
						t.Fatal(errs)
					}
					if len(loaded) != len(configs) {
						t.Fatalf("loaded=%d", len(loaded))
					}
					present, keys := packedCellSnapshot(host, configs[0].Name, configs[1].Name)
					shared := present[0] && present[1] && keys[0] != "" && keys[0] == keys[1]
					if shared != packed {
						t.Fatalf("packed=%v presence=%v keys=%v", packed, present, keys)
					}
					runner := inproc.NewRunner(loaded, t.TempDir())
					diagnostics := []string{}
					runner.AddErrorListener(func(err *extension.ExtensionError) { diagnostics = append(diagnostics, err.Error) })
					result, err := runner.EmitBoundary(t.Context(), extension.TurnEndEvent{Type: "turn_end", TurnIndex: 7, MessageEntryID: "boundary-assistant", ToolResultEntryIds: []string{"boundary-tool"}, Message: map[string]any{"role": "assistant", "content": []any{}, "stopReason": "error"}, ToolResults: []extension.ToolResultMessage{}, BoundaryState: &extension.BoundaryState{Outcome: extension.AgentActivityError}}, func(drafts []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
						entries := make([]extension.ProjectedSessionEntry, len(drafts))
						for i, draft := range drafts {
							entries[i] = extension.ProjectedSessionEntry{SourceEntry: map[string]any{"type": draft.Type, "customType": draft.CustomType}, Messages: []extension.AgentMessage{}}
						}
						return extension.BoundaryContextPreview{ContextEntries: entries, ContextMessages: []extension.AgentMessage{}, LLMMessages: []any{}, PendingMessages: []extension.AgentMessage{map[string]any{"role": "custom", "content": "pending"}}, CanContinue: len(entries) > 0}, nil
					})
					if err != nil {
						t.Fatal(err)
					}
					if len(diagnostics) != len(configs) {
						t.Fatalf("diagnostics=%v", diagnostics)
					}
					for _, diagnostic := range diagnostics {
						if !strings.Contains(diagnostic, "turn-boundary-failure") {
							t.Error(diagnostic)
						}
					}
					if !result.Valid || !result.Continue || len(result.Entries) != 1 || result.Entries[0].CustomType != "turn-boundary" {
						t.Fatalf("result=%+v", result)
					}
					raw, err := json.Marshal(result)
					if err != nil {
						t.Fatal(err)
					}
					var actual any
					if err := json.Unmarshal(raw, &actual); err != nil {
						t.Fatal(err)
					}
					data := result.Entries[0].Data.(map[string]any)
					if data["outcome"] != "error" || data["messageEntryId"] != "boundary-assistant" || data["turnIndex"] != float64(7) || data["continue"] != true || len(data["entries"].([]any)) != 2 || len(data["context"].(map[string]any)["contextEntries"].([]any)) != 2 {
						t.Fatalf("chained turn snapshot=%v", data)
					}
					if !packed {
						baseline = actual
					} else if !reflect.DeepEqual(actual, baseline) {
						t.Errorf("packed snapshot=%s, isolated=%v", raw, baseline)
					}
				})
			}
		})
	}
}
