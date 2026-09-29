package main

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
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

// Print and JSON mode bind in-process extensions only through
// bindSessionExtensionActions. Like upstream _bindExtensionCore, it must give
// their context the session's tools, so a Piglet's tool scoping (tools: [])
// sees the registered tools and can narrow them.
func TestSessionExtensionActionsBindInProcessToolActions(t *testing.T) {
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
	runner := inproc.NewRunner(nil, cwd)
	bindSessionExtensionActions(runner, nil, func() *coding.Session { return session }, extension.ContextActions{})
	ctx := runner.CreateCommandContext()

	all := ctx.GetAllTools()
	if len(all) == 0 || len(all) != len(session.GetAllTools()) {
		t.Fatalf("GetAllTools = %v, want the session's %v", all, session.GetAllTools())
	}
	if got, want := ctx.GetActiveTools(), session.ActiveToolNames(); len(got) == 0 || !slices.Equal(got, want) {
		t.Fatalf("GetActiveTools = %v, want the session's %v", got, want)
	}
	ctx.SetActiveTools([]string{})
	if got := session.ActiveToolNames(); len(got) != 0 {
		t.Fatalf("session active tools after SetActiveTools([]) = %v, want none", got)
	}
}
