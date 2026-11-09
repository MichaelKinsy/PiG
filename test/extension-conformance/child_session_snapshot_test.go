package extensionconformance

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/childsession"
)

// The deprecated creation-time fields of an AgentSession (session id and file, model, thinking level, system prompt, active tools) beside the live members that replace them. The scenario is the child_snapshot tool of every SDK's child session fixture: a file-backed session with one active tool, read once it exists, then its active tools are emptied. The fields must hold the creation values after the change and the live members must hold the new ones; the values are ones a fallback does not produce (a path under the scenario's directory, the sentinel model's id, a tool name, an empty list the change produced).

// childSnapshotWant is the observation of the scenario, identical for every SDK.
var childSnapshotWant = map[string]any{
	"snapshotId": true, "snapshotFile": true, "snapshotModel": "echo", "snapshotThinking": "off",
	"snapshotTools": []any{"sentinel_tool"}, "snapshotPromptListsTool": true,
	"liveStart": map[string]any{"model": "echo", "thinking": "off", "prompt": true, "tools": []any{"sentinel_tool"}},
	"afterChange": map[string]any{
		"liveTools": []any{}, "snapshotTools": []any{"sentinel_tool"}, "livePromptDropsTool": true, "snapshotPrompt": true,
	},
}

func TestChildAgentSessionSnapshotGoPlacements(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			host, _ := newChildSessionHost(t)
			root := findModuleRoot(t)
			cfg := subprocess.ExtConfig{
				Name: childsession.Name, Source: filepath.Join(root, "test/extension-conformance/testdata/child-session-go"), Enabled: true, Isolation: isolation,
				RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "factory", Factory: "Extension",
				SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: "example.com/child-session-go", Package: "example.com/child-session-go",
			}
			configs := []subprocess.ExtConfig{cfg}
			if isolation == "shared-ok" {
				configs = append(configs, providerPeer(t, root, cfg))
			}
			loaded, failures := host.LoadAll(t.Context(), configs)
			if len(failures) != 0 || len(loaded) != len(configs) {
				t.Fatalf("load: %v", failures)
			}
			checkChildSnapshot(t, loaded[0])
		})
	}
}

func TestChildAgentSessionSnapshotGoFused(t *testing.T) {
	host, _ := newChildSessionHost(t)
	ext, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: childsession.Name, Path: "/ext/child-session.go", Enabled: true}, childsession.Extension().RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	checkChildSnapshot(t, *ext)
}

func checkChildSnapshot(t *testing.T, ext extension.Extension) {
	t.Helper()
	tool, found := ext.Tools["child_snapshot"]
	if !found {
		t.Fatalf("no child_snapshot tool among %d tools", len(ext.Tools))
	}
	result, err := tool.Definition.Execute(t.Context(), "call", json.RawMessage(`{"dir":`+quoteJSON(t.TempDir())+`}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Text()), &got); err != nil {
		t.Fatalf("%q: %v", result.Text(), err)
	}
	if !reflect.DeepEqual(got, childSnapshotWant) {
		t.Errorf("observation = %s\nwant %#v", result.Text(), childSnapshotWant)
	}
}

// TestChildAgentSessionSnapshotLisp runs the Lisp child_snapshot scenario (testdata/child-session-lisp) isolated and packed with a sibling.
func TestChildAgentSessionSnapshotLisp(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			host, _ := newChildSessionHost(t)
			root := findModuleRoot(t)
			cfg := lispFixtureConfig(t, "child-session-lisp", childsession.Name)
			cfg.Isolation = isolation
			configs := []subprocess.ExtConfig{cfg}
			if isolation == "shared-ok" {
				peer := packedFlagFactory(t, root, "lisp", "child-session-peer", false)
				peer.Isolation = isolation
				configs = append(configs, peer)
			}
			loaded, failures := host.LoadAll(t.Context(), configs)
			if len(failures) != 0 || len(loaded) != len(configs) {
				t.Fatalf("load: %v", failures)
			}
			checkChildSnapshot(t, loaded[0])
		})
	}
}
