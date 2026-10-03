package mcpext_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// Ports packages/coding-agent/test/mcp-command.test.ts (`pi mcp`), run against `mcpext.RunMcpCommand`.
//
// Forced differences, each the same in every case:
//   - the test binary itself is the stdio fixture server (fixtures_test.go `stdio-server`, packages/mcp/test/fixtures/stdio-server.mjs) where upstream spawns `process.execPath FIXTURE`;
//   - PiG's identity replaces Pi's: the command is `pig`, the project directory `.pig` (upstream APP_NAME `pi`, CONFIG_DIR_NAME `.pi`; see cmd/pig/help_upstream.txt).

// serverEntry is one `mcpServers` member; the list keeps upstream's object key order.
type serverEntry struct{ name, json string }

func fixtureServer() string {
	return fmt.Sprintf(`{"command":%q,"args":["-test.run=^$"],"env":{%q:"stdio-server","GORACE":"atexit_sleep_ms=0"}}`, os.Args[0], fixtureEnv)
}

func commandServers() []serverEntry {
	return []serverEntry{
		{"fixture", fixtureServer()},
		{"broken", `{"command":"pi-test-missing-mcp-server"}`},
		{"parked", strings.TrimSuffix(fixtureServer(), "}") + `,"enabled":false}`},
		{"bad", `{"args":["no command"]}`},
	}
}

type commandResult struct {
	exitCode int
	output   string
	agentDir string
}

// runMcp is `run` of mcp-command.test.ts:20-32.
func runMcp(t *testing.T, args []string, servers []serverEntry, dir string) commandResult {
	t.Helper()
	agentDir := dir
	if agentDir == "" {
		agentDir = t.TempDir()
	}
	if servers != nil {
		members := make([]string, len(servers))
		for i, server := range servers {
			members[i] = fmt.Sprintf("%q:%s", server.name, server.json)
		}
		content := `{"mcpServers":{` + strings.Join(members, ",") + `}}`
		if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var output []string
	add := func(line string) { output = append(output, line) }
	exitCode := mcpext.RunMcpCommand(context.Background(), args, mcpext.McpCommandOptions{
		Cwd: agentDir, AgentDir: agentDir, AppName: "pig", ConfigDirName: ".pig", Log: add, Error: add,
	})
	return commandResult{exitCode, strings.Join(output, "\n"), agentDir}
}

func readMcpConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func mustContain(t *testing.T, output, want string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Fatalf("output does not contain %q:\n%s", want, output)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}

func TestMcpCommandListsServersWithTheirStateToolsAndErrors(t *testing.T) {
	// mcp-command.test.ts:47: "lists servers with their state, tools, and errors, and fails while anything is wrong"
	servers := commandServers()
	result := runMcp(t, []string{"list"}, servers, "")
	if result.exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", result.exitCode, result.output)
	}
	mustContain(t, result.output, "fixture: connected, 1 tool (codemode, global)\n")
	mustContain(t, result.output, "  tools: echo")
	broken := "broken: failed (codemode, global)\n  pi-test-missing-mcp-server\n  "
	if runtime.GOOS == "windows" {
		// cross-spawn runs an unresolved command through cmd.exe, which starts, so the reason is the closed connection and cmd.exe's own localized stderr, not a spawn error (mcp/stdio_windows_test.go TestStdioTransportReportsAMissingCommandAsAClosedConnection).
		mustContain(t, result.output, broken)
	} else {
		mustContain(t, result.output, broken+"spawn pi-test-missing-mcp-server ENOENT")
	}
	mustContain(t, result.output, "parked: disabled (codemode, global)")
	mustContain(t, result.output, "config error: ")
	mustContain(t, result.output, `server "bad" needs either "command"`)

	ok := runMcp(t, []string{"list"}, servers[:1], "")
	if ok.exitCode != 0 {
		t.Fatalf("exit code with only the fixture = %d\n%s", ok.exitCode, ok.output)
	}
}

func TestMcpCommandPrintsJSONForScripts(t *testing.T) {
	// mcp-command.test.ts:70
	servers := commandServers()
	result := runMcp(t, []string{"list", "--json"}, []serverEntry{servers[0], servers[2]}, "")
	if result.exitCode != 0 {
		t.Fatalf("exit code = %d\n%s", result.exitCode, result.output)
	}
	var parsed struct {
		Servers []struct {
			Name  string   `json:"name"`
			State string   `json:"state"`
			Tools []string `json:"tools"`
		} `json:"servers"`
	}
	if err := json.Unmarshal([]byte(result.output), &parsed); err != nil {
		t.Fatalf("%v\n%s", err, result.output)
	}
	type summary struct {
		Name  string
		State string
		Tools []string
	}
	var got []summary
	for _, server := range parsed.Servers {
		got = append(got, summary{server.Name, server.State, server.Tools})
	}
	want := []summary{{"fixture", "connected", []string{"echo"}}, {"parked", "disabled", []string{}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("servers = %#v, want %#v", got, want)
	}
}

func TestMcpCommandRejectsUnknownServersAndServersWithoutOAuthForLoginAndLogout(t *testing.T) {
	// mcp-command.test.ts:83
	servers := commandServers()
	login := runMcp(t, []string{"login", "nope"}, servers, "")
	if login.exitCode != 1 || login.output != `No MCP server named "nope". Configured: fixture, broken, parked.` {
		t.Fatalf("login nope = %d %q", login.exitCode, login.output)
	}
	logout := runMcp(t, []string{"logout", "fixture"}, servers, "")
	if logout.exitCode != 1 || logout.output != `MCP server "fixture" does not use OAuth. Only HTTP servers without an Authorization header do.` {
		t.Fatalf("logout fixture = %d %q", logout.exitCode, logout.output)
	}
	if frobnicate := runMcp(t, []string{"frobnicate"}, servers, ""); frobnicate.exitCode != 1 {
		t.Fatalf("frobnicate exit code = %d", frobnicate.exitCode)
	}
}

func TestMcpCommandAddsStdioServersAndPassesOptionsAfterTheCommandThrough(t *testing.T) {
	// mcp-command.test.ts:98
	added := runMcp(t, []string{"add", "--env", "A=1", "--env", "B=x=y", "files", "--", "npx", "-y", "server", "--root", "."}, nil, "")
	if added.exitCode != 0 {
		t.Fatalf("exit code = %d\n%s", added.exitCode, added.output)
	}
	mustContain(t, added.output, `Added global MCP server "files"`)
	configPath := filepath.Join(added.agentDir, "mcp.json")
	want := map[string]any{"mcpServers": map[string]any{"files": map[string]any{
		"command": "npx", "args": []any{"-y", "server", "--root", "."}, "env": map[string]any{"A": "1", "B": "x=y"},
	}}}
	if got := readMcpConfig(t, configPath); !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %#v, want %#v", got, want)
	}

	// Without `--`, options after the command belong to the command too.
	replaced := runMcp(t, []string{"add", "files", "node", "server.js", "--port", "1"}, nil, added.agentDir)
	mustContain(t, replaced.output, `Replaced global MCP server "files"`)
	want = map[string]any{"mcpServers": map[string]any{"files": map[string]any{"command": "node", "args": []any{"server.js", "--port", "1"}}}}
	if got := readMcpConfig(t, configPath); !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %#v, want %#v", got, want)
	}
}

func TestMcpCommandAddsHTTPServersAndKeepsOtherContentOfTheFile(t *testing.T) {
	// mcp-command.test.ts:121
	servers := commandServers()
	result := runMcp(t, []string{
		"add", "docs", "--url", "https://example.com/mcp", "--bearer-token-env-var", "DOCS_TOKEN", "--header", "X-Team=core", "--exposure", "direct",
		"--description", "Product docs",
	}, servers[:1], "")
	if result.exitCode != 0 {
		t.Fatalf("exit code = %d\n%s", result.exitCode, result.output)
	}
	if strings.Contains(result.output, "mcp login") {
		t.Fatalf("output offers a sign-in for a server with an Authorization header:\n%s", result.output)
	}
	var fixture map[string]any
	if err := json.Unmarshal([]byte(servers[0].json), &fixture); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(result.agentDir, "mcp.json")
	want := map[string]any{"mcpServers": map[string]any{
		"fixture": fixture,
		"docs": map[string]any{
			"url":         "https://example.com/mcp",
			"headers":     map[string]any{"X-Team": "core", "Authorization": "Bearer ${DOCS_TOKEN}"},
			"exposure":    "direct",
			"description": "Product docs",
		},
	}}
	if got := readMcpConfig(t, configPath); !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %#v, want %#v", got, want)
	}

	oauth := runMcp(t, []string{"add", "sentry", "--url", "https://mcp.sentry.dev/mcp", "--oauth-client-id", "pi", "--oauth-client-name", "Claude Code"}, nil, result.agentDir)
	mustContain(t, oauth.output, "If it requires sign-in: pig mcp login sentry")
	sentry := readMcpConfig(t, configPath)["mcpServers"].(map[string]any)["sentry"].(map[string]any)
	if sentry["url"] != "https://mcp.sentry.dev/mcp" || !reflect.DeepEqual(sentry["oauth"], map[string]any{"clientId": "pi", "clientName": "Claude Code"}) {
		t.Fatalf("sentry = %#v", sentry)
	}
}

// cli.ts add writes the value validateMcpServerConfig returns (addMcpServerConfig(path, name, validated)), a copy with exposure aliases resolved (mcp-servers.ts resolveExposureAliases), so `--exposure codemode-deferred` is saved as `codemode` in its place among the members.
func TestMcpCommandAddSavesTheResolvedExposureAlias(t *testing.T) {
	added := runMcp(t, []string{"add", "docs", "--url", "https://example.com/mcp", "--exposure", "codemode-deferred", "--description", "Docs"}, nil, "")
	if added.exitCode != 0 {
		t.Fatalf("exit code = %d\n%s", added.exitCode, added.output)
	}
	data, err := os.ReadFile(filepath.Join(added.agentDir, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `"docs": {
      "url": "https://example.com/mcp",
      "exposure": "codemode",
      "description": "Docs"
    }`; !strings.Contains(string(data), want) {
		t.Fatalf("mcp.json = %s, want the entry %s", data, want)
	}
}

func TestMcpCommandRejectsInvalidAddInvocationsWithoutWriting(t *testing.T) {
	// mcp-command.test.ts:163
	cases := [][]string{
		{"add", "x"},
		{"add", "x", "--url", "https://example.com", "--", "cmd"},
		{"add", "bad name", "--", "cmd"},
		{"add", "x", "--url", "ftp://example.com"},
		{"add", "x", "--env", "A=1", "--url", "https://example.com"},
		{"add", "x", "--header", "A=1", "--", "cmd"},
		{"add", "x", "--env", "NOVALUE", "--", "cmd"},
		{"add", "x", "--exposure", "loud", "--", "cmd"},
	}
	for _, args := range cases {
		result := runMcp(t, args, nil, "")
		if result.exitCode != 1 {
			t.Errorf("%s: exit code = %d, want 1\n%s", strings.Join(args, " "), result.exitCode, result.output)
		}
		if fileExists(filepath.Join(result.agentDir, "mcp.json")) {
			t.Errorf("%s: wrote mcp.json", strings.Join(args, " "))
		}
	}
}

func TestMcpCommandAddsAndRemovesProjectServers(t *testing.T) {
	// mcp-command.test.ts:182
	added := runMcp(t, []string{"add", "-l", "local", "--", "node", "server.js"}, nil, "")
	mustContain(t, added.output, "The project is not trusted")
	projectConfig := filepath.Join(added.agentDir, ".pig", "mcp.json")
	want := map[string]any{"mcpServers": map[string]any{"local": map[string]any{"command": "node", "args": []any{"server.js"}}}}
	if got := readMcpConfig(t, projectConfig); !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %#v, want %#v", got, want)
	}

	wrongScope := runMcp(t, []string{"remove", "local"}, nil, added.agentDir)
	if wrongScope.exitCode != 1 {
		t.Fatalf("remove in the wrong scope: exit code = %d\n%s", wrongScope.exitCode, wrongScope.output)
	}
	mustContain(t, wrongScope.output, "It is defined in "+projectConfig+"; use --local.")

	removed := runMcp(t, []string{"remove", "local", "--local"}, nil, added.agentDir)
	if removed.exitCode != 0 {
		t.Fatalf("remove --local: exit code = %d\n%s", removed.exitCode, removed.output)
	}
	mustContain(t, removed.output, `Removed project MCP server "local"`)
	if got := readMcpConfig(t, projectConfig); !reflect.DeepEqual(got, map[string]any{"mcpServers": map[string]any{}}) {
		t.Fatalf("config = %#v", got)
	}
}

func TestMcpCommandRemovesGlobalServers(t *testing.T) {
	// mcp-command.test.ts:203
	result := runMcp(t, []string{"remove", "broken"}, commandServers(), "")
	if result.exitCode != 0 {
		t.Fatalf("exit code = %d\n%s", result.exitCode, result.output)
	}
	// toEqual over Object.keys: the remaining servers keep their file order.
	data, err := os.ReadFile(filepath.Join(result.agentDir, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	var servers struct {
		McpServers json.RawMessage `json:"mcpServers"`
	}
	if err := decoder.Decode(&servers); err != nil {
		t.Fatal(err)
	}
	members := json.NewDecoder(strings.NewReader(string(servers.McpServers)))
	if _, err := members.Token(); err != nil {
		t.Fatal(err)
	}
	for members.More() {
		key, err := members.Token()
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, key.(string))
		var skip json.RawMessage
		if err := members.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"fixture", "parked", "bad"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("servers = %v, want %v", names, want)
	}
	missing := runMcp(t, []string{"remove", "broken"}, nil, result.agentDir)
	if missing.exitCode != 1 {
		t.Fatalf("removing a missing server: exit code = %d", missing.exitCode)
	}
	mustContain(t, missing.output, `No global MCP server named "broken"`)
}
