package main

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Headless Pi binds the Session's thinking level and base prompt options into every extension
// (agent-session.ts:3125). Without the binding the host answers its unbound defaults ("" and an empty
// cwd), which every SDK then returns as if it were the Session's own value.
func TestSessionExtensionActionsBindThinkingLevelAndPromptOptions(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	cwd := t.TempDir()
	services, err := coding.NewServices(coding.ServicesOptions{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	bridge := subprocess.NewUIBridge(func() {})
	bindSessionExtensionActions(nil, bridge, func() *coding.Session { return session }, extension.ContextActions{})
	call := func(method string) json.RawMessage {
		t.Helper()
		result, err := bridge.HandleCall("probe", &subprocess.CallPayload{Method: method, Args: json.RawMessage(`{}`)})
		if err != nil || result == nil || result.Error != nil {
			t.Fatalf("%s: %+v, %v", method, result, err)
		}
		return result.Result
	}
	var level struct {
		Level string `json:"level"`
	}
	if err := json.Unmarshal(call("getThinkingLevel"), &level); err != nil {
		t.Fatal(err)
	}
	if level.Level == "" || level.Level != string(session.ThinkingLevel()) {
		t.Errorf("getThinkingLevel = %q, want the session's %q", level.Level, session.ThinkingLevel())
	}
	var options struct {
		Cwd           string   `json:"cwd"`
		SelectedTools []string `json:"selectedTools"`
	}
	if err := json.Unmarshal(call("getSystemPromptOptions"), &options); err != nil {
		t.Fatal(err)
	}
	if options.Cwd != session.CWD() || !slices.Equal(options.SelectedTools, session.ActiveToolNames()) {
		t.Errorf("getSystemPromptOptions = %+v, want cwd %q and tools %v", options, session.CWD(), session.ActiveToolNames())
	}
	if raw := call("getContextUsage"); string(raw) != "null" && len(raw) == 0 {
		t.Errorf("getContextUsage = %s", raw)
	}
}
