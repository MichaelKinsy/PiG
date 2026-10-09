package inproc_test

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

func mcpConfig(url string) json.RawMessage { return json.RawMessage(`{"url":"` + url + `"}`) }

func mcpServerURLs(servers []extension.RegisteredMcpServer) []string {
	out := make([]string, len(servers))
	for i, server := range servers {
		out[i] = server.Name + "=" + server.Config.URL + "@" + server.ExtensionPath
	}
	return out
}

// Upstream suite/agent-session-mcp.test.ts "rejects names another extension registered" (0.99.2): names that differ only
// in - and _ share a namespace (#10239), loader.ts registerMcpServer.
func TestRuntimeRegisterMcpServerRejectsNamesThatShareANamespace(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	if err := runtime.RegisterMcpServer("/ext/a.ts", "my-server", mcpConfig("http://x.invalid")); err != nil {
		t.Fatal(err)
	}
	err := runtime.RegisterMcpServer("/ext/b.ts", "my_server", mcpConfig("http://z.invalid"))
	if want := `MCP server "my_server" conflicts with registered server "my-server"`; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	// Registering the same name again is not a clash.
	if err := runtime.RegisterMcpServer("/ext/a.ts", "my-server", mcpConfig("http://y.invalid")); err != nil {
		t.Fatalf("re-registration: %v", err)
	}
	if got, want := mcpServerURLs(runtime.McpServers().List()), []string{"my-server=http://y.invalid@/ext/a.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("servers = %v, want %v", got, want)
	}
}

// Upstream loader.ts:456-478 registerMcpServer / unregisterMcpServer / getMcpServers.
// Upstream suite/agent-session-mcp.test.ts:704 "rejects names another extension registered".
func TestRuntimeRegisterMcpServerValidatesChecksOwnershipAndReplacesOwnRegistration(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()

	// loader.ts:458-461: an invalid entry throws with the extension's path and the validation message.
	err := runtime.RegisterMcpServer("/ext/a.ts", "bad name", mcpConfig("http://x.invalid"))
	want := `Invalid MCP server registered by extension "/ext/a.ts": invalid server name "bad name" (use letters, digits, "_" and "-")`
	if err == nil || err.Error() != want {
		t.Fatalf("invalid name error = %v, want %q", err, want)
	}
	if err := runtime.RegisterMcpServer("/ext/a.ts", "sse", json.RawMessage(`{"type":"sse","url":"http://x.invalid"}`)); err == nil ||
		err.Error() != `Invalid MCP server registered by extension "/ext/a.ts": server "sse": legacy SSE transport is not supported; use the streamable HTTP URL` {
		t.Fatalf("legacy SSE error = %v", err)
	}
	if got := runtime.McpServers().List(); len(got) != 0 {
		t.Fatalf("invalid registrations stored %v", mcpServerURLs(got))
	}

	// agent-session-mcp.test.ts:706-712: registering again replaces the extension's own registration, keeping its position.
	for _, step := range []struct{ path, name, url string }{
		{"/ext/a.ts", "taken", "http://x.invalid"},
		{"/ext/a.ts", "other", "http://o.invalid"},
		{"/ext/a.ts", "taken", "http://y.invalid"},
	} {
		if err := runtime.RegisterMcpServer(step.path, step.name, mcpConfig(step.url)); err != nil {
			t.Fatalf("register %s: %v", step.name, err)
		}
	}
	if got, want := mcpServerURLs(runtime.McpServers().List()), []string{"taken=http://y.invalid@/ext/a.ts", "other=http://o.invalid@/ext/a.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("servers = %v, want %v", got, want)
	}

	// loader.ts:462-465: another extension cannot take the name.
	err = runtime.RegisterMcpServer("/ext/b.ts", "taken", mcpConfig("http://z.invalid"))
	if err == nil || err.Error() != `MCP server "taken" is already registered by extension "/ext/a.ts"` {
		t.Fatalf("ownership error = %v", err)
	}

	// mcp-servers.ts:227-233 unregister: servers of other extensions are left alone.
	runtime.UnregisterMcpServer("/ext/b.ts", "taken")
	if got := len(runtime.McpServers().List()); got != 2 {
		t.Fatalf("a foreign unregister removed a server: %v", mcpServerURLs(runtime.McpServers().List()))
	}
	runtime.UnregisterMcpServer("/ext/a.ts", "taken")
	if got, want := mcpServerURLs(runtime.McpServers().List()), []string{"other=http://o.invalid@/ext/a.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("servers = %v, want %v", got, want)
	}
}

// Upstream runner.ts:459-462 (change listener set in bindCore) and mcp-servers.ts:221-233.
func TestRunnerEmitsMcpServersChangeForRegistrationsAfterBind(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	if err := runtime.RegisterMcpServer("/ext/plugin.ts", "early", mcpConfig("http://early.invalid")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var events []extension.McpServersChangeEvent
	changed := make(chan struct{}, 8)
	runner := inproc.NewRunner([]extension.Extension{{Path: "/ext/mcp.ts", Handlers: map[string][]extension.HandlerFn{
		"mcp_servers_change": {func(args ...any) (any, error) {
			mu.Lock()
			events = append(events, args[0].(extension.McpServersChangeEvent))
			mu.Unlock()
			changed <- struct{}{}
			return nil, nil
		}},
	}}}, t.TempDir(), runtime)
	runner.AddErrorListener(func(err *extension.ExtensionError) { t.Errorf("error listener: %+v", err) })

	// Registrations while loading are read on session_start: no event before the runner binds.
	if err := runtime.RegisterMcpServer("/ext/plugin.ts", "during-load", mcpConfig("http://load.invalid")); err != nil {
		t.Fatal(err)
	}
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	select {
	case <-changed:
		t.Fatal("bindCore emitted mcp_servers_change for registrations made while loading")
	case <-time.After(50 * time.Millisecond):
	}

	if err := runtime.RegisterMcpServer("/ext/plugin.ts", "late", mcpConfig("http://late.invalid")); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, changed)
	runtime.UnregisterMcpServer("/ext/plugin.ts", "late")
	waitEvent(t, changed)

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	// runner.ts:460: `servers: this.runtime.mcpServers.list()` is the full list after each change, in registration order.
	wantAfterRegister := []string{"early=http://early.invalid@/ext/plugin.ts", "during-load=http://load.invalid@/ext/plugin.ts", "late=http://late.invalid@/ext/plugin.ts"}
	if events[0].Type != "mcp_servers_change" || !reflect.DeepEqual(mcpServerURLs(events[0].Servers), wantAfterRegister) {
		t.Fatalf("register event = %+v", events[0])
	}
	if got := mcpServerURLs(events[1].Servers); !reflect.DeepEqual(got, wantAfterRegister[:2]) {
		t.Fatalf("unregister event servers = %v, want %v", got, wantAfterRegister[:2])
	}
}

func waitEvent(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for mcp_servers_change")
	}
}

// Upstream runner.ts:747-762 reportUnhandledMcpServers, called from agent-session.ts:3195 after session_start.
// Upstream suite/agent-session-mcp.test.ts:723 "reports registered servers when no extension connects them".
func TestRunnerReportsRegisteredMcpServersWhenNoExtensionHandlesThem(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	if err := runtime.RegisterMcpServer("/ext/orphan.ts", "orphan", mcpConfig("http://orphan.invalid")); err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner(nil, t.TempDir(), runtime)
	var reported []extension.ExtensionError
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, *err) })
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	runner.ReportUnhandledMcpServers()

	want := extension.ExtensionError{
		ExtensionPath: "/ext/orphan.ts",
		Event:         "register_mcp_server",
		Error:         `MCP server "orphan" is registered, but no loaded extension connects MCP servers; another extension may have replaced the built-in MCP support`,
	}
	if !reflect.DeepEqual(reported, []extension.ExtensionError{want}) {
		t.Fatalf("reported = %+v, want [%+v]", reported, want)
	}

	// The report is once per server name (reportedMcpServers).
	runner.ReportUnhandledMcpServers()
	if len(reported) != 1 {
		t.Fatalf("a second report repeated the error: %+v", reported)
	}

	// A server registered after the bind is reported by the change listener, without an explicit call.
	if err := runtime.RegisterMcpServer("/ext/late.ts", "late", mcpConfig("http://late.invalid")); err != nil {
		t.Fatal(err)
	}
	if len(reported) != 2 || reported[1].ExtensionPath != "/ext/late.ts" || reported[1].Event != "register_mcp_server" {
		t.Fatalf("late registration was not reported: %+v", reported)
	}
}

// pig additive (D92): with mcp stripped (a Binary's OFF shim or a Piglet's runtime strip records it in pigstrip), nothing
// replaced the built-in MCP support, so the report names the strip instead of Pi's "another extension may have replaced".
func TestRunnerNamesTheMcpStripForUnhandledMcpServers(t *testing.T) {
	t.Cleanup(pigstrip.Strip(pigstrip.ListExtensions, "mcp"))
	runtime := extension.CreateExtensionRuntime()
	if err := runtime.RegisterMcpServer("/ext/orphan.ts", "orphan", mcpConfig("http://orphan.invalid")); err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner(nil, t.TempDir(), runtime)
	var reported []extension.ExtensionError
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, *err) })
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	runner.ReportUnhandledMcpServers()
	want := extension.ExtensionError{
		ExtensionPath: "/ext/orphan.ts",
		Event:         "register_mcp_server",
		Error:         `MCP server "orphan" is registered, but no loaded extension connects MCP servers; built-in MCP support is stripped from this Piglet (strip.extensions: mcp)`,
	}
	if !reflect.DeepEqual(reported, []extension.ExtensionError{want}) {
		t.Fatalf("reported = %+v, want [%+v]", reported, want)
	}
}

// Upstream runner.ts:748: an extension that handles mcp_servers_change connects the servers, so nothing is reported.
func TestRunnerDoesNotReportMcpServersWhenAnExtensionHandlesThem(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	if err := runtime.RegisterMcpServer("/ext/plugin.ts", "plugin", mcpConfig("http://plugin.invalid")); err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{{Path: "/ext/mcp.ts", Handlers: map[string][]extension.HandlerFn{
		"mcp_servers_change": {func(...any) (any, error) { return nil, nil }},
	}}}, t.TempDir(), runtime)
	runner.AddErrorListener(func(err *extension.ExtensionError) { t.Errorf("reported despite a handler: %+v", err) })
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	runner.ReportUnhandledMcpServers()
}
