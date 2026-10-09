package cli

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// agent-session.ts:3419 binds setLabel to sessionManager.appendLabelChange in the Session's runner: the bridge host action reaches that handler, a label sets the entry's label and an empty one clears it (types.ts:2106 SetLabelHandler takes `string | undefined`).
// mutation-checked: a SetLabel action that drops the label (always clearing) fails it.
// Pi: packages/coding-agent/src/core/session-manager.ts:1432 (Session.getLabel).
func TestSessionExtensionActionsBindSetLabel(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Runner: inproc.NewRunner(nil, t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	entryID, err := session.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{
		Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "label me"}}, Timestamp: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	bridge := subprocess.NewUIBridge(nil)
	bindSessionReadActions(bridge, func() *coding.Session { return session }, session.CWD(), t.TempDir())
	setLabel := func(label string) {
		t.Helper()
		args, _ := json.Marshal(map[string]string{"entryId": entryID, "label": label})
		result, err := bridge.HandleCall("label-test", &subprocess.CallPayload{Method: "setLabel", Args: args})
		if err != nil || result == nil || result.Error != nil {
			t.Fatalf("setLabel(%q): %+v, %v", label, result, err)
		}
	}
	setLabel("milestone")
	if label, ok := session.Inner().GetLabel(entryID); !ok || label != "milestone" {
		t.Fatalf("label after setLabel = %q, %v, want milestone", label, ok)
	}
	setLabel("")
	if label, ok := session.Inner().GetLabel(entryID); ok {
		t.Fatalf("label after an empty setLabel = %q, want cleared", label)
	}
}
