package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi agent-session.ts:3073 binds name actions to the Session in every mode.
func TestSessionExtensionActionsBindSessionName(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	bridge := subprocess.NewUIBridge(nil)
	bindSessionExtensionActions(nil, bridge, func() *coding.Session { return session }, extension.ContextActions{})
	var names []string
	session.Subscribe(func(event agent.AgentEvent) {
		if info, ok := event.(agent.SessionInfoChangedEvent); ok {
			names = append(names, info.Name)
		}
	})
	call := func(method, args string) json.RawMessage {
		t.Helper()
		result, err := bridge.HandleCall("name-test", &subprocess.CallPayload{Method: method, Args: json.RawMessage(args)})
		if err != nil || result == nil || result.Error != nil {
			t.Fatalf("%s: %+v, %v", method, result, err)
		}
		return result.Result
	}
	call("setSessionName", `{"name":" from\nextension "}`)
	var result struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(call("getSessionName", `{}`), &result); err != nil {
		t.Fatal(err)
	}
	if result.Name != "from extension" || session.SessionName() != result.Name || !reflect.DeepEqual(names, []string{"from extension"}) {
		t.Fatalf("name=%q session=%q events=%q", result.Name, session.SessionName(), names)
	}
}
