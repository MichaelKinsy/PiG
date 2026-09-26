package main

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Upstream _bindExtensionCore binds getActiveTools and setActiveTools to the
// session in every mode. Print and JSON mode bind subprocess extensions
// through bindSessionExtensionActions, so an extension there sees the
// session's active tools and can change them, as under Pi.
func TestSessionExtensionActionsBindActiveTools(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir()})
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

	call := func(method string, args any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		result, err := bridge.HandleCall("probe", &subprocess.CallPayload{Method: method, Args: raw})
		if err != nil || result == nil || result.Error != nil {
			t.Fatalf("%s: %+v, %v", method, result, err)
		}
		return result.Result
	}
	active := func() []string {
		var result struct {
			Tools []string `json:"tools"`
		}
		if err := json.Unmarshal(call("getActiveTools", map[string]any{}), &result); err != nil {
			t.Fatal(err)
		}
		return result.Tools
	}
	if got, want := active(), session.ActiveToolNames(); len(got) == 0 || !slices.Equal(got, want) {
		t.Fatalf("getActiveTools = %v, want the session's %v", got, want)
	}
	call("setActiveTools", map[string]any{"tools": []string{"bash", "read"}})
	if got := active(); !slices.Equal(got, []string{"bash", "read"}) {
		t.Fatalf("getActiveTools after setActiveTools = %v, want [bash read]", got)
	}
}
