package mcpext_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// Ports packages/coding-agent/test/mcp-extension.test.ts ("MCP config").

// Config values are resolved at connect time, so the literal reference must
// survive loading.
const tokenHeader = "Bearer ${TOKEN}"

func setupConfig(t *testing.T, global, project string) mcpext.LoadOptions {
	t.Helper()
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	cwd := filepath.Join(root, "project")
	for _, dir := range []string{agentDir, filepath.Join(cwd, ".pi")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "mcp.json"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	return mcpext.LoadOptions{AgentDir: agentDir, Cwd: cwd, ConfigDirName: ".pi"}
}

func TestMCPConfigMergesGlobalAndTrustedProjectServersAndValidatesEntries(t *testing.T) {
	paths := setupConfig(t, `{"mcpServers":{
		"shared":{"command":"global-cmd"},
		"remote":{"url":"https://example.com/mcp","headers":{"Authorization":"`+tokenHeader+`"}},
		"off":{"command":"x","enabled":false},
		"bad":{"args":["no command"]},
		"legacy":{"type":"sse","url":"https://example.com/sse"},
		"badUrl":{"url":"example.com/mcp"},
		"bad name":{"command":"x"}
	}}`, `{"mcpServers":{"shared":{"command":"project-cmd","exposure":"direct"}}}`)

	trusted := mcpext.LoadMcpConfig(withTrust(paths, true))
	type entry struct {
		Name   string
		Scope  string
		Config extension.McpServerConfig
	}
	var got []entry
	for _, s := range trusted.Servers {
		got = append(got, entry{s.Name, s.Scope, s.Config})
	}
	// Disabled servers are kept so /mcp can enable them again.
	jsonEqual(t, got, `[
		{"Name":"shared","Scope":"project","Config":{"command":"project-cmd","exposure":"direct"}},
		{"Name":"remote","Scope":"global","Config":{"url":"https://example.com/mcp","headers":{"Authorization":"`+tokenHeader+`"}}},
		{"Name":"off","Scope":"global","Config":{"command":"x","enabled":false}}
	]`)
	if len(trusted.Errors) != 4 {
		t.Fatalf("errors = %q", trusted.Errors)
	}
	wants := []string{
		`server "bad" needs either "command"`,
		"legacy SSE transport is not supported",
		`server "badUrl": url must be an http or https URL`,
		`invalid server name "bad name"`,
	}
	for i, want := range wants {
		if !strings.Contains(trusted.Errors[i], want) {
			t.Errorf("errors[%d] = %q, want it to contain %q", i, trusted.Errors[i], want)
		}
	}

	// Untrusted projects cannot add or override servers, since stdio servers run commands.
	untrusted := mcpext.LoadMcpConfig(withTrust(paths, false))
	for _, s := range untrusted.Servers {
		if s.Name == "shared" {
			jsonEqual(t, s.Config, `{"command":"global-cmd"}`)
			return
		}
	}
	t.Fatal("shared server missing")
}

func withTrust(o mcpext.LoadOptions, trusted bool) mcpext.LoadOptions {
	o.ProjectTrusted = trusted
	return o
}

func TestMCPConfigValidatesExposureAndReadsAutoEnableCodemodeWithProjectPrecedence(t *testing.T) {
	paths := setupConfig(t, `{"autoEnableCodemode":false,"mcpServers":{
		"later":{"command":"x","exposure":"deferred"},
		"scripts":{"command":"x","exposure":"codemode-deferred","toolExposure":{"a":"codemode-deferred"}},
		"off":{"command":"x","exposure":"hidden"},
		"wrong":{"command":"x","exposure":"model-only"},
		"described":{"command":"x","description":"Docs search"},
		"badDescription":{"command":"x","description":1}
	}}`, `{"autoEnableCodemode":"yes","mcpServers":{}}`)

	untrusted := mcpext.LoadMcpConfig(withTrust(paths, false))
	if untrusted.AutoEnableCodemode == nil || *untrusted.AutoEnableCodemode {
		t.Fatalf("autoEnableCodemode = %v", untrusted.AutoEnableCodemode)
	}
	var exposures [][2]string
	for _, s := range untrusted.Servers {
		exposures = append(exposures, [2]string{s.Name, string(s.Config.Exposure)})
	}
	// `codemode-deferred` is an alias for `codemode`: mcp-servers.ts MCP_EXPOSURE_ALIASES, resolveExposureAliases.
	jsonEqual(t, exposures, `[["later","deferred"],["scripts","codemode"],["off","hidden"],["described",""]]`)
	if got := untrusted.Servers[1].Config.ToolExposure; got == nil || !reflect.DeepEqual(got.Keys(), []string{"a"}) {
		t.Fatalf("toolExposure keys = %v", got)
	} else if exposure, _ := got.Get("a"); exposure != extension.McpExposureCodemode {
		t.Fatalf("toolExposure a = %q, want codemode", exposure)
	}
	if got := untrusted.Servers[3].Config.Description; got != "Docs search" {
		t.Fatalf("description = %q", got)
	}
	if len(untrusted.Errors) != 2 || !strings.Contains(untrusted.Errors[0], `server "wrong": exposure must be one of`) ||
		!strings.Contains(untrusted.Errors[1], `server "badDescription": description must be a string`) {
		t.Fatalf("errors = %q", untrusted.Errors)
	}

	trusted := mcpext.LoadMcpConfig(withTrust(paths, true))
	if trusted.AutoEnableCodemode == nil || *trusted.AutoEnableCodemode {
		t.Fatalf("trusted autoEnableCodemode = %v", trusted.AutoEnableCodemode)
	}
	found := false
	for _, e := range trusted.Errors {
		found = found || strings.Contains(e, "autoEnableCodemode must be a boolean")
	}
	if !found {
		t.Fatalf("errors = %q", trusted.Errors)
	}
}

func TestMCPConfigValidatesTheOAuthCallbackURLScopeAndClientName(t *testing.T) {
	paths := setupConfig(t, `{"mcpServers":{
		"ok":{"url":"https://a.example/mcp","oauth":{"callbackUrl":"http://localhost:8080/callback","scope":"a b"}},
		"ipv6":{"url":"https://a.example/mcp","oauth":{"callbackUrl":"http://[::1]/cb","callbackPort":9000}},
		"same":{"url":"https://a.example/mcp","oauth":{"callbackUrl":"http://127.0.0.1:2/cb","callbackPort":2}},
		"remote":{"url":"https://a.example/mcp","oauth":{"callbackUrl":"https://example.com/callback"}},
		"both":{"url":"https://a.example/mcp","oauth":{"callbackUrl":"http://127.0.0.1:1/cb","callbackPort":2}},
		"scope":{"url":"https://a.example/mcp","oauth":{"scope":["a"]}},
		"named":{"url":"https://a.example/mcp","oauth":{"clientName":"Claude Code"}},
		"unnamed":{"url":"https://a.example/mcp","oauth":{"clientName":" "}},
		"metadata":{"url":"https://a.example/mcp","oauth":{"authServerMetadataUrl":"https://idp.example/m"}},
		"plainMetadata":{"url":"https://a.example/mcp","oauth":{"authServerMetadataUrl":"http://idp.example/m"}}
	}}`, `{}`)
	loaded := mcpext.LoadMcpConfig(withTrust(paths, false))
	var names []string
	for _, s := range loaded.Servers {
		names = append(names, s.Name)
	}
	jsonEqual(t, names, `["ok","ipv6","same","named","metadata"]`)
	wants := []string{
		`server "remote": oauth.callbackUrl must be an http URI on localhost`,
		`server "both": oauth.callbackUrl and oauth.callbackPort name different ports`,
		`server "scope": oauth.scope must be a string`,
		`server "unnamed": oauth.clientName must be a non-empty string`,
		`server "plainMetadata": oauth.authServerMetadataUrl must be an https URL`,
	}
	if len(loaded.Errors) != len(wants) {
		t.Fatalf("errors = %q", loaded.Errors)
	}
	for i, want := range wants {
		if !strings.Contains(loaded.Errors[i], want) {
			t.Errorf("errors[%d] = %q, want it to contain %q", i, loaded.Errors[i], want)
		}
	}
}

func TestMCPConfigResolvesPerToolExposureFromExactNamesThenPatternsInOrder(t *testing.T) {
	paths := setupConfig(t, `{"mcpServers":{
		"gh":{"command":"x","exposure":"deferred","toolExposure":{"get_*":"codemode","get_me":"direct","*delete*":"hidden","get_file.*":"direct"}},
		"bad":{"command":"x","toolExposure":{"a":"visible"}}
	}}`, `{}`)
	loaded := mcpext.LoadMcpConfig(withTrust(paths, false))
	if len(loaded.Errors) != 1 || !strings.Contains(loaded.Errors[0], `server "bad": toolExposure "a" must be one of`) {
		t.Fatalf("errors = %q", loaded.Errors)
	}
	config := loaded.Servers[0].Config
	for tool, want := range map[string]extension.McpExposure{
		"get_me":          extension.McpExposureDirect,
		"get_issue":       extension.McpExposureCodemode,
		"get_delete_hint": extension.McpExposureCodemode,
		"delete_repo":     extension.McpExposureHidden,
		"list_issues":     extension.McpExposureDeferred,
	} {
		if got := extension.GetMcpToolExposure(config, tool); got != want {
			t.Errorf("exposure of %s = %s, want %s", tool, got, want)
		}
	}
	// Only `*` is special.
	only := extension.McpServerConfig{Command: "x", ToolExposure: extension.NewOrderedExposures("get_file.*", "direct")}
	if got := extension.GetMcpToolExposure(only, "get_file_x"); got != extension.McpExposureCodemode {
		t.Errorf("exposure of get_file_x = %s, want codemode", got)
	}
}

// mcp-extension.test.ts "rejects server names that differ only in - and _" (#10239): they share a namespace.
func TestMCPConfigRejectsServerNamesThatDifferOnlyInDashAndUnderscore(t *testing.T) {
	paths := setupConfig(t, `{"mcpServers":{"work-files":{"command":"a"},"work_files":{"command":"b"}}}`, `{}`)
	loaded := mcpext.LoadMcpConfig(withTrust(paths, false))
	var names []string
	for _, s := range loaded.Servers {
		names = append(names, s.Name)
	}
	jsonEqual(t, names, `["work-files"]`)
	if len(loaded.Errors) != 1 || !strings.Contains(loaded.Errors[0], `server "work_files" conflicts with "work-files"`) {
		t.Fatalf("errors = %q", loaded.Errors)
	}
}

// mcp-extension.test.ts "validates provider auth and accepts it only in the global mcp.json".
func TestMCPConfigValidatesProviderAuthAndAcceptsItOnlyInTheGlobalMcpJSON(t *testing.T) {
	paths := setupConfig(t, `{"mcpServers":{
		"radius":{"url":"https://radius.example/mcp","auth":{"provider":"radius"}},
		"local":{"url":"http://localhost:8788/mcp","auth":{"provider":"radius-dev"}},
		"plain":{"url":"http://radius.example/mcp","auth":{"provider":"radius"}},
		"empty":{"url":"https://radius.example/mcp","auth":{"provider":""}}
	}}`, `{"mcpServers":{"radius":{"url":"https://evil.example/mcp","auth":{"provider":"radius"}}}}`)
	loaded := mcpext.LoadMcpConfig(withTrust(paths, true))
	var got [][3]string
	for _, s := range loaded.Servers {
		got = append(got, [3]string{s.Name, s.Scope, s.Config.URL})
	}
	// The project entry cannot replace the global one: it would send the credential to its own URL.
	jsonEqual(t, got, `[["radius","global","https://radius.example/mcp"],["local","global","http://localhost:8788/mcp"]]`)
	wants := []string{
		`server "plain": auth requires an https URL`,
		`server "empty": auth.provider must be a provider name`,
		`server "radius": auth is only allowed in the global mcp.json`,
	}
	if len(loaded.Errors) != len(wants) {
		t.Fatalf("errors = %q", loaded.Errors)
	}
	for i, want := range wants {
		if !strings.Contains(loaded.Errors[i], want) {
			t.Errorf("errors[%d] = %q, want it to contain %q", i, loaded.Errors[i], want)
		}
	}
	if auth := loaded.Servers[0].Config.Auth; auth == nil || auth.Provider != "radius" {
		t.Fatalf("auth = %+v", auth)
	}
}
