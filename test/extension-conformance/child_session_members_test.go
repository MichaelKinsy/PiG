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

// The AgentSession members an extension reaches over the wire beyond prompt, steer and abort (C2Sessions): each is read or changed on the host's session and observed back. The scenario is the child_members tool of the Go child session fixture; every value is one the session produces and a fallback does not (a mode set to a non-default, a name only the scenario chose, the sentinel model's echo of a prompt, a command's output, Pi's refusal text for a session too small to compact).

// childMembersWant is the observation of the scenario. Pi's own values: the defaults are Pi's (one-at-a-time modes, auto retry on), the refusals are Pi's messages.
var childMembersWant = map[string]any{
	"activeTools": []any{"sentinel_tool"}, "activeToolsEmpty": []any{}, "allTools": []any{"sentinel_tool"},
	"autoCompactionEnabled": false, "autoCompactionSet": true, "autoRetryEnabled": true, "autoRetrySet": false,
	"availableThinkingLevels": []any{"off"}, "bash": map[string]any{"exit": true, "output": "member-bash\n"}, "bashRunning": false,
	"bugReport": true, "cleared": []any{0.0, 0.0}, "compactRefused": "Nothing to compact (session too small)", "cycleOnlyModel": true,
	"exportedJsonl": true, "followUpMode": "one-at-a-time", "followUpModeSet": "all", "fork": "hello", "hasHandlers": false,
	"isCompacting": false, "isIdle": true, "isRetrying": false, "isStreaming": false, "jsonlHasSession": true,
	"lastText": "sentinel-model:echo:hello", "lastTextStart": true, "model": "echo", "navigate": map[string]any{"cancelled": false, "editorText": "hello"},
	"pendingMessageCount": 0.0, "promptTemplates": 0.0, "retryAttempt": 0.0,
	"roles":           []any{"system", "user", "assistant", "user", "assistant", "custom", "bashExecution", "bashExecution"},
	"scopedModelsSet": "echo:medium", "scopedModelsStart": 0.0, "sentText": "sentinel-model:echo:via-send", "sessionFile": "", "sessionId": true,
	"sessionName": "", "sessionNameSet": "named-by-members", "setModelRefused": "No API key for nokey/locked",
	"stats":        map[string]any{"assistant": 1.0, "idMatches": true, "total": 3.0, "user": 1.0},
	"steeringMode": "one-at-a-time", "steeringModeSet": "all", "systemPromptLen": true, "thinkingLevel": "off",
	"toolDefinition": "uppercases x", "toolDefinitionMissing": true,
	"callableTools": []any{"sentinel_tool"}, "steeringMessages": 0.0, "followUpMessages": 0.0, "routedModel": true, "pendingBash": false,
	"supportsThinking": false, "cycledThinking": "", "cacheWarming": "inactive", "exportHtmlInMemory": "Cannot export in-memory session to HTML", "afterReload": true,
	"fileBackedSessionFile": true,
}

func TestChildAgentSessionMembersGoPlacements(t *testing.T) {
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
			checkChildMembers(t, loaded[0])
		})
	}
}

func TestChildAgentSessionMembersGoFused(t *testing.T) {
	host, _ := newChildSessionHost(t)
	ext, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: childsession.Name, Path: "/ext/child-session.go", Enabled: true}, childsession.Extension().RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	checkChildMembers(t, *ext)
}

func checkChildMembers(t *testing.T, ext extension.Extension) {
	t.Helper()
	result, err := ext.Tools["child_members"].Definition.Execute(t.Context(), "call", json.RawMessage(`{"dir":`+quoteJSON(t.TempDir())+`}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Text()), &got); err != nil {
		t.Fatalf("%q: %v", result.Text(), err)
	}
	for key, want := range childMembersWant {
		if !reflect.DeepEqual(got[key], want) {
			t.Errorf("%s = %#v, want %#v", key, got[key], want)
		}
	}
	for key := range got {
		if _, listed := childMembersWant[key]; !listed {
			t.Errorf("unexpected observation %s = %#v", key, got[key])
		}
	}
}
