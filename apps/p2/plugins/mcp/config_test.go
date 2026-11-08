package mcp

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// Ports the first case of test/p2/mcp.test.ts: "owner config defaults: stdio per session at the location, shared on the
// host, HTTP shared".

func TestOwnerConfigDefaultsStdioPerSessionAtTheLocationSharedOnTheHostHTTPShared(t *testing.T) {
	config, err := ParseOwnerConfig(`{"mcpServers":{
		"files": { "command": "npx", "args": ["server"] },
		"browser": { "command": "npx", "shared": true },
		"gpu": { "command": "/opt/tool", "runOn": { "env": "gpu" } },
		"docs": { "url": "https://example.com/mcp" },
		"mine": { "url": "https://example.com/mcp", "shared": false },
		"a-b": { "command": "x" },
		"a_b": { "command": "y" }
	}}`)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		name   string
		shared bool
		runOn  any
	}
	var got []row
	for _, server := range config.Servers {
		var runOn any = "http"
		if !server.HTTP {
			runOn = server.RunOn
		}
		got = append(got, row{server.Name, server.Shared, runOn})
	}
	want := []row{
		{"files", false, RunOn{Kind: RunOnLocation}},
		{"browser", true, RunOn{Kind: RunOnHost}},
		{"gpu", false, RunOn{Kind: RunOnEnv, Env: "gpu"}},
		{"docs", true, "http"},
		{"mine", false, "http"},
		{"a-b", false, RunOn{Kind: RunOnLocation}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	if want := []string{`server "a_b": its tools would be named like those of "a-b"`}; !reflect.DeepEqual(config.Errors, want) {
		t.Fatalf("errors %v", config.Errors)
	}
	if _, err := ParseOwnerConfig("{"); err == nil || !strings.Contains(err.Error(), "Invalid mcp.json syntax") {
		t.Fatalf("error %v", err)
	}
	t.Setenv("P2_MCP_TEST_VALUE", "v")
	if got, err := ExpandHostVariables("a ${P2_MCP_TEST_VALUE} $HOME"); err != nil || got != "a v $HOME" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestExpandHostVariablesNamesAnUnsetVariable(t *testing.T) {
	t.Setenv("P2_MCP_TEST_VALUE", "")
	if _, err := ExpandHostVariables("${P2_MCP_TEST_VALUE}"); err != nil {
		t.Fatalf("an empty variable is set: %v", err)
	}
	// Pi's test deletes the variable; `Unsetenv` after `Setenv` restores it at the end of the test.
	if err := os.Unsetenv("P2_MCP_TEST_VALUE"); err != nil {
		t.Fatal(err)
	}
	if _, err := ExpandHostVariables("${P2_MCP_TEST_VALUE}"); err == nil || !strings.Contains(err.Error(), "P2_MCP_TEST_VALUE is not set") {
		t.Fatalf("error %v", err)
	}
}
