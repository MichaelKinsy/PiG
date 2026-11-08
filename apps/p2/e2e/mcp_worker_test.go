// Ports test/p2/mcp-worker.test.ts (Pi main c23b9ec1): MCP tools in a session worker, end to end with the fake provider: the
// worker leases the owner's servers at the session's location with the project `.pi/mcp.json` it read there, the
// `mcp_servers` section lists them, a code mode script's call returns the server's answer (the model is not offered the
// tools, as in pi), a relocation moves the lease, a subagent's own location is leased while it is not archived, and with code
// mode off `tool_search` loads the tools for the model.

package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	mcpcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
)

// results are the tool result messages of a conversation view, in order.
func results(view delta.JsonValue) []*delta.JsonObject {
	var out []*delta.JsonObject
	for _, message := range messages(view) {
		if message.Value("role") == "toolResult" {
			out = append(out, message)
		}
	}
	return out
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMcpToolsInASessionsWorkerCodeModeScriptsCallThemAndToolSearchLoadsThemWithoutCodeMode(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed: the MCP fixture server is a Node program")
	}
	script, err := filepath.Abs("../plugins/mcp/testdata/mcp-server.mjs")
	if err != nil {
		t.Fatal(err)
	}
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	server := map[string]any{"command": node, "args": []string{script}}
	writeJSON(t, filepath.Join(fixture.DataDir, "mcp.json"), map[string]any{"mcpServers": map[string]any{"files": server}})
	// The project at the session's cwd adds a server there and refuses an HTTP one.
	writeJSON(t, filepath.Join(fixture.Workspace, ".pi", "mcp.json"), map[string]any{"mcpServers": map[string]any{
		"lint": server, "remote": map[string]any{"url": "https://example.com/mcp"},
	}})
	owner := fixture.Browser()
	mcp, err := p2test.HoldIn(owner, mcpcontract.Mcp, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "MCP"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	id := object(created).Value("id").(string)
	chat, err := p2test.HoldIn(owner, chatcontract.Chat, delta.JsonObjectOf("session", id, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	until := func(source p2test.JSONSource, predicate func(delta.JsonValue) bool) delta.JsonValue {
		t.Helper()
		value, err := p2test.Until(source, predicate, 60*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	// The worker leases the session's location; the owner's and the project's servers connect there.
	started := until(mcp.Member("instances"), func(v delta.JsonValue) bool {
		instances := list(v)
		if len(instances) != 2 {
			return false
		}
		for _, each := range instances {
			item := object(each)
			if item.Value("status") != "connected" || object(item.Value("location")).Value("cwd") != fixture.Workspace {
				return false
			}
		}
		return true
	})
	var pairs [][2]any
	for _, each := range list(started) {
		pairs = append(pairs, [2]any{object(each).Value("server"), object(each).Value("source")})
	}
	if !sameJSON(pairs, [][2]any{{"files", "owner"}, {"lint", "project"}}) {
		t.Fatalf("instances: %v", pairs)
	}
	instanceID := object(list(started)[0]).Value("id")
	errs := list(until(mcp.Member("errors"), func(v delta.JsonValue) bool { return len(list(v)) > 0 }))
	if !regexp.MustCompile(`\.pi/mcp\.json: server "remote": HTTP servers are for the owner's mcp\.json`).MatchString(errs[0].(string)) {
		t.Fatalf("errors: %v", errs)
	}
	serial := 0
	view := func(predicate func(delta.JsonValue) bool) delta.JsonValue { return until(chat.Member("view"), predicate) }
	call := func(prompt string) *delta.JsonObject {
		t.Helper()
		count := len(results(view(func(v delta.JsonValue) bool { return v != nil })))
		serial++
		if _, err := chat.Call("send", delta.JsonObjectOf("text", prompt, "requestId", "mcp-"+itoa(serial)), p2test.Background); err != nil {
			t.Fatal(err)
		}
		done := view(func(v delta.JsonValue) bool { return len(results(v)) > count && !running(v) })
		all := results(done)
		return all[len(all)-1]
	}
	// As in pi, the tools are for code mode scripts: the model is not offered them, and `tool_search` is not installed.
	echo := func(said string) string {
		t.Helper()
		result := call("codemode return await tools.mcp__files__echo({ text: " + marshal(t, said) + " });")
		if result.Value("isError") != false {
			t.Fatal(agentResultText(result))
		}
		return scriptOutput(t, agentResultText(result))
	}
	if got := echo("hello"); got != "echo: hello" {
		t.Fatalf("echo: %q", got)
	}
	if got := sectionsOf(view(func(v delta.JsonValue) bool { return v != nil }))["mcp_servers"]; got != "<mcp_servers>\nMCP servers whose tools are not declared to you. Call the tools of `codemode` servers from codemode scripts.\n- mcp__files (codemode)\n- mcp__lint (codemode)\n</mcp_servers>" {
		t.Fatalf("mcp_servers: %q", got)
	}
	if call("mcp__files__echo "+marshal(t, map[string]any{"text": "hello"})).Value("isError") != true {
		t.Fatal("the model was offered an MCP tool")
	}
	if call("tool_search "+marshal(t, map[string]any{"query": "echo"})).Value("isError") != true {
		t.Fatal("tool_search is installed with code mode on")
	}
	// The session moves in place: the worker leases the new location (no project there) and gives up the old one.
	moved := filepath.Join(fixture.Workspace, "moved")
	if err := os.Mkdir(moved, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Call("relocate", delta.JsonObjectOf("id", id, "cwd", moved, "env", nil), p2test.Background); err != nil {
		t.Fatal(err)
	}
	after := list(until(mcp.Member("instances"), func(v delta.JsonValue) bool {
		instances := list(v)
		return len(instances) == 1 && object(object(instances[0]).Value("location")).Value("cwd") == moved && object(instances[0]).Value("status") == "connected"
	}))
	if object(after[0]).Value("id") == instanceID {
		t.Fatal("the instance did not move")
	}
	if got := echo("there"); got != "echo: there" {
		t.Fatalf("echo: %q", got)
	}
	// A subagent spawned with its own cwd (a commit of the subagent registry): its location is leased too, until it is
	// archived.
	own := filepath.Join(moved, "own")
	if err := os.Mkdir(own, 0o755); err != nil {
		t.Fatal(err)
	}
	call("subagent " + marshal(t, map[string]any{"action": "spawn", "name": "own", "message": "hi", "cwd": "own", "tools": []string{"bash"}}))
	until(mcp.Member("instances"), func(v delta.JsonValue) bool {
		for _, each := range list(v) {
			item := object(each)
			if loc := object(item.Value("location")); loc != nil && loc.Value("cwd") == own && item.Value("status") == "connected" {
				return true
			}
		}
		return false
	})
	call("subagent " + marshal(t, map[string]any{"action": "archive", "name": "own"}))
	until(mcp.Member("instances"), func(v delta.JsonValue) bool {
		instances := list(v)
		return len(instances) == 1 && object(object(instances[0]).Value("location")).Value("cwd") == moved
	})
	// Code mode off: as pi falls back, `tool_search` loads the tools and the model calls them. The worker's settings
	// observer is asynchronous: turns go until one runs with `tool_search` installed.
	writeJSON(t, filepath.Join(fixture.DataDir, "settings.json"), map[string]any{"codemode": false})
	var found *delta.JsonObject
	for attempt := 0; attempt < 20 && (found == nil || found.Value("isError") != false); attempt++ {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		found = call("tool_search " + marshal(t, map[string]any{"query": "echo"}))
	}
	if found.Value("isError") != false || !regexp.MustCompile(`mcp__files__echo`).MatchString(agentResultText(found)) {
		t.Fatal(agentResultText(found))
	}
	if got := sectionsOf(view(func(v delta.JsonValue) bool { return v != nil }))["mcp_servers"]; !regexp.MustCompile(`- mcp__files \(tool_search\)\n</mcp_servers>$`).MatchString(got) {
		t.Fatalf("mcp_servers: %q", got)
	}
	echoed := call("mcp__files__echo " + marshal(t, map[string]any{"text": "hello"}))
	if echoed.Value("isError") != false || agentResultText(echoed) != "echo: hello" {
		t.Fatalf("echoed: %v", agentResultText(echoed))
	}
	if !sameJSON(echoed.Value("details"), map[string]any{"server": "files", "tool": "echo"}) {
		t.Fatalf("details: %v", echoed.Value("details"))
	}
}
