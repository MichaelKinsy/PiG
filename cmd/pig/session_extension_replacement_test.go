package main

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// print-mode.ts:80-101 and rpc-mode.ts:324-344 bind these to runtimeHost.
// The shared production builder must not leave a callable SDK method unbound.
func TestHeadlessExtensionReplacementBindings(t *testing.T) {
	for _, mode := range []extension.ExtensionMode{extension.ModePrint, extension.ModeJSON, extension.ModeRPC} {
		t.Run(string(mode), func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			session, err := coding.NewSession(services, coding.SessionOptions{SessionDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			bridge := subprocess.NewUIBridge(func() {})
			bindSessionExtensionActions(nil, bridge, func() *coding.Session { return session }, extension.ContextActions{GetMode: func() extension.ExtensionMode { return mode }})
			call := func(method string, args any) {
				t.Helper()
				raw, err := json.Marshal(args)
				if err != nil {
					t.Fatal(err)
				}
				result, err := bridge.HandleCall("probe", &subprocess.CallPayload{Method: method, Args: raw})
				if err != nil || result.Error != nil {
					t.Fatalf("%s = %+v, %v", method, result, err)
				}
				if string(result.Result) != `{"cancelled":false}` {
					t.Fatalf("%s = %s", method, result.Result)
				}
			}
			if _, err := session.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "saved"}}, StopReason: ai.StopReasonStop}}); err != nil {
				t.Fatal(err)
			}
			originalID, originalPath := session.ID(), session.Path()
			leaf := session.LeafID()
			if leaf == nil {
				t.Fatal("bootstrap thinking entry missing")
			}
			call("fork", map[string]any{"entryId": *leaf, "position": "at"})
			if session.ID() == originalID {
				t.Fatal("fork did not replace session")
			}
			call("switchSession", map[string]any{"sessionPath": originalPath})
			if session.ID() != originalID {
				t.Fatal("switch did not restore original")
			}
			call("newSession", map[string]any{"parentSession": originalPath})
			if session.ID() == originalID || session.Inner().ParentSession() != originalPath {
				t.Fatal("newSession lost identity or parentSession")
			}
		})
	}
}
