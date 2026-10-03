package sdk

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Upstream mcp-servers.ts: `toolExposure` patterns match in object order ("among patterns the first match in the object wins") and `env` and `headers` keep their key order, so the wire must not sort them.
func TestOrderedMcpMapsKeepInsertionOrderThroughTheWire(t *testing.T) {
	config := McpServerConfig{
		Command:      "docs-server",
		Args:         []string{"--stdio"},
		Env:          NewOrderedStrings("ZED", "1", "ALPHA", "2", "MID", "3"),
		Exposure:     McpExposureDirect,
		ToolExposure: NewOrderedExposures("search_*", "direct", "*", "hidden", "search_all", "codemode"),
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"exposure":"direct","toolExposure":{"search_*":"direct","*":"hidden","search_all":"codemode"},"command":"docs-server","args":["--stdio"],"env":{"ZED":"1","ALPHA":"2","MID":"3"}}`
	if string(data) != want {
		t.Fatalf("wire = %s\nwant  %s", data, want)
	}
	var decoded McpServerConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.ToolExposure.Keys(); !reflect.DeepEqual(got, []string{"search_*", "*", "search_all"}) {
		t.Fatalf("decoded toolExposure keys = %v", got)
	}
	if got := decoded.Env.Keys(); !reflect.DeepEqual(got, []string{"ZED", "ALPHA", "MID"}) {
		t.Fatalf("decoded env keys = %v", got)
	}
	if exposure, ok := decoded.ToolExposure.Get("*"); !ok || exposure != McpExposureHidden {
		t.Fatalf("Get(*) = %q %v", exposure, ok)
	}
	if _, ok := decoded.Env.Get("missing"); ok {
		t.Fatal("Get reported a key that is not there")
	}
	// A repeated key keeps its first position and its last value, as JSON.parse does.
	var repeated OrderedStrings
	if err := json.Unmarshal([]byte(`{"a":"1","b":"2","a":"3"}`), &repeated); err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, repeated); got != `{"a":"3","b":"2"}` {
		t.Fatalf("repeated key = %s", got)
	}
	if err := json.Unmarshal([]byte(`{"a":1}`), &repeated); err == nil {
		t.Fatal("a non-string value was accepted")
	}
}

// Upstream loader.ts:456-478: registrations made while the factory runs are applied when it returns, in call order; registering a name again replaces the earlier registration in its position (a Map's set) and unregistering drops the queued entry.
func TestRegisterMcpServerBeforeRunQueuesInRegisterPayload(t *testing.T) {
	ext := New("plugin")
	for _, step := range []struct{ name, url string }{{"docs", "http://docs.invalid"}, {"wiki", "http://wiki.invalid"}, {"gone", "http://gone.invalid"}, {"docs", "http://docs2.invalid"}} {
		if err := ext.RegisterMcpServer(step.name, McpServerConfig{URL: step.url, Exposure: McpExposureDeferred, Description: "Docs search", OAuth: &McpOAuthConfig{ClientName: "Claude Code"}, Auth: &McpAuthConfig{Provider: "radius"}}); err != nil {
			t.Fatal(err)
		}
	}
	ext.UnregisterMcpServer("gone")
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	if len(reg.McpServers) != 2 {
		t.Fatalf("mcp_servers = %+v", reg.McpServers)
	}
	// 0.99.2: description, oauth.clientName and auth.provider reach the host as the config carries them.
	if first := reg.McpServers[0].Config; first.Description != "Docs search" || first.OAuth == nil || first.OAuth.ClientName != "Claude Code" || first.Auth == nil || first.Auth.Provider != "radius" {
		t.Fatalf("config = %+v", first)
	}
	if reg.McpServers[0].Name != "docs" || reg.McpServers[0].Config.URL != "http://docs2.invalid" || reg.McpServers[0].Config.Exposure != McpExposureDeferred ||
		reg.McpServers[1].Name != "wiki" || reg.McpServers[1].Config.URL != "http://wiki.invalid" {
		t.Fatalf("mcp_servers = %+v", reg.McpServers)
	}
}

// Upstream loader.ts:456-478 after load: the call is applied at once, an invalid config or a foreign name is the caller's error, and the reply lists every registered server so getMcpServers sees the change without a host call.
func TestRegisterMcpServerAtRuntimeCallsTheHostAndRefreshesTheReplicatedList(t *testing.T) {
	ext := New("plugin")
	type outcome struct {
		registerErr error
		listed      []RegisteredMcpServer
		afterFail   []RegisteredMcpServer
		afterRemove []RegisteredMcpServer
	}
	got := make(chan outcome, 1)
	ext.Command("go", "", func(ctx Context, _ string) error {
		var o outcome
		var err error
		if err = ctx.RegisterMcpServer("late", McpServerConfig{Command: "late-server", Env: NewOrderedStrings("B", "1", "A", "2")}); err != nil {
			return err
		}
		if o.listed, err = ctx.GetMcpServers(); err != nil {
			return err
		}
		o.registerErr = ctx.RegisterMcpServer("taken", McpServerConfig{URL: "http://taken.invalid"})
		if o.afterFail, err = ctx.GetMcpServers(); err != nil {
			return err
		}
		ctx.UnregisterMcpServer("late")
		o.afterRemove, err = ctx.GetMcpServers()
		got <- o
		return err
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	lateOnly := `{"servers":[{"name":"other","config":{"url":"http://other.invalid"},"extensionPath":"/ext/other.ts"},{"name":"late","config":{"command":"late-server","env":{"B":"1","A":"2"}},"extensionPath":"/ext/plugin.ts"}]}`
	calls, resp := runSurfaceCommand(t, host, "go", func(call *callMsg) *callResultMsg {
		switch call.Method {
		case "registerMcpServer":
			if strings.Contains(string(call.Args), `"taken"`) {
				return &callResultMsg{Error: &errorInfo{Message: `MCP server "taken" is already registered by extension "/ext/other.ts"`}}
			}
			return &callResultMsg{Result: json.RawMessage(lateOnly)}
		case "unregisterMcpServer":
			return &callResultMsg{Result: json.RawMessage(`{"servers":[{"name":"other","config":{"url":"http://other.invalid"},"extensionPath":"/ext/other.ts"}]}`)}
		}
		return &callResultMsg{Error: &errorInfo{Message: "unexpected " + call.Method}}
	})
	if resp.Error != nil {
		t.Fatalf("command failed: %+v", resp.Error)
	}
	if len(calls) != 3 || calls[0].Method != "registerMcpServer" || calls[1].Method != "registerMcpServer" || calls[2].Method != "unregisterMcpServer" {
		t.Fatalf("calls = %+v (getMcpServers must not call the host)", calls)
	}
	if want := `{"name":"late","config":{"command":"late-server","env":{"B":"1","A":"2"}}}`; string(calls[0].Args) != want {
		t.Fatalf("registerMcpServer args = %s, want %s", calls[0].Args, want)
	}
	if want := `{"name":"taken"`; !strings.HasPrefix(string(calls[1].Args), want) {
		t.Fatalf("second registerMcpServer args = %s", calls[1].Args)
	}
	if string(calls[2].Args) != `{"name":"late"}` {
		t.Fatalf("unregisterMcpServer args = %s", calls[2].Args)
	}
	o := recv(t, got)
	if len(o.listed) != 2 || o.listed[0].Name != "other" || o.listed[1].Name != "late" || o.listed[1].ExtensionPath != "/ext/plugin.ts" || o.listed[1].Config.Command != "late-server" {
		t.Fatalf("list after register = %+v", o.listed)
	}
	if got := o.listed[1].Config.Env.Keys(); !reflect.DeepEqual(got, []string{"B", "A"}) {
		t.Fatalf("replicated env order = %v", got)
	}
	if o.registerErr == nil || !strings.Contains(o.registerErr.Error(), `MCP server "taken" is already registered by extension "/ext/other.ts"`) {
		t.Fatalf("foreign-name error = %v", o.registerErr)
	}
	if len(o.afterFail) != 2 {
		t.Fatalf("a failed registration changed the list: %+v", o.afterFail)
	}
	if len(o.afterRemove) != 1 || o.afterRemove[0].Name != "other" {
		t.Fatalf("list after unregister = %+v", o.afterRemove)
	}
}

// Upstream types.ts:1839 getMcpServers is synchronous: the host replicates the list with each state, and an update replaces it wholesale, including with an empty list.
func TestGetMcpServersReadsReplicatedState(t *testing.T) {
	ext := New("reader")
	got := make(chan []RegisteredMcpServer, 3)
	errs := make(chan error, 3)
	ext.Command("read", "", func(ctx Context, _ string) error {
		servers, err := ctx.GetMcpServers()
		got <- servers
		errs <- err
		return nil
	})
	ready := &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"mcpServers":[{"name":"a","config":{"url":"http://a.invalid","oauth":{"clientId":"id","callbackPort":8080}},"extensionPath":"/ext/a.ts"}]}`)}
	host, _, done := surfaceHost(t, ext, ready)
	defer surfaceShutdown(t, host, done)
	step := func() []RegisteredMcpServer {
		t.Helper()
		_, resp := runSurfaceCommand(t, host, "read", func(*callMsg) *callResultMsg { return &callResultMsg{} })
		if resp.Error != nil {
			t.Fatal(resp.Error)
		}
		if err := recv(t, errs); err != nil {
			t.Fatal(err)
		}
		return recv(t, got)
	}
	first := step()
	if len(first) != 1 || first[0].Name != "a" || first[0].Config.URL != "http://a.invalid" || first[0].ExtensionPath != "/ext/a.ts" ||
		first[0].Config.OAuth == nil || first[0].Config.OAuth.ClientID != "id" || first[0].Config.OAuth.CallbackPort == nil || *first[0].Config.OAuth.CallbackPort != 8080 {
		t.Fatalf("initial state list = %+v", first)
	}
	sendState(t, host, `{"mcpServers":[{"name":"b","config":{"command":"b"},"extensionPath":"/ext/b.ts"},{"name":"c","config":{"command":"c"},"extensionPath":"/ext/c.ts"}]}`)
	second := step()
	if len(second) != 2 || second[0].Name != "b" || second[1].Name != "c" {
		t.Fatalf("list after state_update = %+v", second)
	}
	sendState(t, host, `{"mcpServers":[]}`)
	if third := step(); len(third) != 0 {
		t.Fatalf("list after an empty update = %+v", third)
	}
}

// A host failure of the registration call is returned to the caller, not swallowed, and does not disturb the replicated list.
func TestRegisterMcpServerReturnsTransportFailure(t *testing.T) {
	ext := New("plugin")
	failure := make(chan error, 1)
	ext.Command("go", "", func(ctx Context, _ string) error {
		failure <- ctx.RegisterMcpServer("x", McpServerConfig{URL: "http://x.invalid"})
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	runSurfaceCommand(t, host, "go", func(*callMsg) *callResultMsg {
		return &callResultMsg{Error: &errorInfo{Code: "host_failed", Message: "boom"}}
	})
	if err := recv(t, failure); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v", err)
	}
}

// A state update the host sent before the reply of a registration call is older than the reply. The read loop routes the reply straight to the caller while the message loop applies the update later, so applying the reply first lets the stale list overwrite it: a caller that reads getMcpServers after registerMcpServer returns must see its own registration. Found by the conformance row under CPU load, where the host's state push for an earlier registration reached the message loop after the reply.
func TestRegisterMcpServerReplyOutranksAnOlderStateUpdate(t *testing.T) {
	ext := New("plugin")
	registered := make(chan error, 1)
	proceed := make(chan struct{})
	listed := make(chan []RegisteredMcpServer, 1)
	ext.Command("arm", "", func(Context, string) error { return nil })
	ext.Command("go", "", func(ctx Context, _ string) error {
		registered <- ctx.RegisterMcpServer("late", McpServerConfig{URL: "http://late.invalid"})
		<-proceed
		servers, err := ctx.GetMcpServers()
		listed <- servers
		return err
	})
	ready := &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"mcpServers":[]}`)}
	host, _, done := surfaceHost(t, ext, ready)
	defer surfaceShutdown(t, host, done)

	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-go", Request: &requestMsg{Method: "command", Tool: "go"}})
	var call envelope
	for call.Call == nil {
		if env := host.readEnvelope(t); env.Type == msgCall {
			call = env
		}
	}
	// The message loop is busy while the host's state update and then the reply arrive: a notify that waits for a lock the test holds keeps it from applying the update.
	ext.toolRenderMu.Lock()
	held := true
	defer func() {
		if held {
			ext.toolRenderMu.Unlock()
		}
	}()
	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "tool_render_release", Args: json.RawMessage(`{"card":"held"}`)}})
	sendState(t, host, `{"mcpServers":[{"name":"old","config":{"url":"http://old.invalid"},"extensionPath":"/ext/old.ts"}]}`)
	reply := `{"servers":[{"name":"old","config":{"url":"http://old.invalid"},"extensionPath":"/ext/old.ts"},{"name":"late","config":{"url":"http://late.invalid"},"extensionPath":"/ext/plugin.ts"}]}`
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(reply)}})

	// Whether the registration call returns before the loop applies the older update or after it, the caller then reads its own registration.
	release := func() {
		held = false
		ext.toolRenderMu.Unlock()
	}
	select {
	case err := <-registered:
		if err != nil {
			t.Fatal(err)
		}
		release()
	case <-time.After(300 * time.Millisecond):
		release()
		if err := recv(t, registered); err != nil {
			t.Fatal(err)
		}
	}
	// A later request runs after the message loop applied the older update.
	if _, resp := runSurfaceCommand(t, host, "arm", func(*callMsg) *callResultMsg { return &callResultMsg{} }); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	close(proceed)
	servers := recv(t, listed)
	if len(servers) != 2 || servers[1].Name != "late" {
		t.Fatalf("servers after the registration = %+v, want the reply's list", servers)
	}
}

// An extension may call its Extension-level registration API from a width handler, as upstream's pi.registerMcpServer is callable from any callback. The call must return with the reply's list installed, whichever goroutine runs the handler.
func TestRegisterMcpServerFromAWidthHandlerReturns(t *testing.T) {
	ext := New("plugin")
	registered := make(chan error, 1)
	listed := make(chan []RegisteredMcpServer, 1)
	ext.Command("arm", "", func(ctx Context, _ string) error {
		_, err := ctx.OnWidthChange(func(Context, int) {
			registered <- ext.RegisterMcpServer("w", McpServerConfig{URL: "http://w.invalid"})
		})
		return err
	})
	ext.Command("read", "", func(ctx Context, _ string) error {
		servers, err := ctx.GetMcpServers()
		listed <- servers
		return err
	})
	ready := &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"mcpServers":[]}`)}
	host, _, done := surfaceHost(t, ext, ready)
	defer surfaceShutdown(t, host, done)
	if _, resp := runSurfaceCommand(t, host, "arm", func(*callMsg) *callResultMsg { return &callResultMsg{} }); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "width_change", Args: json.RawMessage(`{"width":100}`)}})
	var call envelope
	for call.Call == nil {
		if env := host.readEnvelope(t); env.Type == msgCall {
			call = env
		}
	}
	if call.Call.Method != "registerMcpServer" {
		t.Fatalf("call = %s", call.Call.Method)
	}
	reply := `{"servers":[{"name":"w","config":{"url":"http://w.invalid"},"extensionPath":"/ext/plugin.ts"}]}`
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(reply)}})
	if err := recv(t, registered); err != nil {
		t.Fatal(err)
	}
	if _, resp := runSurfaceCommand(t, host, "read", func(*callMsg) *callResultMsg { return &callResultMsg{} }); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if servers := recv(t, listed); len(servers) != 1 || servers[0].Name != "w" {
		t.Fatalf("servers = %+v, want the reply's list", servers)
	}
}
